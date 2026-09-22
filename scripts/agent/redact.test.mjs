import test from "node:test";
import assert from "node:assert/strict";

import {
  redactSecrets,
  requestIdFrom,
  secretsFromEnv,
  credentialEnvNames,
  classifyInfraError,
  publicInfraReason,
  INFRA_CODES,
} from "./redact.mjs";
import { MAX_SLOTS } from "./token-pool.mjs";
import { classifyResult } from "./ask.mjs";
import { infraSummary, lensFailureSummary, lensInfraReason } from "./review-panel.mjs";
import { renderGuardSummary, guardVerdictLine } from "./guard-verdict.mjs";
import { renderSessionSummary } from "./session-job-summary.mjs";
import { renderFixEffort } from "./metrics.mjs";

// The credential from the incident: a well-formed OAuth token with a stray space
// rotated into the middle of it. The space is what made the header structurally
// invalid, which is what made the HTTP client quote it back.
const MALFORMED = "sk-ant-oat01-AbCdEf1234567890XyZw QqRrSs9876543210AbCd";
const INCIDENT = `Header 'Authorization' has invalid value: ${MALFORMED}`;

/** Every fragment that must never appear in published text. */
function assertNoLeak(text, secret = MALFORMED) {
  for (const fragment of secret.split(/\s+/)) {
    assert.ok(!text.includes(fragment), `leaked "${fragment}" in: ${text}`);
  }
}

// --- the incident -----------------------------------------------------------

test("redactSecrets: the incident, with the env layer switched OFF", () => {
  // `extra: []` is the whole point of this test. The env layer is the one that
  // knows credential names, and it is exactly the layer that rotted when the pool
  // grew from one token to nine. Everything below has to hold without it.
  const out = redactSecrets(INCIDENT, { extra: [] });
  assertNoLeak(out);
  assert.match(out, /<REDACTED/);
});

test("redactSecrets: pool-independence — any slot, including past MAX_SLOTS", () => {
  // The regression this port exists for. An allowlist headed by the unsuffixed
  // CLAUDE_CODE_OAUTH_TOKEN covers slot zero and nothing else; these are the
  // slots a verbatim port of the upstream fix would have been blind to.
  for (const name of [
    "CLAUDE_CODE_OAUTH_TOKEN_5",
    `CLAUDE_CODE_OAUTH_TOKEN_${MAX_SLOTS}`,
    // Deliberately BEYOND the pool: a slot that does not exist yet, so no
    // enumeration of any kind can cover it. It must still be masked.
    `CLAUDE_CODE_OAUTH_TOKEN_${MAX_SLOTS + 7}`,
  ]) {
    const env = { [name]: MALFORMED };
    const out = redactSecrets(INCIDENT, { extra: secretsFromEnv(env) });
    assertNoLeak(out, MALFORMED);
  }
});

test("credentialEnvNames: derived from the pool, so it cannot drift from MAX_SLOTS", () => {
  const names = credentialEnvNames();
  assert.ok(names.includes("CLAUDE_CODE_OAUTH_TOKEN"));
  for (let i = 1; i <= MAX_SLOTS; i++) {
    assert.ok(names.includes(`CLAUDE_CODE_OAUTH_TOKEN_${i}`), `slot ${i} missing`);
  }
  assert.ok(names.includes("ANTHROPIC_API_KEY"));
  assert.ok(names.includes("GITHUB_TOKEN"));
});

// --- the layers, each proven on its own -------------------------------------

test("layer: echo frames mask a credential of a shape no pattern knows", () => {
  // The strongest rule, because it keys on the SENTENCE rather than the secret.
  // A provider invented tomorrow is covered by this and nothing else.
  const alien = "ZZ|||UNRECOGNISABLE|||CREDENTIAL|||9999";
  const out = redactSecrets(`Header 'Authorization' has invalid value: ${alien}`, { extra: [] });
  assert.ok(!out.includes(alien), out);
  assert.match(out, /<REDACTED>/);
});

test("layer: entropy catch-all needs no prefix, no frame and no env", () => {
  // No `sk-`, no `Bearer`, no `token:` lead-in, not in the environment. Shape alone.
  const opaque = "Qq7WweERrTtYyUu1234567890AaBbCc";
  const out = redactSecrets(`the service returned ${opaque} and gave up`, { extra: [] });
  assert.ok(!out.includes(opaque), out);
  assert.match(out, /<REDACTED_HIGH_ENTROPY>/);
});

test("layer: env fragments catch a malformed token quoted back after splitting", () => {
  // The client has already split the value, so the text on the page never equals
  // the value in the environment — the case exact-substring matching (and GitHub's
  // own log masker) sails straight past.
  const secrets = secretsFromEnv({ CLAUDE_CODE_OAUTH_TOKEN: MALFORMED });
  const [head, tail] = MALFORMED.split(/\s+/);
  assert.ok(secrets.includes(head) && secrets.includes(tail), "fragments not collected");
  // Sorted longest-first, so the full value cannot be chewed into by its own piece.
  assert.deepEqual([...secrets].sort((a, b) => b.length - a.length), secrets);
  const out = redactSecrets(`rejected ${head} ... and ${tail}`, { extra: secrets });
  assertNoLeak(out);
});

test("layer: a prefixed token is masked whole across arbitrary whitespace", () => {
  // Rejoining chunks with a single space would slice short here and re-emit the
  // tail of the secret next to the label.
  const out = redactSecrets("key sk-ant-abc123XY   dEf12345678 rest", { extra: [] });
  assert.ok(!out.includes("dEf12345678"), out);
  assert.match(out, /rest$/, "prose after the secret must survive");
});

test("layer: the continuation rule stops at prose, not at the end of the line", () => {
  const out = redactSecrets("sk-ant-abcdef1234XY rejected by the server", { extra: [] });
  assert.match(out, /rejected by the server$/);
});

test("ordering: a later rule never degrades an earlier, more specific label", () => {
  // Without the `(?!<REDACTED)` guard the Bearer rule rewrites `<REDACTED_JWT>` —
  // the output of the line above it — into the vaguer `<REDACTED>`. Ordering alone
  // does not protect it.
  const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc123signature";
  const out = redactSecrets(`Authorization: Bearer ${jwt}`, { extra: [] });
  assert.match(out, /<REDACTED_JWT>/);
  assert.ok(!out.includes("abc123signature"), out);
});

// --- the counterweight ------------------------------------------------------

test("redactSecrets: leaves ordinary diagnostic text completely intact", () => {
  // Over-redaction destroys the diagnostics this pipeline exists to produce, so
  // this is a real requirement, not a nicety. Git SHAs and UUIDs are the dangerous
  // ones: both are long, both are high-entropy to a naive rule, and this pipeline
  // prints them constantly. Neither has an uppercase character, which is precisely
  // what `looksLikeSecretRun` keys on.
  const unchanged = [
    "commit ffbee7272a882e305161b6deb4367432cdd522fa failed to apply",
    "run 8163e7ad-d653-4f7c-b7c9-0857a0a86759 timed out after 900s",
    "error_max_structured_output_retries after 9 turns",
    "You've hit your session limit · resets 3:30pm (UTC)",
    "at /home/runner/work/wafflebase/scripts/agent/review-panel.mjs:2611:15",
    "TypeError: Cannot read properties of undefined (reading 'findings')",
    "expected 3 findings, received 0 — the lens produced no verdict",
  ];
  for (const s of unchanged) {
    assert.equal(redactSecrets(s, { extra: [] }), s, `over-redacted: ${s}`);
  }
});

test("secretsFromEnv: ignores short values that would censor ordinary words", () => {
  assert.deepEqual(secretsFromEnv({ CLAUDE_CODE_OAUTH_TOKEN: "short" }), []);
  // A long value whose fragments are short keeps the whole value, drops the pieces.
  const secrets = secretsFromEnv({ CLAUDE_CODE_OAUTH_TOKEN: "abcdefghij the a" });
  assert.ok(secrets.includes("abcdefghij"));
  assert.ok(!secrets.includes("the"), "a 3-char fragment would censor prose");
});

// --- the closed vocabulary --------------------------------------------------

test("classifyInfraError: every branch, and no upstream text in the output", () => {
  const cases = [
    [{ status: null, detail: INCIDENT }, "AUTH_MALFORMED_CREDENTIAL"],
    [{ status: 401, detail: "invalid x-api-key" }, "AUTH_REJECTED"],
    [{ status: 403, detail: "forbidden" }, "AUTH_REJECTED"],
    [{ status: 429, detail: "You've hit your session limit" }, "USAGE_LIMIT"],
    // The live outage: a weekly window arrives under a 429 like the others, and
    // reading it as RATE_LIMITED made it retryable and un-failoverable, so one
    // account's reset froze the review panel across every open PR.
    [{ status: 429, detail: "You've hit your weekly limit · resets 11pm (UTC)" }, "USAGE_LIMIT"],
    [{ status: 429, detail: "daily limit reached" }, "USAGE_LIMIT"],
    [{ status: 429, detail: "monthly limit reached" }, "USAGE_LIMIT"],
    // Still a plain rate limit: the period words only count next to ` limit`.
    [{ status: 429, detail: "rate limit exceeded" }, "RATE_LIMITED"],
    [{ status: 429, detail: "too many requests this week" }, "RATE_LIMITED"],
    [{ status: 529, detail: "overloaded_error" }, "UPSTREAM_ERROR"],
    [{ status: "weird", detail: "?" }, "UPSTREAM_ERROR"],
    [{ status: null, detail: "fetch failed" }, "NO_RESPONSE"],
    [{ kind: "limit", subtype: "error_max_turns" }, "RUN_LIMIT_TURNS"],
    [{ kind: "limit", subtype: "error_max_budget_usd" }, "RUN_LIMIT_BUDGET"],
    [{ kind: "limit", subtype: "error_max_structured_output_retries" }, "RUN_LIMIT_OUTPUT_RETRIES"],
    [{ kind: "limit", subtype: "something_new" }, "RUN_LIMIT"],
    [{ kind: "no-output", detail: "subtype=error" }, "NO_OUTPUT"],
  ];
  for (const [input, code] of cases) {
    const got = classifyInfraError(input);
    assert.equal(got.code, code, JSON.stringify(input));
    assert.equal(got.reason, INFRA_CODES[code]);
    // `context` is constructed, never quoted: numbers, plus `requestId` — which may
    // only ever be the literal match of the anchored pattern, never free text.
    for (const [k, v] of Object.entries(got.context)) {
      if (k === "requestId") assert.match(v, /^req_[A-Za-z0-9]{10,40}$/);
      else assert.equal(typeof v, "number");
    }
  }
});

test("classifyInfraError: an absent status is NO_RESPONSE, never 'HTTP 0'", () => {
  // `Number(null)` is 0 and 0 is finite, so a `Number.isFinite` test placed before
  // the nullish check reports a request that never got a response as a status no
  // server ever sent.
  for (const status of [null, undefined, ""]) {
    const out = publicInfraReason({ status, detail: "fetch failed" });
    assert.equal(out, `[NO_RESPONSE] ${INFRA_CODES.NO_RESPONSE}`);
    assert.ok(!out.includes("HTTP 0"), out);
  }
});

test("publicInfraReason: carries no upstream text, whatever the upstream said", () => {
  const out = publicInfraReason({ status: null, detail: INCIDENT });
  assertNoLeak(out);
  assert.match(out, /^\[AUTH_MALFORMED_CREDENTIAL\]/);
  // The distinct code earns its place: the remedy is "fix the secret's
  // formatting", which is not what AUTH_REJECTED would tell an operator.
  assert.notEqual(
    publicInfraReason({ status: 401, detail: "authentication_error" }),
    out,
  );
});

// --- the published surfaces -------------------------------------------------

test("no surface publishes the credential — the incident, end to end", () => {
  // The measurement from the incident write-up: drive the real SDK message shape
  // through every renderer that publishes a failure, and assert zero leaks.
  const message = { subtype: "success", is_error: true, result: INCIDENT };
  const c = classifyResult(message);

  assert.equal(c.code, "AUTH_MALFORMED_CREDENTIAL");
  assertNoLeak(c.detail); // redacted at birth, log-only
  assertNoLeak(c.reason); // closed vocabulary

  // The panel's own failure path, driven with the error it really constructs.
  const panelErr = Object.assign(new Error("all lens samples failed"), {
    infra: true,
    status: c.status,
    detail: c.detail,
    code: c.code,
  });

  const reason = publicInfraReason({ kind: "api-error", status: c.status, detail: c.detail });
  const surfaces = {
    "lens summary (check-run body + PR comment)": lensFailureSummary(panelErr),
    "guard summary (job summary)": renderGuardSummary({ decision: "page", reason: "infra", detail: infraSummary({ reason }) }),
    "guard summary (proceed path)": renderGuardSummary({ decision: "proceed", infra: INCIDENT }),
    "guard verdict line": guardVerdictLine({ decision: "page", reason: "infra", detail: infraSummary({ reason }) }),
    "session job summary": renderSessionSummary({ rec: null, outcome: c }),
    "fix-effort PR comment": renderFixEffort({ rec: null, outcome: c, head: "abc1234", runUrl: null }),
  };
  for (const [name, body] of Object.entries(surfaces)) {
    assertNoLeak(String(body), MALFORMED);
    assert.ok(!String(body).includes("invalid value: sk-"), `${name} echoed the frame`);
  }

  // Stronger than "no credential": the lens summary must contain no UPSTREAM PROSE
  // at all. Redaction alone would leave "Header 'Authorization' has invalid value:
  // <REDACTED>" — safe, but still an unreviewed sentence on a public PR, and safe
  // only while the pattern list stays complete. This asserts the closed vocabulary
  // is what got published, which is the guarantee that does not depend on patterns.
  const lens = lensFailureSummary(panelErr);
  for (const word of ["Header", "Authorization", "invalid value", "<REDACTED"]) {
    assert.ok(!lens.includes(word), `upstream prose survived into the lens summary: ${lens}`);
  }
  assert.equal(lens, infraSummary({ reason }));
});

test("lensFailureSummary: a genuine no-verdict stays an ordinary blocker", () => {
  // Not every failure is infra. A model that ran and produced nothing must NOT get
  // the infra prefix — that prefix makes both parsers drop the record, so tagging a
  // real fail-closed blocker with it would silently stop gating the merge.
  const out = lensFailureSummary(new Error("structured output not produced"));
  assert.match(out, /^Reviewer did not produce a valid verdict:/);
  assert.ok(!out.startsWith("Review could not run"), out);
});

test("legacy records without a code are still redacted on the way out", () => {
  // Session logs outlive a deploy, so a record written before the vocabulary
  // existed reaches these renderers with a raw `detail` and no `code`/`reason`.
  const legacy = { ok: false, kind: "api-error", status: null, detail: INCIDENT, retryable: false };
  assertNoLeak(renderSessionSummary({ rec: null, outcome: legacy }));
  assertNoLeak(renderFixEffort({ rec: null, outcome: legacy, head: "abc1234", runUrl: null }));
});

test("the execution-log artifact carries no raw upstream text", () => {
  // review-execution.json / hunt-execution.json are uploaded as workflow artifacts,
  // downloadable by anyone with repo read access — everyone, on a public repo. They
  // are `sessionLog` verbatim, so the scrub happens at the single shared producer.
  const sessionLog = [];
  const message = { type: "result", subtype: "success", is_error: true, result: INCIDENT, usage: { input_tokens: 5 } };
  // Mirrors runSession's push. Asserted against the serialised form, since that is
  // what actually lands in the artifact.
  const safe = typeof message.result === "string" ? { ...message, result: redactSecrets(message.result) } : message;
  sessionLog.push(safe);
  assertNoLeak(JSON.stringify(sessionLog));
  // The metrics fields must survive untouched — they are the log's only consumers.
  assert.equal(sessionLog[0].usage.input_tokens, 5);
  assert.equal(sessionLog[0].type, "result");
});

// --- request ids: extracted into the vocabulary, not exempted from the filter ---

test("requestIdFrom: extracted from raw text, and published in the reason", () => {
  // Support asks for this identifier first, and the entropy rule masks it (mixed
  // case, over 24 chars) — so it has to be lifted out as structured context rather
  // than exempted, which would punch a hole in a filter meant to be unconditional.
  const id = "req_011CQVdF7Wt3xYzAbCdEfGh";
  assert.equal(requestIdFrom(`overloaded_error (request id: ${id})`), id);
  const out = publicInfraReason({ status: 529, detail: `overloaded_error (request id: ${id})` });
  assert.equal(out, `[UPSTREAM_ERROR] ${INFRA_CODES.UPSTREAM_ERROR} (HTTP 529, ${id})`);
});

test("requestIdFrom: only the literal match escapes, never its surroundings", () => {
  const around = requestIdFrom("prefix req_011CQVdF7Wt3xYzAbCd sk-ant-oat01-SECRETVALUE1234 suffix");
  assert.equal(around, "req_011CQVdF7Wt3xYzAbCd");
  // Nothing that is not a request id can ride along.
  for (const text of ["req_short", "xreq_011CQVdF7Wt3xYzAbCd", "no id here", ""]) {
    const got = requestIdFrom(text);
    assert.ok(got === null || /^req_[A-Za-z0-9]{10,40}$/.test(got), `${text} -> ${got}`);
  }
});

test("requestIdFrom: refuses anything overlapping a live credential", () => {
  // Cannot happen with a real token (no provider here issues a `req_` secret), so
  // this pins that the channel is provably disjoint from the credential set rather
  // than disjoint by assumption about formats we do not control.
  const id = "req_011CQVdF7Wt3xYzAbCdEfGh";
  assert.equal(requestIdFrom(`err ${id}`, { CLAUDE_CODE_OAUTH_TOKEN: id }), null);
  assert.equal(requestIdFrom(`err ${id}`, { CLAUDE_CODE_OAUTH_TOKEN: `${id}TAIL` }), null);
  assert.equal(requestIdFrom(`err ${id}`, {}), id, "an unrelated env must not suppress it");
});

test("the panel publishes the request id, which it could not re-derive itself", () => {
  // `lensFailureSummary` only ever sees the REDACTED detail, by which point the
  // entropy rule has masked the id. It must therefore carry `reason` through from
  // classifyResult rather than reclassify.
  const id = "req_011CQVdF7Wt3xYzAbCdEfGh";
  const c = classifyResult({
    subtype: "success", is_error: true, api_error_status: 529,
    result: `overloaded_error (request id: ${id})`,
  });
  assert.ok(!c.detail.includes(id), "the redacted detail cannot carry it");
  const err = Object.assign(new Error("all lens samples failed"),
    { infra: true, status: c.status, detail: c.detail, code: c.code, reason: c.reason });
  assert.ok(lensFailureSummary(err).includes(id), "the request id was lost on the way to the PR");
});

test("both renderers classify a drained pool from its kind, not from an assumed one", () => {
  // A record persisted before `poolExhaustedError` carried the vocabulary — or any
  // caller that builds a bare `{ infra, kind }` — reaches these two renderers with
  // no `reason` to prefer, so each has to re-derive one. Hard-coding `"api-error"`
  // there sent a drained pool down the status branches, and with no status and no
  // detail to read it came out as NO_RESPONSE: "no response from the API", for an
  // outage in which no request was ever sent.
  const bare = Object.assign(new Error("review: every credential in the pool (2) was retired"), {
    infra: true, kind: "pool-exhausted",
  });
  assert.equal(lensInfraReason(bare), "[POOL_EXHAUSTED] every credential in the pool was retired");
  assert.equal(lensFailureSummary(bare), infraSummary({ reason: "[POOL_EXHAUSTED] every credential in the pool was retired" }));
  // The operator's own message, which is the one that names the slot count, stays
  // in the log; only the vocabulary is published.
  assert.ok(!lensFailureSummary(bare).includes("(2)"));
});

test("lensInfraReason: null for a model that ran, whatever else the error carries", () => {
  // The field is read as a boolean by every consumer — the workflow persists an
  // empty carry-forward on it, eval/run.mjs voids the item, PANEL_INFRA_ERROR pages
  // on it — so a non-null value for a genuine no-verdict would stop that round
  // gating the merge. `infra` is the only thing that may decide this.
  assert.equal(lensInfraReason(Object.assign(new Error("x"), { kind: "limit", code: "RUN_LIMIT_TURNS" })), null);
  assert.equal(lensInfraReason(Object.assign(new Error("x"), { kind: "no-output" })), null);
  assert.equal(lensInfraReason(new Error("all lens samples failed")), null);
  assert.equal(lensInfraReason({}), null);
  assert.equal(lensInfraReason(), null);
  // And non-null the moment the producer says the session never opened.
  assert.equal(lensInfraReason({ infra: true, kind: "api-error", status: 429 }), "[RATE_LIMITED] rate limited (HTTP 429)");
});
