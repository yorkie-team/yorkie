// The publication boundary: what may leave this process and land on a pull request.
//
// WHY THIS EXISTS. A `CLAUDE_CODE_OAUTH_TOKEN` was rotated with a stray space in
// it. A space makes an `Authorization` header value structurally invalid, so the
// HTTP client rejected it BEFORE SENDING — and that class of error quotes the
// offending value back at you: `Header 'Authorization' has invalid value: <the
// token>`. The SDK surfaced that string as its `result` text, `classifyResult`
// carried it as `detail`, and the panel interpolated `detail` verbatim into a lens
// summary that fans out to a check-run body, a PR comment and the job summary. The
// credential was published to a public repository.
//
// Note the shape of it, because it inverts the usual intuition: a merely WRONG
// token produces a clean 401 with no echo. The TYPO is what converted an auth
// failure into a disclosure.
//
// Two things did not save us, and both are routinely assumed to:
//
//   Storing the value in GitHub Secrets. "In secrets" means not committed and
//   scrubbed from logs. It never meant the running process cannot read it — the
//   panel MUST put the token in an Authorization header to authenticate, so it
//   necessarily holds it in memory. Every process that authenticates does.
//
//   Log masking. Masking rewrites the run's console output. A PR comment is a
//   request body this pipeline asks GitHub to publish; there is no log for a
//   scrubber to sit in front of. Masking is also exact-substring matching against
//   the registered value, and GitHub splits registered secrets on whitespace — so
//   a token stored WITH A SPACE registers as fragments, which is close to the
//   worst case for the masker even in the logs.
//
// So it has to happen here, in-process, before any text reaches a renderer.
//
// TWO INDEPENDENT GUARANTEES, and the independence is the design.
//
//   1. `classifyInfraError` / `renderInfraError` — no raw upstream text is ever
//      published. A failure is mapped onto a CLOSED VOCABULARY of codes, and the
//      published string is BUILT from that vocabulary plus typed primitives. There
//      is no path from upstream prose to a comment body, whatever it contains.
//
//   2. `redactSecrets` — an unconditional pattern filter at the publication
//      boundary that masks anything RESEMBLING a credential.
//
// Guarantee 1 is the stronger one: a filter is only as good as its pattern list,
// and this incident happened precisely because a value arrived in a shape nobody
// had enumerated. Guarantee 2 exists because Guarantee 1 protects the surfaces we
// remembered to route through it, and defence in depth is what covers the one we
// forgot.
//
// WHAT THIS MODULE DELIBERATELY DOES NOT DO: depend on knowing which credentials
// exist. An earlier draft (agent-pipeline#2) keyed its defence on a frozen list of
// credential env var names. That list was correct when written and wrong two hours
// later, when #854 grew the pool from one token to nine. Enumerating credentials
// is a maintenance treadmill that fails silently and in the dangerous direction,
// so the env layer below is supplementary — every other layer works without
// knowing a single variable name.

import { poolEnvNames } from "./token-pool.mjs";

/**
 * Shortest run of characters we will treat as secret material.
 *
 * Guards the fragment and continuation rules. A fragment shorter than this is
 * likelier to be an ordinary word that happens to sit inside the value than
 * anything worth protecting, and redacting short strings mangles the very error
 * messages this pipeline exists to report.
 */
const MIN_SECRET_LEN = 8;

/**
 * Shortest unprefixed run the entropy rule will mask.
 *
 * Higher than MIN_SECRET_LEN because this rule fires with NO prefix to confirm it
 * is looking at a credential — only the character mix. Short mixed-class runs are
 * common in ordinary text (identifiers, short hashes, version strings), so the
 * threshold buys precision at the cost of missing a hypothetical sub-24-char
 * secret, which no provider in this pipeline issues.
 */
const MIN_ENTROPY_LEN = 24;

/**
 * Does this run of characters look like secret material rather than prose?
 *
 * Requires all three character classes. This is the discriminator that keeps the
 * entropy rule usable: a git SHA is lowercase hex and a UUID is lowercase hex with
 * dashes, so NEITHER has an uppercase character and neither is masked — which
 * matters because this pipeline prints commit SHAs and run ids constantly, and a
 * filter that ate them would be turned off within a day.
 *
 * English words fail on the digit requirement, which is what stops the
 * continuation rule below from swallowing the rest of a sentence.
 */
function looksLikeSecretRun(s) {
  return /[a-z]/.test(s) && /[A-Z]/.test(s) && /[0-9]/.test(s);
}

/**
 * Environment variables whose VALUE is a live credential in this pipeline.
 *
 * DERIVED, not written out. The pool's size lives in `token-pool.mjs::MAX_SLOTS`
 * and this list follows it, so growing the pool cannot leave credentials outside
 * the filter — the failure mode that made a verbatim port of the upstream fix
 * insufficient.
 *
 * Still an allowlist rather than a name pattern (`/TOKEN|KEY|SECRET/`): every
 * value collected here is masked out of published text, so a name that matched by
 * accident — `GITHUB_TOKEN_EXPIRY`, say — would start censoring ordinary numbers
 * out of error messages. Adding a name is cheap; a false positive is a debugging
 * mystery.
 */
export function credentialEnvNames() {
  return [...poolEnvNames(), "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"];
}

/**
 * The live credential values visible to this process, plus their whitespace
 * fragments.
 *
 * A SUPPLEMENTARY layer, and the only one that knows a variable name. It needs no
 * pattern to recognise a value, so it catches a credential shape nobody has
 * thought of — but only while the text still contains the value verbatim.
 *
 * The fragments are why it is worth keeping. A well-formed token is one opaque run
 * and the exact-value rule catches it; a MALFORMED one is quoted back after the
 * client has already split or trimmed it, so the text on the page never equals the
 * value in the environment. Splitting the env value the same way closes that gap.
 *
 * Sorted longest-first so the full value is replaced before any of its own
 * fragments can chew a hole in it and leave the remainder exposed.
 */
export function secretsFromEnv(env = process.env) {
  const out = new Set();
  for (const name of credentialEnvNames()) {
    const raw = env?.[name];
    if (typeof raw !== "string") continue;
    const value = raw.trim();
    if (value.length >= MIN_SECRET_LEN) out.add(value);
    if (!/\s/.test(value)) continue;
    for (const part of value.split(/\s+/)) {
      if (part.length >= MIN_SECRET_LEN) out.add(part);
    }
  }
  return [...out].sort((a, b) => b.length - a.length);
}

/**
 * Mask a prefixed credential and any malformed tail it carries.
 *
 * `Header 'Authorization' has invalid value: sk-ant-oat01-AbC dEf123456` must not
 * mask only up to the space. The continuation rule consumes following
 * whitespace-separated runs for as long as they still look like secret material,
 * then stops — so the token is masked whole and the sentence around it survives.
 */
function maskPrefixed(text, pattern, label) {
  return text.replace(pattern, (match) => {
    // Walk the match chunk by chunk tracking the OFFSET each one ends at, rather
    // than rejoining the pieces with a single space. Rejoining silently assumes
    // the separators were one character wide, so `sk-ant-x␣␣␣dEf12345` would slice
    // two characters short and re-emit the tail of the secret next to the label.
    const chunks = /\S+/g;
    let end = 0;
    let first = true;
    let m;
    while ((m = chunks.exec(match)) !== null) {
      if (first) {
        end = m.index + m[0].length;
        first = false;
        continue;
      }
      if (m[0].length < MIN_SECRET_LEN || !looksLikeSecretRun(m[0])) break;
      end = m.index + m[0].length;
    }
    // Anything past the secret material is prose, handed back untouched.
    return label + match.slice(end);
  });
}

/**
 * Remove credential material from `text`.
 *
 * FIVE LAYERS, ordered most-specific to most-general, and they fail differently on
 * purpose. Ordering is load-bearing: a general rule running first would claim a
 * value the specific rule labels better, and the `(?!<REDACTED)` guards stop a
 * later rule rewriting an earlier, more precise label into a vaguer one.
 */
export function redactSecrets(text, { extra = secretsFromEnv() } = {}) {
  let s = typeof text === "string" ? text : String(text ?? "");

  // LAYER 1 — live env values (supplementary; the only pool-aware layer).
  for (const value of extra) {
    if (typeof value === "string" && value.length >= MIN_SECRET_LEN) {
      s = s.split(value).join("<REDACTED>");
    }
  }

  // LAYER 2 — echo FRAMES, matched by the sentence around the secret rather than
  // by the secret's shape, and consuming to end of line because what follows the
  // colon is by definition unrecognisable. This is the rule that would have
  // stopped the incident on its own, and the only one that works against a
  // credential format that did not exist when this was written.
  s = s
    .replace(/(\bAuthorization\b['"]?[^\n:]*\bvalue:)\s*.*/gi, "$1 <REDACTED>")
    .replace(/(\b(?:x-api-key|api-key)\b['"]?[^\n:]*\bvalue:)\s*.*/gi, "$1 <REDACTED>");

  // LAYER 3 — known credential shapes, whitespace-tolerant (see maskPrefixed).
  s = maskPrefixed(s, /\bsk-ant-[A-Za-z0-9_-]+(?:\s+[A-Za-z0-9_-]{8,})*/g, "<REDACTED_ANTHROPIC_KEY>");
  s = maskPrefixed(s, /\bsk-(?!ant-)[A-Za-z0-9_-]{16,}(?:\s+[A-Za-z0-9_-]{8,})*/g, "<REDACTED_API_KEY>");
  s = maskPrefixed(
    s,
    /\b(?:gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,})(?:\s+[A-Za-z0-9_-]{8,})*/g,
    "<REDACTED_GITHUB_TOKEN>",
  );
  // wafflebase API keys — this pipeline reviews that repo, so its keys can appear
  // in a quoted upstream error here.
  s = maskPrefixed(s, /\bwfb_[A-Za-z0-9_-]+(?:\s+[A-Za-z0-9_-]{8,})*/g, "<REDACTED_API_KEY>");
  // JWTs before the Bearer rule, so a bearer-carried JWT is labelled as a JWT
  // rather than swallowed by the broader pattern.
  s = s.replace(/\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*/g, "<REDACTED_JWT>");
  // The `(?!<REDACTED)` guard is not decoration. Without it this rule matches
  // `Bearer <REDACTED_JWT>` — the output of the line above — and rewrites it to the
  // vaguer `Bearer <REDACTED>`, silently undoing the precision the JWT rule exists
  // to provide. Ordering alone does NOT protect an earlier label.
  s = s.replace(/\b(Bearer)\s+(?!<REDACTED)\S+/gi, "$1 <REDACTED>");

  // LAYER 4 — generic key/value, for a credential introduced by its field name.
  s = s.replace(
    /\b(x-api-key|api[-_]?key|auth[-_]?token|token|secret|password)(["'\s:=]+)(?!<REDACTED)([^\s"',}]{8,})/gi,
    "$1$2<REDACTED>",
  );

  // LAYER 5 — UNCONDITIONAL entropy catch-all. No prefix, no frame, no env
  // knowledge: any sufficiently long run mixing upper, lower and digit is treated
  // as credential material. This is what makes the filter independent of the pool
  // and of the provider — a token from a service this pipeline has never heard of
  // is masked on shape alone. `looksLikeSecretRun` is what keeps it from eating
  // git SHAs and UUIDs.
  s = s.replace(new RegExp(`[A-Za-z0-9_-]{${MIN_ENTROPY_LEN},}`, "g"), (run) =>
    looksLikeSecretRun(run) ? "<REDACTED_HIGH_ENTROPY>" : run,
  );

  return s;
}

/**
 * Standardized infrastructure failure codes — the CLOSED VOCABULARY.
 *
 * Every code a reader will ever see is in this object, and every published
 * sentence is assembled from it. Nothing derived from an upstream message reaches
 * a pull request, which is a stronger property than filtering: it does not depend
 * on a pattern list being complete.
 *
 * `AUTH_MALFORMED_CREDENTIAL` is separate from `AUTH_REJECTED` because the remedy
 * differs and it is the incident's own shape — the header was refused before the
 * request was sent, so the secret's FORMATTING is wrong, not its value. "Rotate the
 * token" is the wrong instruction there and would reproduce the fault.
 */
export const INFRA_CODES = Object.freeze({
  AUTH_MALFORMED_CREDENTIAL: "the configured credential is malformed and was rejected before the request was sent",
  AUTH_REJECTED: "credentials rejected",
  // The POOL-level outcome, distinct from the per-credential ones above. Every slot
  // has been retired, so there is no failover left and no session was opened. It is
  // its own code because the remedy is different again: the single-credential codes
  // point at one secret, this one says the account has nothing usable at all.
  POOL_EXHAUSTED: "every credential in the pool was retired",
  USAGE_LIMIT: "usage or session limit reached",
  RATE_LIMITED: "rate limited",
  UPSTREAM_ERROR: "upstream API error",
  NO_RESPONSE: "no response from the API",
  // Our OWN ceilings, split by which one was hit. Splitting rather than carrying
  // the SDK's subtype string as free text is what keeps the vocabulary closed: the
  // operator still learns whether to raise turns or raise the budget, and an
  // unrecognised subtype degrades to the generic code instead of smuggling text out.
  RUN_LIMIT: "run ceiling reached",
  RUN_LIMIT_TURNS: "turn ceiling reached",
  RUN_LIMIT_BUDGET: "cost ceiling reached",
  RUN_LIMIT_OUTPUT_RETRIES: "structured-output retry ceiling reached",
  NO_OUTPUT: "no output produced",
});

/**
 * SDK limit subtype → our code. A `Map` rather than a name transform so an
 * unrecognised subtype cannot become part of a published string.
 */
const RUN_LIMIT_CODES = new Map([
  ["error_max_turns", "RUN_LIMIT_TURNS"],
  ["max_turns", "RUN_LIMIT_TURNS"],
  ["error_max_budget_usd", "RUN_LIMIT_BUDGET"],
  ["error_max_structured_output_retries", "RUN_LIMIT_OUTPUT_RETRIES"],
]);

/**
 * A closed usage window on the ACCOUNT, in the upstream's own prose.
 *
 * Exported and imported by `ask.mjs` rather than copied there. The two were
 * separate constants with a comment on each promising they matched, and they
 * drifted the moment one of them had to grow: this rule decides `USAGE_LIMIT`,
 * `USAGE_LIMIT` is the ONLY thing `isAccountLimit` fails over on, so a period
 * this pattern misses is a period the credential pool cannot route around.
 *
 * The period alternation is the fix for a live outage. "You've hit your weekly
 * limit · resets 11pm (UTC)" matched neither `session` nor `usage`, so it fell
 * through to `RATE_LIMITED` (it arrives under a 429) — retryable, never a
 * failover — and one account's weekly window froze the review panel across
 * every open PR in the repo while three healthy credentials sat unused.
 *
 * `session|usage|weekly|daily|monthly` is a closed set of billing periods, not
 * a widening. The earlier over-match this file's history warns about came from
 * unanchored fragments (`resets?\b` catching `ECONNRESET`, bare `quota`); every
 * member here is still anchored to a literal ` limit`, so the pattern can only
 * match text that already says a limit was reached.
 */
export const SESSION_LIMIT_RE = /\b(?:session|usage|weekly|daily|monthly)\s+limit\b/i;

/**
 * An upstream request id, e.g. `req_011CQVdF7Wt3xYzAbCdEfGh`.
 *
 * Fully anchored and length-bounded, because this is the ONE value allowed out of
 * `classifyInfraError` that is not a number. It is the identifier support asks for
 * first, and the entropy rule masks it (mixed case, over 24 characters) — so
 * without this it would be unavailable everywhere, log included.
 *
 * Extracting it rather than exempting it is the point. An exemption punches a hole
 * in a filter that is meant to be unconditional, and the hole is open to whatever
 * else happens to match. This reads the id out of the text and re-emits it FROM THE
 * MATCH, so the only thing that can leave is `req_` plus bounded alphanumerics.
 */
const REQUEST_ID_RE = /\breq_[A-Za-z0-9]{10,40}\b/;

/**
 * The request id in `text`, or null.
 *
 * Refuses to emit anything that overlaps a live credential. No provider here issues
 * a `req_`-prefixed secret, so this cannot fire in practice — it is here so the
 * channel is provably disjoint from the credential set rather than disjoint by
 * assumption about token formats we do not control.
 */
export function requestIdFrom(text, env = process.env) {
  const match = String(text ?? "").match(REQUEST_ID_RE);
  if (!match) return null;
  const id = match[0];
  for (const secret of secretsFromEnv(env)) {
    if (secret.includes(id) || id.includes(secret)) return null;
  }
  return id;
}

/**
 * The signature of a credential the HTTP client refused to send.
 *
 * Matched on the FRAME, never on the value. This is the one classification that
 * reads upstream text for something other than a limit phrase, and it reads only
 * enough to distinguish "your secret has a typo" from "your secret is wrong".
 */
const MALFORMED_CREDENTIAL_RE =
  /\b(?:invalid (?:header )?value|has invalid value|invalid character|illegal (?:header )?value)\b/i;

/**
 * Classify a failure into the closed vocabulary.
 *
 * Reads the RAW text — this is a classifier, and scrubbing its input would make it
 * worse at its job for no benefit, because it emits none of what it reads. What it
 * returns carries no upstream prose at all.
 *
 * `context` is CONSTRUCTED, never quoted: numbers, plus `requestId` — the one
 * string, and only ever the literal match of an anchored, length-bounded pattern
 * that no credential format here can satisfy. There is no free-text field, so
 * there is no channel for an unreviewed string to ride out on.
 *
 * Always returns a code — callers use the result as the truthy "this was an infra
 * failure" marker, so a null return would silently reclassify an outage as an
 * ordinary fail-closed verdict.
 */
export function classifyInfraError({
  kind = "api-error",
  status = null,
  detail = "",
  turns = null,
  subtype = null,
} = {}) {
  const text = typeof detail === "string" ? detail : String(detail ?? "");
  const context = {};
  if (Number.isFinite(Number(status)) && status !== null && status !== "") context.status = Number(status);
  if (Number.isFinite(turns)) context.turns = turns;
  // Read from the RAW text: by the time `detail` has been through `redactSecrets`
  // the entropy rule has already masked the id.
  const requestId = requestIdFrom(text);
  if (requestId) context.requestId = requestId;

  const code = (() => {
    // Our own ceilings first: they are not an upstream failure at all, and their
    // `detail` is generated by this repo rather than quoted from anywhere.
    if (kind === "limit") return RUN_LIMIT_CODES.get(String(subtype ?? "")) ?? "RUN_LIMIT";
    if (kind === "no-output") return "NO_OUTPUT";
    // Also decided by kind alone, and for the same reason: pool exhaustion is this
    // repo's own bookkeeping — `poolExhaustedError` builds it from a slot count —
    // so there is no upstream text to inspect, and inspecting the retired slots'
    // detail would only report whichever one happened to fail last.
    if (kind === "pool-exhausted") return "POOL_EXHAUSTED";
    // A malformed credential outranks the status check because it typically has
    // NO status — the request never left the client — and would otherwise fall
    // through to the far less actionable NO_RESPONSE.
    if (MALFORMED_CREDENTIAL_RE.test(text)) return "AUTH_MALFORMED_CREDENTIAL";
    // Limit before status: it is the actionable one, and it arrives under a 429
    // that would otherwise read as a plain rate limit.
    if (SESSION_LIMIT_RE.test(text)) return "USAGE_LIMIT";
    // NULLISH IS CHECKED FIRST, and it has to be: `Number(null)` is 0, which is
    // finite, so testing `Number.isFinite` first reports a request that never got
    // a response as "HTTP 0" — a status no server ever sent.
    if (status == null || status === "") return "NO_RESPONSE";
    const s = Number(status);
    if (!Number.isFinite(s)) return "UPSTREAM_ERROR";
    if (s === 401 || s === 403) return "AUTH_REJECTED";
    if (s === 429) return "RATE_LIMITED";
    return "UPSTREAM_ERROR";
  })();

  return { code, reason: INFRA_CODES[code], context };
}

/**
 * Render a classified failure as the one string we are willing to publish.
 *
 * Every character originates in `INFRA_CODES` or in a number that has been through
 * `Number()`. Nothing here can carry a credential, whatever the upstream said.
 */
export function renderInfraError(classified) {
  const { code, reason, context = {} } = classified || {};
  const parts = [];
  if (Number.isFinite(context.status)) parts.push(`HTTP ${context.status}`);
  if (Number.isFinite(context.turns)) parts.push(`${context.turns} turns`);
  if (context.requestId) parts.push(context.requestId);
  const suffix = parts.length ? ` (${parts.join(", ")})` : "";
  return `[${code}] ${reason}${suffix}`;
}

/**
 * One-shot: failure in, publishable string out.
 *
 * The function every renderer should reach for, so that "classify then render"
 * cannot be half-applied at a call site.
 */
export function publicInfraReason(err) {
  return renderInfraError(classifyInfraError(err || {}));
}
