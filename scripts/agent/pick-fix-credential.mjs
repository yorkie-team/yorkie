// Which credential should the FIXER use, and is there one at all?
//
// WHY THIS EXISTS. `token-pool.mjs` is a Node module, so only the SDK-driven steps
// can consult it. Every `claude-code-action` step in the pipeline — the `fix` job
// here, plus agent-fix, agent-implement, agent-iterate-ci, agent-review-reply and
// agent-summarize — instead takes one hardcoded `secrets.CLAUDE_CODE_OAUTH_TOKEN`.
// That variable is SLOT ZERO of the pool (`token-pool.mjs::TOKEN_ENV`, "the
// unsuffixed variable is slot zero"), which means the fixer runs on the credential
// the panel is most likely to have just burned. `isExhausted`'s docblock names the
// hazard exactly: falling back to the ambient token re-uses "a credential the pool
// has already retired".
//
// Measured on #876: the panel drained both configured accounts, the fix job
// dispatched round 1 of 3, and `claude-code-action` died 1.6s after init with
// `is_error:true`, turns=1, tokens=0. The branch head never moved, the loop paged
// "the fixer agent failed", and the PR had spent a fix round on a session that never
// ran. #873 is the same root cause one step earlier: a lens reported "every
// credential in the pool (2) was retired — nothing left to fail over to".
//
// So this reads the pool state the panel recorded and answers two questions:
//   slot=<suffix>   which secret to hand `claude-code-action` ("" = the unsuffixed one)
//   available=      true/false — is there any live credential to spend a round on
//
// FAIL DIRECTION — and it is deliberately NOT uniform, because the two failures are
// not alike:
//
//   * Cannot READ the pool state (no artifact, malformed JSON, panel crashed before
//     writing it) → `available=true`, `slot=` empty. We do not know that the fixer
//     would fail, and skipping it would lose a fix that might well have worked. This
//     is exactly today's behaviour, so an un-wired or half-broken hand-off costs
//     nothing that is not already lost.
//   * KNOW every slot is retired → `available=false`. Here we do know. Dispatching
//     would consume one of three fix rounds on a session that cannot start, which is
//     strictly worse than pausing and saying so.
//
// THE PROBE (`--probe`). The pool state knows only what the PANEL's sessions
// saw. On #1426 it named CLAUDE_CODE_OAUTH_TOKEN ("live-slot", "0 retired") and
// the fixer died 0.5 s after init at $0, having already been charged a round. A
// slot the panel never touched, or one whose window closed after the panel
// finished, reads as live. So with `--probe` each candidate gets a one-word query
// before the round is recorded, and the first that answers is the one handed
// out. Same two fail directions as above: every candidate REFUSED (quota or
// auth) is a known-dead pool and `available=false`; anything inconclusive — an
// unclassified error, a timeout, an SDK that will not load — proceeds.
//
// It runs before the branch checkout, so the pool secrets it needs are never in
// a process that has branch code on disk.
//
// Usage:
//   node pick-fix-credential.mjs [--state <dir-or-file>] [--probe] [--model <id>]
// Always exits 0. Writes `slot=` / `available=` / `reason=` to $GITHUB_OUTPUT when
// set, and always logs a human line.

import { readFileSync, existsSync, statSync, appendFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { MAX_SLOTS, TOKEN_ENV, slotSuffix, readPoolSlots } from "./token-pool.mjs";
import { SESSION_LIMIT_RE } from "./redact.mjs";
import { classifyFixResult } from "./metrics.mjs";

// Re-exported, not redefined: `token-pool.mjs` owns the slot naming, and a second
// copy of this mapping is exactly the drift its docblock warns about.
export { slotSuffix };

const STATE_FILE = "review-pool-state.json";
// Must match what review-panel.mjs stamps. Read as a gate, not decoration — see the
// version check in `chooseCredential`.
const POOL_STATE_VERSION = 1;

/**
 * Decide from a parsed pool-state object. Pure, so the fail directions above are
 * testable without artifacts.
 *
 * `live` is filtered through `slotSuffix`, so an unrecognised name is dropped rather
 * than forwarded — an artifact naming `GITHUB_TOKEN` cannot make the fixer read it.
 */
export function chooseCredential(state) {
  const proceed = (reason) => ({ slot: "", available: true, reason });
  if (!state || typeof state !== "object") return proceed("no-pool-state");

  // VERSION FIRST. `v` is the only thing that says these field names mean what this
  // code thinks they mean, so an unknown version makes every other field
  // uninterpretable — including `live: []`, which would otherwise read as a drained
  // pool. A future panel that bumps the format therefore degrades to today's
  // behaviour instead of refusing every fixer until this file catches up.
  if (state.v !== POOL_STATE_VERSION) return proceed("pool-state-version");

  const live = Array.isArray(state.live) ? state.live : null;
  if (!live) return proceed("pool-state-unreadable");

  const usable = live.map(slotSuffix).filter((s) => s !== null);
  // First live slot in slot order. Not the panel's current slot and not a random
  // one: order is stable, so two jobs reading the same state agree, and a human
  // reading the log can predict what it picked.
  if (usable.length > 0) return { slot: usable[0], available: true, reason: "live-slot" };

  // ── No usable live slot. Everything below decides whether that is EVIDENCE of a
  // drained pool or merely an artifact we cannot interpret.
  //
  // THE ASYMMETRY THAT DRIVES THIS: refusing costs a stalled PR and a page, while
  // proceeding costs at most one wasted fix round. So `available: false` is returned
  // ONLY for an artifact that is internally consistent AND says the pool is spent.
  // Anything self-contradictory is unreadable, and unreadable proceeds.

  // `size` must be a real, bounded slot count before any conclusion rests on it.
  // Without this, `{live: [], size: "banana"}` reached the refusal below: `Number()`
  // gave NaN, NaN !== 0, and a malformed artifact stalled the PR.
  const size = state.size;
  if (!Number.isInteger(size) || size < 0 || size > MAX_SLOTS + 1) return proceed("pool-size-unreadable");

  // `size: 0` is a repo with no pool configured, not a spent one. It runs on the
  // ambient credential and always has.
  if (size === 0) return proceed("pool-unconfigured");

  // A NON-empty live list none of whose names we recognise is a malformed or tampered
  // artifact, not a drained pool — nothing here says a credential is unavailable,
  // only that we cannot tell which.
  if (live.length > 0) return proceed("unrecognised-slots");

  // `live` is empty and slots are configured. `retired` must corroborate it: every
  // entry a recognised slot, no duplicates, and exactly as many as the pool holds.
  // A pool of 2 that reports one retired slot and no live ones has lost a slot
  // somewhere, and guessing which way is worse than not guessing.
  const retired = Array.isArray(state.retired) ? state.retired.map(slotSuffix) : null;
  if (!retired || retired.some((x) => x === null)) return proceed("retired-unreadable");
  if (new Set(retired).size !== retired.length) return proceed("retired-duplicated");
  if (retired.length !== size) return proceed("retired-count-mismatch");

  return { slot: "", available: false, reason: "all-slots-retired" };
}

/**
 * Which slots to probe, in order: the panel's live slots that have a secret
 * behind them, or — when the state cannot be read — every configured slot. A
 * KNOWN-drained pool has none, and `chooseCredential`'s refusal stands.
 */
export function candidateNames(state, configuredNames) {
  const configured = (Array.isArray(configuredNames) ? configuredNames : []).filter((n) => slotSuffix(n) !== null);
  const decided = chooseCredential(state);
  if (decided.available === false) return [];
  const live = state && state.v === POOL_STATE_VERSION && Array.isArray(state.live) ? new Set(state.live) : null;
  if (!live || live.size === 0) return configured;
  return configured.filter((n) => live.has(n));
}

// After wafflebase's auth-smoke.mjs, NARROWED. A "refused" answer here pauses
// the PR behind the paged latch until a human reruns it, so only a refusal that
// STAYS refused counts: a closed usage window (until it resets) or a rejected
// credential. A plain 429, "rate limit" or "overloaded" is transient — on an
// API-wide blip every slot would read as dead and the PR would latch for
// nothing — so it is `unknown`, which proceeds. The same line the panel's own
// failover draws: `isAccountLimit` keys on USAGE_LIMIT, not on RATE_LIMITED.
const QUOTA = [SESSION_LIMIT_RE];
const AUTH = [
  /\b401\b/, /\b403\b/, /unauthorized/i, /authentication[ _-]?(error|failed)/i,
  /invalid[ _-]?(api[ _-]?key|token|credential)/i, /expired[ _-]?(token|credential)/i,
  /not[ _-]?logged[ _-]?in/i, /please run \/login/i,
];

/** "quota" | "auth" | "unknown". Quota first: it often arrives worded as auth. */
export function classifyProbeFailure(message) {
  const text = String(message ?? "");
  if (QUOTA.some((re) => re.test(text))) return "quota";
  if (AUTH.some((re) => re.test(text))) return "auth";
  return "unknown";
}

/**
 * Probe `names` in order with `check(name)` → `{ ok }` or `{ ok: false, kind }`.
 * Returns the picker's answer, or `null` when there was nothing to probe (the
 * caller then falls back to `chooseCredential`). A throw is `unknown`.
 */
export async function probeCredentials({ names, check, log = console.log }) {
  const list = Array.isArray(names) ? names : [];
  if (list.length === 0) return null;
  let firstUnknown = null;
  for (const name of list) {
    let res;
    try {
      res = await check(name);
    } catch (err) {
      res = { ok: false, kind: "unknown", detail: err?.message };
    }
    if (res && res.ok) return { slot: slotSuffix(name), available: true, reason: "probed-live" };
    const kind = res && (res.kind === "quota" || res.kind === "auth") ? res.kind : "unknown";
    // The NAME only — never the token, never upstream text (a malformed secret
    // is quoted back verbatim by the HTTP client).
    log(`probe: ${name} → ${kind}`);
    if (kind === "unknown" && firstUnknown === null) firstUnknown = name;
  }
  if (firstUnknown !== null) return { slot: slotSuffix(firstUnknown), available: true, reason: "probe-inconclusive" };
  return { slot: "", available: false, reason: "probe-all-refused" };
}

/**
 * One credential, one word, no tools, a hard timeout.
 *
 * The child gets ONLY the token under test, PATH, and a throwaway HOME and
 * CLAUDE_CONFIG_DIR. Not `process.env`: this step holds every pool secret, and
 * the CLI it starts would otherwise inherit all nine. And not the real HOME: the
 * CLI writes state there, on the runner the fixer later gets a shell on.
 *
 * 30 s, serial. Nine dead slots cost at most 4.5 minutes of the fix job's
 * 55-minute wall; a healthy pool answers on the first.
 */
export function probeEnv(token, home) {
  return { PATH: process.env.PATH ?? "", HOME: home, CLAUDE_CONFIG_DIR: home, CLAUDE_CODE_OAUTH_TOKEN: token };
}

function sdkCheck(query, model, token, timeoutMs = 30_000) {
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), timeoutMs);
  const home = mkdtempSync(path.join(tmpdir(), "probe-"));
  return (async () => {
    let failure = "(no result message)";
    try {
      for await (const m of query({
        prompt: "Reply with exactly one word: pong",
        options: {
          model, allowedTools: [], permissionMode: "dontAsk", settingSources: [], maxTurns: 1,
          abortController: abort,
          // `env` REPLACES the child environment (see ask.mjs).
          env: probeEnv(token, home),
        },
      })) {
        if (m.type !== "result") continue;
        // The repo's one success rule for a session result (it also reads
        // `api_error_status` and `terminal_reason`), not a second copy of it.
        if (classifyFixResult(m)?.ok) return { ok: true };
        failure = `${m.subtype ?? ""} ${m.api_error_status ?? ""} ${m.result ?? ""}`;
      }
    } catch (err) {
      failure = abort.signal.aborted ? "timeout" : String(err?.message ?? err);
    } finally {
      clearTimeout(timer);
      rmSync(home, { recursive: true, force: true });
    }
    return { ok: false, kind: classifyProbeFailure(failure) };
  })();
}

/** Read the state file from a directory or an explicit path. Never throws. */
export function readPoolState(target) {
  try {
    if (!target) return null;
    let file = target;
    if (existsSync(target) && statSync(target).isDirectory()) file = path.join(target, STATE_FILE);
    if (!existsSync(file)) return null;
    return JSON.parse(readFileSync(file, "utf8"));
  } catch {
    return null;
  }
}

/**
 * How many slots are configured out of the ceiling — the capacity line an operator
 * can act on. A page that says "a lens failed" is not actionable; one that says
 * "2 of 9 credential slots are configured" is.
 */
export function capacityNote(state) {
  const size = Number(state?.size);
  if (!Number.isInteger(size) || size < 0) return "";
  const max = Number.isInteger(state?.maxSlots) ? state.maxSlots : MAX_SLOTS;
  const total = max + 1; // slot zero plus the suffixed ones
  const retired = Array.isArray(state?.retired) ? state.retired.length : 0;
  const detail = `${size} of ${total} credential slot(s) configured, ${retired} retired this round`;
  if (size === 0) return `${detail} — the pool is OFF; the pipeline runs on the ambient credential alone`;
  if (size <= 2) return `${detail} — register more \`${TOKEN_ENV}_N\` secrets; a round spends 6 lenses plus a verifier per finding`;
  return detail;
}

async function main(argv) {
  const at = argv.indexOf("--state");
  const target = at >= 0 ? argv[at + 1] : "/tmp/review-panel-execution";
  const state = readPoolState(target);
  let { slot, available, reason } = chooseCredential(state);

  if (argv.includes("--probe") && available) {
    const mi = argv.indexOf("--model");
    const model = mi >= 0 && argv[mi + 1] ? argv[mi + 1] : "claude-opus-5";
    const slots = readPoolSlots(process.env);
    const tokenOf = new Map(slots.map((s) => [s.name, s.token]));
    let query = null;
    try {
      ({ query } = await import("@anthropic-ai/claude-agent-sdk"));
    } catch (err) {
      console.log(`probe: the Agent SDK did not load (${err.message}); using the pool state alone`);
    }
    const probed = query
      ? await probeCredentials({
          names: candidateNames(state, slots.map((s) => s.name)),
          check: (name) => sdkCheck(query, model, tokenOf.get(name)),
        })
      : null;
    if (probed) ({ slot, available, reason } = probed);
  }

  const which = slot === "" ? TOKEN_ENV : `${TOKEN_ENV}_${slot}`;
  console.log(
    available
      ? `fixer credential: ${which} (reason: ${reason})`
      : `fixer credential: NONE LIVE (reason: ${reason}) — not dispatching, so no fix round is spent`,
  );
  const note = capacityNote(state);
  if (note) console.log(`credential capacity: ${note}`);

  const out = process.env.GITHUB_OUTPUT;
  if (out) {
    appendFileSync(out, `slot=${slot}\navailable=${available}\nreason=${reason}\n`);
    appendFileSync(out, `capacity=${note}\n`);
  }
}

// Same guard, same two reasons, as `pick-credential.mjs`: this runs from the
// parentless `$RUNNER_TEMP/agent-tools` copy, so the shared `isDirectRun` is
// out of reach; and the `file://` template this replaces compared a
// percent-encoded URL against a raw path.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  // Fail OPEN on a crash, like every other doubt here: no `available=` written
  // reads as "proceed" in the workflow.
  main(process.argv.slice(2)).catch((err) => console.log(`pick-fix-credential: crashed (${err.message}); proceeding as before`));
}
