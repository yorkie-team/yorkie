// Shared Claude Agent SDK wrapper for the agent pipeline — ONE place that opens
// a model session, so the read-only tool invariant is stated once and can be
// tested, rather than living as a hardcoded array inside a single caller.
//
// Extracted from review-panel.mjs, which called the SDK inline. Two changes come
// with the move, both about making the read-only claim real rather than assumed:
//
//   1. The tool grant is a REQUIRED, VALIDATED parameter checked against the
//      `PERMITTED_TOOLS` allow-list below, instead of a hardcoded array. Sharing
//      a wrapper invites widening it for everyone; an allow-list makes that a
//      rejected call rather than a one-word edit.
//   2. The session now sets `tools` as well as `allowedTools`. Only the former
//      restricts what exists; the latter just pre-approves. See the call site.
//
// A consumer that genuinely needs to execute something must do it in its own
// trusted code, acting on data the model returned — never by holding the
// capability itself.
//
// SDK: @anthropic-ai/claude-agent-sdk (imported lazily so the pure helpers here
// are unit-testable without the dependency installed). Verified against
// @anthropic-ai/claude-agent-sdk 0.3.217 (pinned + lockfiled): outputFormat:
// {type:'json_schema'}, result.structured_output, permissionMode 'dontAsk',
// settingSources:[], maxTurns all exist; the SDK reads CLAUDE_CODE_OAUTH_TOKEN.
// `systemPrompt` also accepts `string[]` with SYSTEM_PROMPT_DYNAMIC_BOUNDARY as a
// standalone element, which is how a caller marks the cacheable prefix — see the
// mirrored constant below.

// The credential pool. Not the SDK, so a static import is fine here — the lazy
// rule above exists to keep the pure helpers testable without the SDK on disk,
// and token-pool.mjs has no dependencies at all.
import { createTokenPool, poolEnvNames } from "./token-pool.mjs";
import { SESSION_LIMIT_RE, classifyInfraError, redactSecrets, renderInfraError } from "./redact.mjs";

/**
 * The COMPLETE set of tools any agent spawned through this wrapper may ever be
 * granted. Callers pass a subset; anything else is refused.
 *
 * This is an ALLOW-list, and that shape is the whole point. A deny-list of
 * dangerous names cannot express "read-only" in this SDK, for three independent
 * reasons — each of which silently grants capability:
 *
 *   1. `allowedTools` entries are permission RULES, not bare names. `Bash(git
 *      diff:*)` is a legal entry, and since `allowedTools` is an auto-approve
 *      list (see askStructured), it would not merely evade a name-based check —
 *      it would PRE-APPROVE that shell invocation past `permissionMode:
 *      'dontAsk'`. Whitespace and `mcp__server__exec` forms slip through the
 *      same way.
 *   2. The tool namespace moves. Pinned SDK 0.3.217 ships `Agent` (spawns
 *      subagents, which inherit the parent's tools), `REPL`, `Workflow`,
 *      `CronCreate` and `RemoteTrigger` — none of which an author of a
 *      hand-written deny-list in 2026 would have thought to name. A deny-list
 *      is wrong by default and only accidentally right.
 *   3. It is unfalsifiable in review: you cannot tell by reading it whether it
 *      is complete.
 *
 * An allow-list inverts all three: unknown, scoped, aliased and future names are
 * refused because they are not present, not because someone predicted them.
 *
 * Read/Grep/Glob is the entire intended surface. Every agent here reads
 * UNTRUSTED input — a branch diff, an issue body, a repository checkout — so a
 * consumer that genuinely needs to run something must do it in its own trusted
 * code, from data the model returned, rather than holding the capability itself.
 */
export const PERMITTED_TOOLS = Object.freeze(["Read", "Grep", "Glob"]);

/**
 * In-process MCP tools this repo implements itself, permitted by EXACT name.
 *
 * Reason (2) above names `mcp__server__exec` as a deny-list bypass, so admitting
 * an MCP name at all needs justifying rather than assuming. The distinction is
 * ownership, not naming:
 *
 *   * A conventional MCP grant trusts a server the SDK connects to — another
 *     process, whose capabilities this file cannot see or bound.
 *   * These are `createSdkMcpServer` tools defined in THIS repo, running in THIS
 *     process, whose handler is `scripts/agent/hunt-tool.mjs` and whose entire
 *     reachable surface is `node <cli-bin> <argv…>` behind `assertSafeArgv`,
 *     a replaced environment, and a probe budget.
 *
 * So the capability is bounded by code in this repository, reviewed alongside it,
 * rather than by a name. The list stays SEPARATE from `PERMITTED_TOOLS` for two
 * reasons: the two are wired to different SDK levers (built-in `tools` versus
 * `mcpServers`), and keeping them apart preserves the meaning of the existing
 * tests that assert no MCP name reaches a review-panel session.
 *
 * Matching is exact, so `mcp__wafflebase__exec`, `mcp__other__run` and every
 * future MCP name remain refused because they are absent — the same inversion
 * that makes the allow-list above work.
 */
export const PERMITTED_MCP_TOOLS = Object.freeze(["mcp__wafflebase__run", "mcp__wafflebase__ui"]);

const PERMITTED_SET = new Set(PERMITTED_TOOLS);

/**
 * Reasoning-effort levels the SDK accepts (`EffortLevel`, sdk.d.ts:539 in the
 * pinned 0.3.217). Effort controls how much the model thinks AND how many tool
 * calls it makes to get there, so on this pipeline it is a COST dial: panel
 * spend tracks agentic turns almost exactly, and turns are what effort moves.
 *
 * Frozen and listed literally rather than derived, for the same reason
 * `PERMITTED_TOOLS` is: if the SDK ever drops a level, a literal list turns that
 * into a failing test instead of a session that silently runs at the default.
 */
export const EFFORT_LEVELS = Object.freeze(["low", "medium", "high", "xhigh", "max"]);

const EFFORT_SET = new Set(EFFORT_LEVELS);

/**
 * Validate an effort value, returning it unchanged. `undefined` means "unset" and
 * passes through — the caller then omits the key entirely and takes the SDK
 * default, exactly as `maxTurns` does.
 *
 * Everything else throws, including `null` and a mis-cased or misspelled level.
 * That strictness is the point: an unrecognised value would otherwise be dropped
 * and the session would run at the default `high`, which is a silent COST
 * regression — no error, no changed output, nothing in the logs. The same class
 * of failure `assertBoundaryMatchesSdk` exists to prevent, one option over.
 */
export function assertEffort(effort) {
  if (effort === undefined) return undefined;
  if (typeof effort !== "string" || !EFFORT_SET.has(effort)) {
    throw new Error(
      `askStructured: \`effort\` must be one of ${EFFORT_LEVELS.join(", ")} ` +
        `(got: ${JSON.stringify(effort)}). Omit it to take the SDK default.`,
    );
  }
  return effort;
}
const PERMITTED_MCP_SET = new Set(PERMITTED_MCP_TOOLS);

/** Is this grant one of the in-process MCP tools rather than a built-in? */
export function isMcpTool(name) {
  return PERMITTED_MCP_SET.has(name);
}

/**
 * Validate a caller's tool grant, throwing on anything outside PERMITTED_TOOLS.
 *
 * Required rather than defaulted ON PURPOSE. A default would let a new caller
 * inherit read-only access silently and then widen it at the call site with no
 * signal that it had crossed a boundary; requiring the argument makes every
 * grant an explicit, greppable decision.
 *
 * Matching is exact against the canonical names — deliberately strict. `" Read"`
 * and `"read"` are refused rather than normalized, because a validator that
 * repairs its input is guessing at intent, and the one place you do not want
 * guessing is a capability gate. The caller's fix is to pass the canonical name.
 *
 * Fails closed on every ambiguity: a non-array, an empty array, a non-string
 * entry, or any entry not in the permitted set. An empty array is rejected
 * because the SDK would treat it as "no tools" and the agent would silently be
 * unable to read anything — a session that burns quota and returns nothing.
 * (`auth-smoke.mjs` legitimately wants `allowedTools: []` for a pure auth probe,
 * which is why it calls the SDK directly and does not come through here.)
 */
export function assertAllowedTools(allowedTools) {
  if (!Array.isArray(allowedTools) || allowedTools.length === 0) {
    throw new Error(
      "askStructured: `allowedTools` is required and must be a non-empty array " +
        `(got: ${JSON.stringify(allowedTools)})`,
    );
  }
  for (const t of allowedTools) {
    if (typeof t !== "string" || t.trim() === "") {
      throw new Error(`askStructured: \`allowedTools\` entries must be non-empty strings (got: ${JSON.stringify(t)})`);
    }
    if (!PERMITTED_SET.has(t) && !PERMITTED_MCP_SET.has(t)) {
      throw new Error(
        `askStructured: tool ${JSON.stringify(t)} is not permitted. Agents here read ` +
          `untrusted input, so the grant is limited to read-only inspection ` +
          `(${PERMITTED_TOOLS.join(", ")}) plus this repo's own in-process MCP tools ` +
          `(${PERMITTED_MCP_TOOLS.join(", ")}). Scoped permission rules (e.g. ` +
          `"Bash(git diff:*)"), other MCP tool names and non-canonical spellings are ` +
          `refused by the same rule.`,
      );
    }
  }
  return [...allowedTools];
}

/**
 * Mirrors `SYSTEM_PROMPT_DYNAMIC_BOUNDARY` from the SDK (sdk.d.ts:6810 in the
 * pinned 0.3.217). A `systemPrompt` ARRAY containing this marker as a standalone
 * element is split in two: blocks BEFORE it are eligible for cross-session
 * prompt caching, blocks after it are not.
 *
 * Kept as a local literal rather than re-exported from the SDK so the pure
 * prompt builders — here and in review-panel.mjs — need no static SDK import.
 * The agent test lane (`scripts/verify-self.mjs`) deliberately runs with the SDK
 * NOT installed, which works only because the import stays lazy.
 *
 * A wrong marker is not an error to the SDK. It is just a prompt block that
 * silently never caches: a pure cost regression with no failing test, no changed
 * output, and nothing in the logs. `assertBoundaryMatchesSdk` closes that hole at
 * the one place the real SDK is loaded, so the mirror cannot drift unnoticed.
 */
export const SYSTEM_PROMPT_DYNAMIC_BOUNDARY = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__";

/**
 * Throw unless the SDK still agrees with the local mirror above.
 *
 * Takes the imported namespace as an ARGUMENT rather than importing it, which is
 * what keeps this checkable without the SDK on disk — the same constraint that
 * makes the mirror necessary at all. A missing export counts as drift: an SDK
 * that no longer declares the marker cannot be honoring one.
 */
export function assertBoundaryMatchesSdk(sdk) {
  const actual = (sdk || {}).SYSTEM_PROMPT_DYNAMIC_BOUNDARY;
  if (actual !== SYSTEM_PROMPT_DYNAMIC_BOUNDARY) {
    throw new Error(
      "SYSTEM_PROMPT_DYNAMIC_BOUNDARY drifted from the SDK " +
        `(local=${JSON.stringify(SYSTEM_PROMPT_DYNAMIC_BOUNDARY)}, ` +
        `sdk=${JSON.stringify(actual)}). Prompt caching would silently stop ` +
        "working — update the mirror in ask.mjs.",
    );
  }
  return actual;
}

/**
 * A session/usage limit resets on a fixed schedule, often hours out, so no
 * in-run retry can clear it. This is the ONE thing that overrides an otherwise
 * retryable status, so the pattern is deliberately narrow and anchored.
 *
 * It replaced `/session limit|usage limit|quota|rate limit|resets?\b/i`, which
 * over-matched badly in both directions:
 *   - `resets?\b` matched `ECONNRESET`, `read ECONNRESET` and `connection reset
 *     by peer` (word boundary at end-of-string), turning ordinary transport
 *     blips into "give up immediately".
 *   - `rate limit` made every plain 429 non-retryable, contradicting the very
 *     docblock above it — a rate limit is the canonical thing you retry.
 *   - bare `quota` is ambiguous enough to catch unrelated messages.
 * Dropping those is safe in the cheap direction: if a genuine limit is now
 * retried, the cost is a few seconds of backoff before the same failure is
 * reported correctly. Whereas mis-classifying a transient error as permanent
 * pages a human for nothing, which is the expensive mistake.
 */
// Imported, not re-declared. This file and redact.mjs each held their own copy
// with a comment promising they matched; see SESSION_LIMIT_RE's docblock in
// redact.mjs for the outage that promise did not survive.

/**
 * Is another attempt at this HTTP status plausibly worth making?
 *
 * Keyed on the status the API actually returned rather than on prose, because
 * the message text is a human string that varies by provider and version while
 * the status is a contract. Absent status means a transport/network failure
 * (`fetch failed`, `ECONNRESET`) — those are the most retryable of all.
 *
 * 4xx other than 408/429 are deterministic: a 401 from a bad token or a 400 from
 * a malformed schema will fail identically three times, so retrying only delays
 * the real error. That is a change in behavior from the previous regex-only rule,
 * and an intentional one.
 */
function isRetryableStatus(status) {
  if (status == null) return true; // no status → transport/network → transient
  const s = Number(status);
  if (!Number.isFinite(s)) return true; // unparseable → treat as transient
  if (s === 408 || s === 429) return true; // request timeout / rate limited
  return s >= 500; // 500/502/503/529 overload → transient; other 4xx → not
}

/**
 * Classify an SDK `result` message. The SDK reports API/quota failures as
 * subtype "success" with `is_error: true` (+ `api_error_status`, and a human
 * `result` string like "You've hit your session limit · resets 3:30pm (UTC)"),
 * so "subtype === success" alone is NOT proof the model ran. Returns one of:
 *   { ok:true, output }                                  — real structured verdict
 *   { ok:false, kind:'api-error', status, detail, retryable } — API/quota failure
 *   { ok:false, kind:'no-output', detail, retryable:false }   — ran but no verdict
 *
 * Retryability = "could another attempt succeed?", decided by `isRetryableStatus`
 * and overridden to false only for an explicit session/usage limit. A plain 429
 * IS retryable even when its message mentions rate limiting; a 429 that says
 * "session limit" is not.
 */
/**
 * SDK result subtypes for a DETERMINISTIC run-limit failure: the model ran fine
 * and hit a ceiling we set. Retrying one is pure waste — the same ceiling applies
 * to the retry, so it burns 3× the cost to fail identically.
 *
 * These MUST be recognised before the api-error branch below, because they look
 * like an API error to a naive check: `is_error: true` with no `api_error_status`
 * and no `result` text. Observed live — a verifier at `maxTurns: 8` returned
 * `{subtype:"error_max_turns", terminal_reason:"max_turns", num_turns:9,
 * result:""}`, which the old rule reported as a retryable
 * "API error: unknown" and which silently cost two already-reproduced findings.
 */
const LIMIT_SUBTYPES = new Set([
  "error_max_turns",
  "error_max_budget_usd",
  "error_max_structured_output_retries",
]);

/** Human-readable detail from the SDK's `errors` array, when it carries one. */
function describeErrors(m) {
  const errs = Array.isArray(m.errors) ? m.errors : [];
  const parts = errs
    .map((e) => (typeof e === "string" ? e : e && typeof e === "object" ? e.message || e.type || "" : ""))
    .filter(Boolean);
  return parts.join("; ");
}

/**
 * The publishable half of a failure, built once so no branch can forget it.
 *
 * Returns the three fields every failure carries out of `classifyResult`:
 *
 *   `code`   — a `redact.mjs` INFRA_CODES key. The standardized, closed-vocabulary
 *              identity of the failure. Renderers switch on THIS.
 *   `reason` — the publishable sentence, assembled from the vocabulary plus typed
 *              primitives. Contains no upstream text by construction.
 *   `detail` — the upstream text, REDACTED. For the run log only. It stays on the
 *              object because operators need it when debugging a live outage, and
 *              because dropping it would only push callers back to the raw message.
 *
 * The split is the point. `detail` was previously the ONLY field, so every renderer
 * that wanted to say something about a failure had upstream prose as its sole
 * option, and one of them published it to a public pull request. Now the useful
 * thing to publish is also the safe thing.
 */
function safeFailure({ kind, status, detail, turns = null, subtype = null }) {
  const classified = classifyInfraError({ kind, status, detail, turns, subtype });
  return {
    code: classified.code,
    reason: renderInfraError(classified),
    detail: redactSecrets(detail),
  };
}

export function classifyResult(message) {
  const m = message || {};
  const isLimit = LIMIT_SUBTYPES.has(m.subtype) || m.terminal_reason === "max_turns";
  const isApiError = Boolean(m.is_error || m.api_error_status || m.terminal_reason === "api_error");

  // A verdict is trustworthy only when NOTHING flags the session as failed.
  //
  // The `!isLimit && !isApiError` guard is the point. Testing
  // `subtype === "success" && structured_output` alone was a FAIL-OPEN: the SDK
  // reports API failures as `subtype: "success"` with `is_error: true` (the whole
  // reason this function exists), so a failed session that happened to carry
  // partial structured output was returned as a real verdict. In the review panel
  // that means a lens's findings from a failed session reach the merge gate; in the
  // issue hunter it means a verifier's "confirmed" from a failed session can cause
  // a report. Both directions are wrong, and in a fail-quiet gate a fail-open path
  // is the one thing that cannot be tolerated.
  //
  // Strictly tightening: a genuinely successful result sets none of these flags, so
  // no previously-accepted verdict is now rejected.
  if (m.subtype === "success" && m.structured_output && !isLimit && !isApiError) {
    return { ok: true, output: m.structured_output };
  }
  // Deterministic ceilings BEFORE the api-error branch, which would otherwise
  // claim them and mark them retryable.
  if (isLimit) {
    const reason = String(m.subtype ?? m.terminal_reason ?? "limit");
    const extra = describeErrors(m);
    const turns = Number.isFinite(m.num_turns) ? ` after ${m.num_turns} turns` : "";
    const numTurns = Number.isFinite(m.num_turns) ? m.num_turns : null;
    return {
      ok: false,
      kind: "limit",
      status: null,
      retryable: false,
      turns: numTurns,
      // `detail` here is generated by THIS repo (a subtype name and a turn count),
      // not quoted from upstream — but `describeErrors` folds in the SDK's own
      // `errors` array, which is upstream text, so it goes through the same gate.
      ...safeFailure({
        kind: "limit",
        status: null,
        detail: `${reason}${turns}${extra ? ` — ${extra}` : ""}`,
        turns: numTurns,
        subtype: m.subtype ?? m.terminal_reason ?? null,
      }),
    };
  }
  if (isApiError) {
    const raw = typeof m.result === "string" && m.result ? m.result : "";
    const status = m.api_error_status ?? null;
    // RETRYABILITY IS DECIDED FROM THE RAW TEXT, before `safeFailure` scrubs it.
    // Redaction must never be able to change a classification: a session limit
    // whose prose got masked would come back as retryable and burn the pool
    // retrying a window that cannot reopen inside this run.
    const retryable = !SESSION_LIMIT_RE.test(raw) && isRetryableStatus(status);
    return { ok: false, kind: "api-error", status, retryable, ...safeFailure({ kind: "api-error", status, detail: raw }) };
  }
  return {
    ok: false,
    kind: "no-output",
    status: null,
    retryable: false,
    ...safeFailure({ kind: "no-output", status: null, detail: `subtype=${m.subtype}` }),
  };
}

const defaultSleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * Run `fn`, retrying ONLY on errors flagged `err.retryable === true` (see
 * classifyResult), with exponential backoff + jitter. A non-retryable error
 * (quota/session-limit, or a genuine no-output) throws immediately — no wasted
 * retries on a limit that can't clear in-run. `sleep` is injectable for tests.
 */
export async function withRetry(fn, { retries = 2, baseMs = 2000, sleep = defaultSleep, jitter = () => 0 } = {}) {
  let last;
  for (let attempt = 0; attempt <= retries; attempt++) {
    try {
      return await fn();
    } catch (err) {
      last = err;
      if (!err || err.retryable !== true || attempt === retries) throw err;
      await sleep(baseMs * 2 ** attempt + jitter());
    }
  }
  throw last;
}

/**
 * Assemble the SDK `options` for one session. Pure and exported so the session's
 * security- and cost-critical shape is observable by a test WITHOUT opening a
 * session or having the SDK installed — `askStructured` passes the returned
 * object straight through, so what a test asserts here is what the session gets.
 *
 * `allowedTools` is validated here, which is also what keeps the grant check
 * ahead of the lazy SDK import at the one call site below.
 */
export function buildSessionOptions({ systemPrompt, model, repo, schema, maxTurns, allowedTools, mcpServers, effort, authToken }) {
  const sessionTools = assertAllowedTools(allowedTools);
  // Validated here, not at the spread below, so a bad value fails before the
  // session is opened — same ordering as the tool grant.
  const sessionEffort = assertEffort(effort);

  // `tools` restricts BUILT-IN tools only ("the base set of available built-in
  // tools", sdk.d.ts:1395). MCP tools do not come from there — they come from
  // `mcpServers` — so passing an `mcp__…` name in `tools` would at best be
  // ignored and at worst drop the built-in restriction. Split, deliberately.
  const builtinTools = sessionTools.filter((t) => !isMcpTool(t));
  const mcpTools = sessionTools.filter((t) => isMcpTool(t));

  // A grant and its server must arrive together. Either half alone is a silent
  // bug: a granted tool with no server is invisible to the model (a session that
  // burns quota and cannot probe), and a server with no grant leaves the tool
  // present but never pre-approved, so every call is denied by `dontAsk` with no
  // explanation the model can act on. Fail loudly at the call site instead.
  const hasServers = Boolean(mcpServers) && Object.keys(mcpServers).length > 0;
  if (mcpTools.length > 0 && !hasServers) {
    throw new Error(
      `buildSessionOptions: granted ${mcpTools.join(", ")} but passed no \`mcpServers\`; ` +
        "the tool would not exist in the session.",
    );
  }
  if (hasServers && mcpTools.length === 0) {
    throw new Error(
      "buildSessionOptions: passed `mcpServers` but granted no MCP tool; every call would " +
        "be denied by `permissionMode: 'dontAsk'`.",
    );
  }
  if (builtinTools.length === 0) {
    throw new Error(
      "buildSessionOptions: the grant must include at least one built-in read tool " +
        `(${PERMITTED_TOOLS.join(", ")}); an agent that can act but not read cannot cite evidence.`,
    );
  }

  return {
    // Forwarded VERBATIM, and the SDK accepts `string | string[]`. The array form
    // is load-bearing for COST, not just style: review-panel.mjs puts the shared
    // diff in a block before SYSTEM_PROMPT_DYNAMIC_BOUNDARY so a lens's later
    // samples re-read it at ~0.1x instead of re-sending it at full price.
    // Joining or stringifying this would leave every prompt working, every test
    // green, and quietly restore the panel's old bill — hence the pass-through
    // test rather than trust.
    systemPrompt,
    model,
    cwd: repo,
    // Only set when the caller asks for a ceiling; omitting it takes the SDK
    // default. `maxTurns` exists in the pinned SDK (0.3.217), on the same
    // Options type as allowedTools/outputFormat/permissionMode.
    ...(Number.isFinite(maxTurns) ? { maxTurns } : {}),
    // Same shape as `maxTurns`: present only when the caller asked for one, so
    // an unset effort takes the SDK default (`high`) rather than being pinned
    // here. `effort` is a session option, NOT prompt text — it never reaches
    // `systemPrompt`, so it cannot fragment the cross-lens cache prefix that
    // review-panel.mjs's warm-up depends on.
    ...(sessionEffort === undefined ? {} : { effort: sessionEffort }),
    // `tools` and `allowedTools` are DIFFERENT levers and both are needed.
    // Per the pinned SDK's own docs (sdk.d.ts:1341-1348, :1395-1404):
    //   tools        = "the base set of available built-in tools" — the actual
    //                  RESTRICTION. Anything omitted is not in the model's
    //                  context at all, so it cannot be called or injected into.
    //   allowedTools = "auto-allowed without prompting" — a PRE-APPROVAL list,
    //                  not a restriction. On its own it would leave Bash/Write/
    //                  WebFetch/Agent present and merely unapproved.
    // Setting only `allowedTools` (as this code did before) left the read-only
    // claim resting entirely on `permissionMode: 'dontAsk'`. That does deny
    // un-pre-approved calls, so it was not exploitable by itself — but it made
    // the guarantee a property of one enum value rather than of the tool set,
    // and it left every dangerous tool visible to a model reading an untrusted
    // diff. `tools` closes that: same list, so the two can never disagree.
    tools: builtinTools,
    // Pre-approve the built-ins AND any in-process MCP tool. This is the one
    // place the two lists legitimately differ: the MCP tool must be approved
    // here to be callable, but must NOT appear in `tools`, which is built-ins
    // only. Anything absent from both stays denied by `dontAsk`.
    allowedTools: sessionTools,
    // Only attach the server(s) when one was actually wired — `askStructured`
    // keys its MCP timeout backstop off the presence of this field.
    ...(hasServers ? { mcpServers } : {}),
    permissionMode: "dontAsk", // deny anything not pre-approved, no prompts
    // SECURITY: do NOT load project settings/hooks/agents from cwd — cwd may be
    // an untrusted branch checkout, and a branch-supplied .claude hook would be
    // a shell command the SDK could execute. `settingSources: []` disables that
    // (the review workflow also strips the branch's `.claude/` as
    // belt-and-suspenders).
    // settingSources exists in the pinned SDK (0.3.217); [] loads no project config.
    settingSources: [],
    // Pin THIS session to one pooled credential. Per-session rather than a
    // `process.env` mutation because the panel runs lenses and samples
    // concurrently: a global swap during a failover would hand a half-changed
    // environment to sessions already in flight. Omitted entirely when there is
    // no pool, so an unconfigured environment keeps today's plain inheritance.
    ...(authToken ? { env: credentialEnv(authToken) } : {}),
    outputFormat: { type: "json_schema", schema },
  };
}

/**
 * The environment for a session's subprocess: the parent's, minus the rest of
 * the pool, plus the one credential this session was given.
 *
 * Two things it has to get right, and both fail quietly:
 *
 *   1. The `process.env` spread is load-bearing. `Options.env` REPLACES the
 *      subprocess environment rather than merging it (sdk.d.ts:1408, pinned
 *      0.3.217), so dropping the spread strips PATH/HOME and the CLI never
 *      starts.
 *   2. The pool variables are then DELETED. This process needs all of them —
 *      it fails over inside the round — but the child does not: it holds one
 *      credential and runs with `cwd` set to the untrusted branch checkout. A
 *      plain spread would hand that child all nine, which is the blast radius
 *      `docs/design/agent-pipeline/harness-engineering.md` records as the pool's residual risk;
 *      this is the half of it that costs nothing to close.
 *
 * One builder, used by the first attempt and by every failover, so the two can
 * never disagree about what a session's environment contains.
 */
export function credentialEnv(authToken) {
  const env = { ...process.env };
  for (const name of poolEnvNames()) delete env[name];
  return { ...env, CLAUDE_CODE_OAUTH_TOKEN: authToken };
}

/**
 * Is this a CLOSED USAGE WINDOW on the account, rather than any other failure?
 *
 * One of the two errors where switching credentials is the fix; see
 * `isCredentialRejected` for the other, and `nextCredential` for why the split
 * matters. Still deliberately narrow: our own turn ceilings are also
 * non-retryable, and failing over on those would burn the pool on a problem no
 * other token can solve.
 */
export function isAccountLimit(err) {
  if (!err || err.kind !== "api-error") return false;
  // `code` FIRST. It is decided by `classifyInfraError` from the raw upstream text
  // before redaction, so it is the authoritative signal and cannot be perturbed by
  // the filter. The prose test is the fallback for callers that construct a bare
  // `{ kind, detail }` — several tests and `review-panel.mjs`'s synthesised errors
  // do — and for any failure that never passed through `classifyResult`.
  if (err.code) return err.code === "USAGE_LIMIT";
  return SESSION_LIMIT_RE.test(String(err.detail ?? ""));
}

/**
 * Was THIS credential refused, rather than the request being wrong?
 *
 * A 401/403 is a fact about one secret, and in a pool of independent accounts
 * the other members are unaffected — so it is a failover, not a stop.
 *
 * This reverses an earlier rule, and the reasoning it reverses was sound for the
 * shape it was written against. When there was one credential, a 401 could only
 * mean "the secret is wrong", failing over could only re-find the same wrong
 * secret, and burning the pool to learn that was pure waste. A pool of separate
 * accounts breaks that premise: one slot expiring leaves the rest valid, and
 * refusing to move made a per-slot fault behave like a total outage. Measured —
 * one of four pool secrets expired, `selectStartIndex` handed roughly a quarter
 * of runs to it, and each of those runs died on the first call with three good
 * credentials in the same process.
 *
 * The old rule's fear is real but cheap: if EVERY credential is genuinely bad,
 * this walks the whole pool before giving up. That costs one rejected round trip
 * per slot — seconds, since a refusal returns immediately — and the pool then
 * reports that every credential was rejected, which is a better diagnosis than
 * the first slot's error stated as though it were the pool's.
 *
 * Keyed on `code` first for the same reason as `isAccountLimit`: it is decided
 * from the raw text before redaction. The status fallback covers callers that
 * construct a bare `{ kind, status }` without going through `classifyResult`.
 */
export function isCredentialRejected(err) {
  if (!err || err.kind !== "api-error") return false;
  if (err.code) return err.code === "AUTH_REJECTED";
  const status = Number(err.status);
  return status === 401 || status === 403;
}

/**
 * The failure for "the pool had credentials, and every one of them is retired".
 *
 * One builder, because the condition is reachable from TWO places and they must
 * not disagree: before the first session opens (a sibling in this process
 * drained the pool), and inside the failover loop when the last live credential
 * is retired. The loop used to rethrow the last slot's own error there, so the
 * SAME outcome was reported as "credentials rejected" or "usage limit" — the
 * first thing an operator reads would name one slot's problem as though it were
 * the pool's, and which of the two messages you got depended on call ordering.
 *
 * Exported so the shape is testable without opening a session: this suite
 * deliberately never lets `askStructured` past its validation gate, because that
 * would burn a paid session from a unit test.
 *
 * The wording stays on WHAT HAPPENED (retired) rather than WHY. A slot is
 * retired for a closed window OR a refused credential, and the two remedies are
 * opposite — wait for a reset, versus rotate a secret. Naming one of them here
 * would send an operator the wrong way half the time. Nothing is lost by
 * omitting it: every failing session is already pushed to `sessionLog` with its
 * own code before it throws, and `Agent SDK Auth Smoke Test` answers "which
 * secret" definitively, per name.
 */
export function poolExhaustedError({ label, pool }) {
  const err = new Error(
    `${label}: every credential in the pool (${pool.size}) was retired — nothing left to fail over to`,
  );
  err.kind = "pool-exhausted";
  err.retryable = false;
  // The same three publishable fields every `classifyResult` failure carries. This
  // error is built here rather than there, so without this it was the ONE failure
  // reaching a renderer with no `code` and no `reason` — and a renderer with no
  // `reason` falls back to re-deriving one from `detail`, which for a drained pool
  // is empty. `detail` is empty by construction and not merely redacted: nothing
  // upstream is quoted here, only a slot count this repo produced itself.
  Object.assign(err, safeFailure({ kind: "pool-exhausted", status: null, detail: "" }));
  return err;
}

/**
 * Did this failure mean the session NEVER RAN, as opposed to running and producing
 * nothing usable?
 *
 * The distinction decides whether a failure is INFRASTRUCTURE, and it is not the
 * same question as "did something go wrong". A run ceiling (`limit`) and an empty
 * verdict (`no-output`) both mean the model ran, spent turns and returned something
 * unusable — those stay ordinary fail-closed blockers, because there is a review to
 * re-attempt and a verifier can reason about it. `api-error` and `pool-exhausted`
 * mean no reviewer ever opened, so there is nothing to re-check and nothing a
 * verifier could refute.
 *
 * `pool-exhausted` belongs here and its absence was a real defect. It is the SAME
 * underlying failure as an `api-error` — a closed usage window or a refused
 * credential — wearing a different `kind` only because a pool was configured and
 * failover ran out of slots. Keying infrastructure on `api-error` alone therefore
 * classified one outage two different ways depending on how many credentials the
 * run happened to have, and the multi-credential answer was the wrong one: the
 * synthesised record lost its `infra` flag and was carried into the next round as a
 * blocking code finding.
 *
 * Exported so the panel and its tests agree on one predicate rather than each
 * spelling out a list of kinds that has already grown once.
 */
export function sessionNeverRan(err) {
  const kind = err && typeof err.kind === "string" ? err.kind : "";
  return kind === "api-error" || kind === "pool-exhausted";
}

/**
 * The credential pool for this process, built once.
 *
 * Module-scoped because exhaustion is a fact about the ACCOUNT, not about one
 * call: the six lenses of a panel round should make that discovery once between
 * them rather than six times each.
 */
let poolInstance = null;
export function tokenPool() {
  if (!poolInstance) poolInstance = createTokenPool();
  return poolInstance;
}

/** Test seam: drop the memoised pool (and optionally install a fake). */
export function __setTokenPool(pool = null) {
  poolInstance = pool;
}

/**
 * The credential to retry this failure on, or `null` to give up and rethrow.
 *
 * Pure and exported so the failover decision is testable WITHOUT the SDK on disk
 * — the agent test lane runs without it, which is the same reason
 * `buildSessionOptions` is separated out.
 *
 * `authToken` is the credential the failed session actually used. Passing it is
 * what makes a concurrent report idempotent: the panel's lenses hit one closed
 * window within milliseconds of each other, and without it the second report
 * would retire the healthy token the first one just moved to — a burst of
 * concurrency would drain the pool in a single round.
 */
export function nextCredential({ pool, err, authToken }) {
  if (!isAccountLimit(err) && !isCredentialRejected(err)) return null;
  const next = pool.advance(err.detail, authToken);
  return !next || next === authToken ? null : next;
}

/**
 * What the failover loop does with one failed session: `{ next }` to retry on
 * another credential, or `{ error }` to give up and throw.
 *
 * Pure and exported for the same reason `nextCredential` is — the loop that uses
 * it can only be entered by opening a real session, which this suite refuses to
 * do, so the decision has to be testable on its own or it is not tested at all.
 *
 * The `error` branch is where the two shapes of "give up" are separated. A
 * failure no credential can fix belongs to the caller and is rethrown as-is. A
 * failure that retired the LAST live slot belongs to the pool: rethrowing the
 * slot's own error there reported one account's problem as the pool's verdict,
 * and made the message depend on whether this call or a sibling drained it.
 */
export function failoverStep({ pool, err, authToken, label }) {
  const next = nextCredential({ pool, err, authToken });
  if (next !== null) return { next };
  return { error: pool.isExhausted() ? poolExhaustedError({ label, pool }) : err };
}

/**
 * Open one structured-output SDK session and return the validated object.
 *
 * `allowedTools` is required and passes through `assertAllowedTools`. `label`
 * only shapes the thrown error's wording ("<label> query API error …") so a
 * caller's logs read naturally; it has no behavioral effect.
 *
 * Throws on every non-success path, with `kind`/`status`/`detail`/`retryable`
 * copied onto the error from `classifyResult` so `withRetry` can decide whether
 * another attempt could possibly help.
 */
export async function askStructured({
  systemPrompt,
  prompt,
  model,
  repo,
  schema,
  sessionLog,
  maxTurns,
  allowedTools,
  mcpServers,
  effort,
  label = "agent",
  // Optional attribution ({ lens, role }) stamped onto this call's logged result
  // message, so a consumer summing `sessionLog` can split cost by lens and by
  // detection-vs-verifier instead of seeing one anonymous total. No behavioral
  // effect; omitted for callers that don't attribute.
  logMeta,
}) {
  // One credential for this session, and — barring exhaustion — for every other
  // session in this process. See token-pool.mjs: distribution is per JOB because
  // prompt caches are per account, and rotating per call would make the panel pay
  // its shared-prefix warm-up once per token instead of once per round.
  const pool = tokenPool();
  // A drained pool must FAIL, not fall through to ambient resolution. `current()`
  // is null for both "no pool configured" (fall through — correct) and "every
  // credential retired", and in these workflows the ambient credential is slot
  // zero: one the pool has already retired. Falling through would spend a live
  // call to fail the same way, or report "not logged in" for a pool that is
  // merely out of quota. Non-retryable so the caller's `withRetry` does not back
  // off around a condition that cannot clear in-run.
  //
  // The wording stays on WHAT HAPPENED (retired) rather than WHY. A slot is now
  // retired for a closed window OR a refused credential, and the two remedies are
  // opposite — wait for a reset, versus rotate a secret. Naming one of them here
  // would send an operator the wrong way half the time; the per-slot reason is in
  // the run log above, and `Agent SDK Auth Smoke Test` answers it definitively.
  if (pool.isExhausted()) throw poolExhaustedError({ label, pool });
  let authToken = pool.current();

  // Built BEFORE the dynamic import, because it validates the tool grant: a bad
  // grant must fail without opening a session (ask.test.mjs asserts this ordering
  // by observing that an invalid grant throws the validation error even when the
  // SDK cannot be resolved). It also splits any MCP grant from the built-ins and
  // wires `mcpServers`, so the whole session shape stays observable in one place.
  let options = buildSessionOptions({ systemPrompt, model, repo, schema, maxTurns, allowedTools, mcpServers, effort, authToken });

  for (;;) {
    try {
      return await runSession({ options, prompt, sessionLog, logMeta, label });
    } catch (err) {
      // Null covers both "no other credential could help" and "pool dry", and
      // rethrowing is right for both. Every other failure belongs to the caller:
      // `withRetry` still owns the transient ones, and a non-retryable error no
      // credential can fix (our own turn ceiling, a malformed request) must not
      // burn the pool looking for one that can.
      const step = failoverStep({ pool, err, authToken, label });
      if (step.error) throw step.error;
      authToken = step.next;
      options = { ...options, env: credentialEnv(authToken) };
    }
  }
}

/**
 * Open ONE session and return the validated object — the body of askStructured's
 * failover loop, split out so the retry is readable as a retry.
 *
 * Throws on every non-success path, with `kind`/`status`/`detail`/`retryable`
 * copied onto the error from `classifyResult`.
 */
async function runSession({ options, prompt, sessionLog, logMeta, label }) {
  // NOTE: `MCP_TOOL_TIMEOUT` is deliberately NOT set here. It has to be in place
  // before the SDK is imported, and by this line it already has been — the server
  // passed in `mcpServers` was built by `createProbeServer` (hunt-tool.mjs), which
  // imports the SDK itself. That is where the backstop is set.

  const sdk = await import("@anthropic-ai/claude-agent-sdk");
  // The one place the real SDK is in hand, so the one place the cache-boundary
  // mirror can be checked against it. Cheap, and it converts a silent 10x cost
  // regression into a loud failure.
  assertBoundaryMatchesSdk(sdk);
  const { query } = sdk;
  for await (const message of query({ prompt, options })) {
    if (message.type === "result") {
      // Record cost/turns/tokens regardless of success — the call still burned
      // compute even when it didn't produce usable structured output. This is
      // the ONLY place an SDK call's result is observable at all; callers
      // discard everything else, so record before the throw below.
      // Tag the recorded entry with its attribution when given (a shallow copy so
      // the SDK's message is not mutated). metrics.mjs reads `.attribution`; every
      // other field is preserved, so the log stays claude-execution-output shaped.
      // `result` is raw upstream text, and this log is written to
      // review-execution.json / hunt-execution.json and uploaded as a workflow
      // ARTIFACT — downloadable by anyone with repo read access, which on a public
      // repo is everyone. So it is scrubbed on the way in, at the one producer all
      // three logs share. Nothing reads it back: metrics.mjs consumes usage, turns,
      // cost and session_id only. `classifyResult` below still sees the untouched
      // `message`, so classification is unaffected.
      if (sessionLog) {
        const safe = typeof message.result === "string" ? { ...message, result: redactSecrets(message.result) } : message;
        sessionLog.push(logMeta ? { ...safe, attribution: logMeta } : safe);
      }
      const c = classifyResult(message);
      if (c.ok) return c.output;
      // `c.reason`, NOT `c.detail`. This message is not log-only: `review-panel.mjs`
      // publishes `err.message` verbatim on its non-infra path
      // ("Reviewer did not produce a valid verdict: …"), so an upstream string
      // interpolated here reaches a public pull request exactly the way the
      // credential did. `reason` is assembled from the closed vocabulary, and the
      // codes below are more specific than the prose they replace — a turn ceiling
      // now says RUN_LIMIT_TURNS rather than being inferred from a subtype name.
      const err = new Error(
        c.kind === "api-error"
          ? `${label} query API error: ${c.reason}`
          : c.kind === "limit"
            ? // Names the actual ceiling and the turns spent, so the log says
              // "hit its turn ceiling" rather than the old, misleading
              // "API error: unknown" — which sent us looking at the network.
              `${label} query hit a run limit: ${c.reason} (raise the ceiling or simplify the task; retrying will not help)`
            : // `label` here too: with two consumers sharing the wrapper, an
              // unattributed "structured output not produced" is exactly the
              // ambiguity `label` exists to remove.
              `${label} query: structured output not produced (${c.reason})`,
      );
      err.kind = c.kind;
      err.status = c.status;
      err.code = c.code;
      err.reason = c.reason;
      // Redacted, and for the run log only — no renderer may publish this.
      err.detail = c.detail;
      err.retryable = c.retryable;
      throw err;
    }
  }
  throw new Error("query ended without a result message");
}
