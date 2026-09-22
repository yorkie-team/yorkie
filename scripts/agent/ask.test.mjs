import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import {
  askStructured,
  assertAllowedTools,
  assertBoundaryMatchesSdk,
  assertEffort,
  buildSessionOptions,
  EFFORT_LEVELS,
  PERMITTED_TOOLS,
  SYSTEM_PROMPT_DYNAMIC_BOUNDARY,
  classifyResult,
  withRetry,
  isAccountLimit,
  isCredentialRejected,
  nextCredential,
  poolExhaustedError,
  sessionNeverRan,
  failoverStep,
  __setTokenPool,
  credentialEnv,
} from "./ask.mjs";
import { createTokenPool, poolEnvNames } from "./token-pool.mjs";
import { REVIEW_TOOLS } from "./review-panel.mjs";

// ask.mjs exists to make the read-only tool grant a CHECKED invariant instead of
// a hardcoded array. These tests are that check, so they are deliberately mostly
// NEGATIVE: the interesting property is what the gate refuses.
//
// None of them install or touch the SDK. `assertAllowedTools` runs before the
// lazy `import()`, which is what lets the askStructured tests below assert the
// validation happens at all without a token or a network call.

// --- the allow-list itself ---------------------------------------------------

test("PERMITTED_TOOLS: pinned to exactly the read-only triple", () => {
  // A LITERAL pin, not derived from the module. The point is that widening the
  // grant must break a test — if this asserted against PERMITTED_TOOLS itself it
  // would pass no matter what anyone added to it.
  assert.deepEqual([...PERMITTED_TOOLS], ["Read", "Grep", "Glob"]);
  assert.ok(Object.isFrozen(PERMITTED_TOOLS));
});

test("assertAllowedTools: accepts the full permitted set and any subset", () => {
  assert.deepEqual(assertAllowedTools(["Read", "Grep", "Glob"]), ["Read", "Grep", "Glob"]);
  assert.deepEqual(assertAllowedTools(["Read"]), ["Read"]);
  // A copy, not the caller's array — mutating the input afterwards must not
  // retroactively change what was validated and handed to the session.
  const input = ["Read"];
  const validated = assertAllowedTools(input);
  input.push("Bash");
  assert.deepEqual(validated, ["Read"]);
});

test("assertAllowedTools: refuses execution / write / network / spawn tools", () => {
  // Literal names, deliberately NOT looped over a list the module exports.
  for (const tool of ["Bash", "Write", "Edit", "MultiEdit", "NotebookEdit", "WebFetch", "WebSearch"]) {
    assert.throws(() => assertAllowedTools(["Read", tool]), /is not permitted/, `${tool} must be refused`);
  }
});

test("assertAllowedTools: refuses tools an author of a deny-list would have missed", () => {
  // The regression this allow-list exists for. Every name here is a REAL tool in
  // pinned SDK 0.3.217 (sdk-tools.d.ts) that a hand-written deny-list of
  // "dangerous" names did not contain. `Agent` is the worst of them: it spawns
  // subagents that inherit the parent's tools, i.e. the exact respawn escape the
  // module claims to prevent. An allow-list refuses all of them for free.
  for (const tool of ["Agent", "REPL", "Workflow", "CronCreate", "RemoteTrigger", "Artifact", "Mcp", "SendFeedback"]) {
    assert.throws(() => assertAllowedTools(["Read", tool]), /is not permitted/, `${tool} must be refused`);
  }
});

test("assertAllowedTools: refuses scoped permission-rule syntax", () => {
  // THE critical bypass. `allowedTools` entries are permission RULES, so these
  // are legal SDK input — and because the list is an auto-APPROVE list, a
  // name-based deny-list would not merely fail to catch them, it would
  // pre-approve the shell invocation past permissionMode:'dontAsk'.
  for (const rule of ["Bash(git diff:*)", "Bash(*)", "Bash(git log:*)", "WebFetch(domain:example.com)", "Read(/etc/**)"]) {
    assert.throws(() => assertAllowedTools([rule]), /is not permitted/, `${rule} must be refused`);
  }
});

test("assertAllowedTools: refuses MCP tool names", () => {
  for (const t of ["mcp__server__exec", "mcp__workspace__bash", "mcp__*"]) {
    assert.throws(() => assertAllowedTools([t]), /is not permitted/, `${t} must be refused`);
  }
});

test("assertAllowedTools: refuses non-canonical spellings rather than repairing them", () => {
  // Strictness is the design: a validator that normalizes its input is guessing
  // at intent, and a capability gate is the last place to guess. " Bash" is the
  // specific case a trim-then-compare-untrimmed bug would have let through.
  for (const t of [" Bash", "Bash ", "bash", "READ", " Read", "Read\t"]) {
    assert.throws(() => assertAllowedTools([t]), /is not permitted/, `${JSON.stringify(t)} must be refused`);
  }
});

test("assertAllowedTools: missing / empty / malformed input fails closed", () => {
  // Required, not defaulted — a default would let a new caller inherit read-only
  // access silently and then widen it with no visible signal.
  assert.throws(() => assertAllowedTools(undefined), /required and must be a non-empty array/);
  assert.throws(() => assertAllowedTools(null), /required and must be a non-empty array/);
  assert.throws(() => assertAllowedTools("Read"), /required and must be a non-empty array/);
  // Empty is rejected rather than read as "no tools": the SDK would run an agent
  // that cannot read anything, burning a paid session to return nothing.
  assert.throws(() => assertAllowedTools([]), /required and must be a non-empty array/);
  assert.throws(() => assertAllowedTools(["Read", ""]), /must be non-empty strings/);
  assert.throws(() => assertAllowedTools(["Read", "   "]), /must be non-empty strings/);
  assert.throws(() => assertAllowedTools(["Read", 42]), /must be non-empty strings/);
  assert.throws(() => assertAllowedTools(["Read", null]), /must be non-empty strings/);
});

// --- askStructured actually enforces it --------------------------------------
// Without these, deleting the assertAllowedTools call from askStructured — the
// one line that makes any of the above load-bearing — leaves the suite green.

test("askStructured: validates the grant BEFORE opening a session", async () => {
  // The SDK is never reached: these reject with the VALIDATION error, and the
  // assertion on the message is what proves the ordering. If the import ran
  // first, an unresolvable/unauthenticated SDK would produce a different error.
  await assert.rejects(
    () => askStructured({ prompt: "x", schema: {}, allowedTools: ["Bash"] }),
    /is not permitted/,
    "a forbidden grant must fail before any session is opened",
  );
  await assert.rejects(
    () => askStructured({ prompt: "x", schema: {}, allowedTools: ["Bash(git diff:*)"] }),
    /is not permitted/,
    "a scoped rule must fail before any session is opened",
  );
  await assert.rejects(
    () => askStructured({ prompt: "x", schema: {} }),
    /required and must be a non-empty array/,
    "an omitted grant must fail before any session is opened",
  );
});

// There is deliberately NO "valid grant reaches the SDK" test here. Asserting
// that would mean letting askStructured past the validation gate, which opens a
// real model session — slow, and on any runner that has credentials it would
// burn a paid session from a unit suite. The complement is covered without a
// session by the direct `assertAllowedTools(REVIEW_TOOLS)` assertion below: a
// valid grant returns rather than throws, so the rejections above are the gate
// firing and not a blanket failure.

// --- the session options, including the cache boundary ------------------------
// `buildSessionOptions` is what askStructured hands the SDK verbatim, so these
// assert the shipped session shape without opening one (or having the SDK on
// disk, which the agent test lane does not).

test("buildSessionOptions: pins the read-only session shape", () => {
  const o = buildSessionOptions({ systemPrompt: "s", model: "m", repo: "/r", schema: {}, allowedTools: ["Read"] });
  // Both levers, same list — `tools` is the restriction, `allowedTools` only
  // pre-approves. Nothing observed this at the options level before.
  assert.deepEqual(o.tools, ["Read"]);
  assert.deepEqual(o.allowedTools, ["Read"]);
  assert.equal(o.permissionMode, "dontAsk");
  // cwd is an UNTRUSTED branch checkout; [] means no project hooks/agents load.
  assert.deepEqual(o.settingSources, []);
  assert.equal(o.cwd, "/r");
  // Omitted rather than passed as undefined, so the SDK default applies.
  assert.ok(!("maxTurns" in o), "an unset ceiling must not appear at all");
  assert.equal(buildSessionOptions({ schema: {}, maxTurns: 8, allowedTools: ["Read"] }).maxTurns, 8);
  // Same rule for effort: absent means "take the SDK default", not "high".
  assert.ok(!("effort" in o), "an unset effort must not appear at all");
  assert.equal(
    buildSessionOptions({ schema: {}, effort: "medium", allowedTools: ["Read"] }).effort,
    "medium",
  );
  // The gate still runs here — this is the one place it is reached.
  assert.throws(() => buildSessionOptions({ schema: {}, allowedTools: ["Bash"] }), /is not permitted/);
});

// --- pooled credentials -------------------------------------------------------

test("buildSessionOptions: no pooled token means today's plain env inheritance", () => {
  const o = buildSessionOptions({ schema: {}, allowedTools: ["Read"] });
  assert.ok(!("env" in o), "an unset credential must not appear at all");
});

test("buildSessionOptions: a pooled token rides in env, with process.env preserved", () => {
  const o = buildSessionOptions({ schema: {}, allowedTools: ["Read"], authToken: "tok-2" });
  assert.equal(o.env.CLAUDE_CODE_OAUTH_TOKEN, "tok-2");
  // The spread is the load-bearing part: `Options.env` REPLACES the subprocess
  // environment rather than merging it (sdk.d.ts:1408, pinned 0.3.217), so
  // dropping it strips PATH/HOME and the CLI never starts. Asserting on PATH
  // turns that into a failing test rather than a session that cannot spawn.
  assert.equal(o.env.PATH, process.env.PATH);
});

test("credentialEnv: the child gets ONE credential, never the rest of the pool", () => {
  // This process needs the whole pool — it fails over inside the round. The
  // child does not: it holds one credential and runs with cwd set to the
  // untrusted branch checkout. A plain `{...process.env}` spread would hand it
  // all nine, which is the blast radius harness-engineering.md records.
  const saved = {};
  for (const name of poolEnvNames()) {
    saved[name] = process.env[name];
    process.env[name] = `secret-${name}`;
  }
  try {
    const env = credentialEnv("chosen");
    assert.equal(env.CLAUDE_CODE_OAUTH_TOKEN, "chosen");
    for (const name of poolEnvNames().filter((n) => n !== "CLAUDE_CODE_OAUTH_TOKEN")) {
      assert.ok(!(name in env), `${name} must not reach the child`);
    }
    // Every value the pool did not own still has to survive, or the CLI cannot spawn.
    assert.equal(env.PATH, process.env.PATH);
  } finally {
    for (const [name, value] of Object.entries(saved)) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  }
});

test("credentialEnv: the failover path builds the same shape as the first attempt", () => {
  // The two used to be built inline in separate places; they must not drift,
  // because a failover that leaked the pool would undo the containment above.
  const first = buildSessionOptions({ schema: {}, allowedTools: ["Read"], authToken: "a" });
  assert.deepEqual(Object.keys(first.env).sort(), Object.keys(credentialEnv("a")).sort());
});

test("isAccountLimit: only a closed usage window, never another non-retryable failure", () => {
  assert.ok(isAccountLimit({ kind: "api-error", detail: "You've hit your session limit · resets 3:30pm (UTC)" }));
  assert.ok(isAccountLimit({ kind: "api-error", detail: "usage limit reached" }));
  // Every billing period, not just the two this rule started with: a `weekly
  // limit` that reads as anything else is a window the pool cannot route around.
  assert.ok(isAccountLimit({ kind: "api-error", detail: "You've hit your weekly limit · resets 11pm (UTC)" }));
  // A refused credential is a failover too, but NOT a limit — the remedy is to
  // rotate the secret, not to wait for a reset, and `isCredentialRejected` owns it.
  assert.equal(isAccountLimit({ kind: "api-error", status: 401, detail: "invalid x-api-key" }), false);
  // Our OWN ceilings, which classifyResult also marks non-retryable.
  assert.equal(isAccountLimit({ kind: "limit", detail: "error_max_turns after 9 turns" }), false);
  assert.equal(isAccountLimit({ kind: "no-output", detail: "subtype=error" }), false);
  for (const bad of [null, undefined, {}, { kind: "api-error" }]) {
    assert.equal(isAccountLimit(bad), false, JSON.stringify(bad));
  }
});

/** A pool of exactly `tokens`, independent of the ambient environment. */
function fakePool(tokens) {
  const env = { CLAUDE_CODE_OAUTH_TOKEN: "" };
  tokens.forEach((t, i) => { env[`CLAUDE_CODE_OAUTH_TOKEN_${i + 1}`] = t; });
  return createTokenPool({ env, runId: "0", runAttempt: "1" });
}

test("nextCredential: a closed window moves to the next token", () => {
  const pool = fakePool(["a", "b"]);
  const err = { kind: "api-error", detail: "session limit" };
  assert.equal(nextCredential({ pool, err, authToken: "a" }), "b");
});

test("nextCredential: a failure no other credential can fix gives up immediately", () => {
  const pool = fakePool(["a", "b"]);
  for (const err of [
    { kind: "limit", detail: "error_max_turns after 9 turns" },
    { kind: "api-error", status: 529, detail: "overloaded" },
    { kind: "api-error", status: 400, detail: "malformed schema" },
    { kind: "no-output", detail: "subtype=error" },
  ]) {
    assert.equal(nextCredential({ pool, err, authToken: "a" }), null, err.detail);
  }
  // …and none of them consumed a credential.
  assert.equal(pool.retiredCount(), 0);
});

test("isCredentialRejected: one refused secret, not a verdict on the pool", () => {
  assert.ok(isCredentialRejected({ kind: "api-error", code: "AUTH_REJECTED", detail: "<REDACTED>" }));
  // Status fallback, for callers that never went through classifyResult.
  assert.ok(isCredentialRejected({ kind: "api-error", status: 401, detail: "invalid x-api-key" }));
  assert.ok(isCredentialRejected({ kind: "api-error", status: 403, detail: "forbidden" }));
  // `code` wins over prose, exactly as in isAccountLimit: a redacted detail must
  // not be able to talk the classifier out of the code it was given.
  assert.equal(isCredentialRejected({ kind: "api-error", code: "USAGE_LIMIT", detail: "401" }), false);
  assert.equal(isCredentialRejected({ kind: "api-error", status: 429, detail: "rate limited" }), false);
  assert.equal(isCredentialRejected({ kind: "limit", status: 401, detail: "error_max_turns" }), false);
  for (const bad of [null, undefined, {}, { kind: "api-error" }]) {
    assert.equal(isCredentialRejected(bad), false, JSON.stringify(bad));
  }
});

test("nextCredential: a refused credential fails over to a healthy one", () => {
  // The regression this exists for: one of four pool secrets expired, roughly a
  // quarter of runs sharded onto it, and every one of those died on its first
  // call while three valid credentials sat unused in the same process.
  const pool = fakePool(["dead", "good"]);
  const err = { kind: "api-error", code: "AUTH_REJECTED", status: 401, detail: "<REDACTED>" };
  assert.equal(nextCredential({ pool, err, authToken: "dead" }), "good");
  assert.equal(pool.retiredCount(), 1);
});

test("poolExhaustedError: one report for a drained pool, whatever drained it", () => {
  // The two callers must not disagree. Before this was one builder, the entry
  // guard said "every credential retired" while the failover loop rethrew the
  // last slot's own error, so the same outcome read as "credentials rejected"
  // or "usage limit" depending on call ordering — one account's problem
  // published as the pool's verdict.
  const err = poolExhaustedError({ label: "reviewer", pool: fakePool(["a", "b"]) });
  assert.match(err.message, /^reviewer: every credential in the pool \(2\) was retired/);
  assert.equal(err.kind, "pool-exhausted");
  assert.equal(err.retryable, false, "withRetry must not back off around a condition that cannot clear in-run");
  // Nothing upstream may ride out on it: this message IS published verbatim by
  // review-panel.mjs's non-infra path.
  assert.ok(!/401|OAuth|limit ·/.test(err.message), err.message);
});

test("askStructured: a pool drained by a sibling fails before any session opens", async () => {
  // The entry-guard half of the same condition, and the only half reachable
  // without opening a session — this suite deliberately never lets
  // askStructured past its validation gate (see the note above buildSessionOptions).
  const pool = fakePool(["a"]);
  pool.advance("session limit", "a");
  assert.ok(pool.isExhausted());
  __setTokenPool(pool);
  try {
    await assert.rejects(
      () => askStructured({ prompt: "x", schema: {}, allowedTools: ["Read"] }),
      /every credential in the pool \(1\) was retired/,
    );
  } finally {
    __setTokenPool(null);
  }
});

test("failoverStep: the last retired slot reports the POOL, not that slot", () => {
  const pool = fakePool(["a", "b"]);
  const rejected = { kind: "api-error", code: "AUTH_REJECTED", status: 401, detail: "<REDACTED>" };

  // Still a live credential left → retry on it.
  assert.deepEqual(failoverStep({ pool, err: rejected, authToken: "a", label: "reviewer" }), { next: "b" });

  // That retired the last one → the pool's failure, not the slot's. Before this
  // split, the loop rethrew `rejected` here and an operator's first line read
  // "credentials rejected" for a pool that was simply used up.
  const drained = failoverStep({ pool, err: rejected, authToken: "b", label: "reviewer" });
  assert.equal(drained.next, undefined);
  assert.equal(drained.error.kind, "pool-exhausted");
  assert.match(drained.error.message, /every credential in the pool \(2\) was retired/);

  // A failure no credential can fix still belongs to the caller, untouched —
  // wrapping it would hide a turn ceiling behind a credential story.
  const ceiling = { kind: "limit", detail: "error_max_turns after 9 turns" };
  const fresh = fakePool(["x", "y"]);
  assert.equal(failoverStep({ pool: fresh, err: ceiling, authToken: "x", label: "reviewer" }).error, ceiling);
  assert.equal(fresh.retiredCount(), 0);
});

test("nextCredential: an all-bad pool drains and stops, it does not loop", () => {
  // The cost the old rule was written to avoid, now bounded and paid on purpose:
  // walking a wholly-bad pool costs one refusal per slot and ends with the pool
  // reporting that every credential was retired.
  const pool = fakePool(["a", "b"]);
  const err = { kind: "api-error", code: "AUTH_REJECTED", status: 401, detail: "<REDACTED>" };
  assert.equal(nextCredential({ pool, err, authToken: "a" }), "b");
  assert.equal(nextCredential({ pool, err, authToken: "b" }), null);
  assert.equal(pool.retiredCount(), 2);
  assert.ok(pool.isExhausted());
});

test("nextCredential: a dry pool gives up rather than looping", () => {
  const pool = fakePool(["solo"]);
  const err = { kind: "api-error", detail: "session limit" };
  assert.equal(nextCredential({ pool, err, authToken: "solo" }), null);
});

test("nextCredential: concurrent reports of one dead token retire it once", () => {
  // The panel runs lenses and samples at once, so several sessions hit the same
  // closed window within milliseconds. Without the authToken check the second
  // report would retire the healthy token the first one just moved to, and a
  // burst of concurrency would drain the pool in a single round.
  const pool = fakePool(["a", "b", "c"]);
  const err = { kind: "api-error", detail: "session limit" };
  const first = nextCredential({ pool, err, authToken: "a" });
  const second = nextCredential({ pool, err, authToken: "a" });
  assert.equal(first, "b");
  assert.equal(second, "b", "the late report must not burn a healthy token");
  assert.equal(pool.retiredCount(), 1);
});

test("EFFORT_LEVELS pins the SDK's levels as a literal", () => {
  // Deliberately NOT derived from the SDK: this repo's agent test lane runs with
  // the SDK absent. If a level is ever removed upstream, a literal list turns
  // that into a failing assertion rather than a session that silently falls back
  // to the default — the same silent-cost-regression shape assertBoundaryMatchesSdk
  // exists to catch. Matches `EffortLevel` at sdk.d.ts:539 in the pinned 0.3.217.
  assert.deepEqual([...EFFORT_LEVELS], ["low", "medium", "high", "xhigh", "max"]);
});

test("assertEffort: unset passes through, anything unrecognised throws", () => {
  assert.equal(assertEffort(undefined), undefined);
  for (const level of EFFORT_LEVELS) assert.equal(assertEffort(level), level);
  // Strict on purpose. A dropped-and-defaulted value is a COST regression with
  // no error, no changed output and nothing in the logs, so every ambiguity
  // fails loudly instead: casing, whitespace, near-misses and wrong types.
  for (const bad of ["MEDIUM", " medium", "med", "hgih", "", null, 0, 3, true, [], {}]) {
    assert.throws(() => assertEffort(bad), /effort/, JSON.stringify(bad));
  }
});

test("buildSessionOptions: an invalid effort throws before a session is opened", () => {
  // Ordering matters — the same reason the tool grant is validated first. This
  // runs with the SDK unresolvable, so reaching the throw proves nothing had to
  // be imported to get there.
  assert.throws(
    () => buildSessionOptions({ schema: {}, effort: "hgih", allowedTools: ["Read"] }),
    /effort/,
  );
});

test("buildSessionOptions: an array systemPrompt reaches the SDK VERBATIM", () => {
  // THE cache-cost guard. review-panel.mjs puts the shared diff before the
  // boundary marker so later sessions re-read it at ~0.1x; anything that joined,
  // stringified or reordered this array would keep every prompt working and every
  // other test green while quietly restoring the panel's old bill.
  const prompt = ["cacheable prefix", SYSTEM_PROMPT_DYNAMIC_BOUNDARY];
  const o = buildSessionOptions({ systemPrompt: prompt, schema: {}, allowedTools: ["Read"] });
  assert.deepEqual(o.systemPrompt, prompt);
  assert.ok(Array.isArray(o.systemPrompt), "the array form is what marks the cacheable prefix");
  assert.equal(o.systemPrompt[1], SYSTEM_PROMPT_DYNAMIC_BOUNDARY);
  // The string form must keep working — the verifier call still uses it.
  assert.equal(buildSessionOptions({ systemPrompt: "plain", schema: {}, allowedTools: ["Read"] }).systemPrompt, "plain");
});

test("SYSTEM_PROMPT_DYNAMIC_BOUNDARY: pinned literally, and drift from the SDK throws", () => {
  // A LITERAL pin, like PERMITTED_TOOLS above: the value is a protocol constant
  // from sdk.d.ts:6810, not something this repo gets to choose.
  assert.equal(SYSTEM_PROMPT_DYNAMIC_BOUNDARY, "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__");
  // Matching SDK → returns the value, no throw.
  assert.equal(
    assertBoundaryMatchesSdk({ SYSTEM_PROMPT_DYNAMIC_BOUNDARY: "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__" }),
    SYSTEM_PROMPT_DYNAMIC_BOUNDARY,
  );
  // Every way the mirror can go stale. A wrong marker is NOT an SDK error — it is
  // a prompt block that silently never caches, so this assertion is the only thing
  // standing between an SDK bump and a ~10x cost regression nobody would see.
  for (const sdk of [
    { SYSTEM_PROMPT_DYNAMIC_BOUNDARY: "__DYNAMIC_BOUNDARY__" }, // renamed value
    { SYSTEM_PROMPT_DYNAMIC_BOUNDARY: "" },
    {}, // export removed entirely
    null,
    undefined,
  ]) {
    assert.throws(() => assertBoundaryMatchesSdk(sdk), /drifted from the SDK/, `${JSON.stringify(sdk)} must throw`);
  }
});

test("REVIEW_TOOLS: the panel's actual grant is inside the permitted set", () => {
  // Pins what review-panel.mjs really passes at both call sites. Exported for
  // exactly this: previously nothing could observe the panel's grant at all.
  assert.deepEqual(REVIEW_TOOLS, ["Read", "Grep", "Glob"]);
  assert.deepEqual(assertAllowedTools(REVIEW_TOOLS), REVIEW_TOOLS, "the panel's own grant must pass its own gate");
});

// --- moved-from-review-panel behavior, asserted at its new home ---------------
// review-panel.test.mjs still covers these through the re-export and is untouched
// by this refactor (that is the evidence behavior did not change). These assert
// the same contract against ask.mjs directly, so deleting the re-export later
// cannot silently drop the coverage. Kept at parity with the originals, not
// weaker: retry-cap, terminal_reason and the returned value are all covered.

test("classifyResult: distinguishes verdict / api-error / no-output at its new home", () => {
  const ok = classifyResult({ subtype: "success", structured_output: { findings: [], summary: "s" } });
  assert.equal(ok.ok, true);
  assert.deepEqual(ok.output, { findings: [], summary: "s" });

  const quota = classifyResult({
    subtype: "success",
    is_error: true,
    api_error_status: 429,
    result: "You've hit your session limit · resets 3:30pm (UTC)",
  });
  assert.equal(quota.kind, "api-error");
  assert.equal(quota.status, 429);
  assert.equal(quota.retryable, false, "a session limit cannot clear in-run, so it must not be retried");

  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 529, result: "overloaded_error" }).retryable, true);
  assert.equal(classifyResult({ subtype: "error", is_error: true, api_error_status: 500, result: "internal error" }).retryable, true);
  assert.equal(classifyResult({ terminal_reason: "api_error", result: "fetch failed" }).retryable, true);

  const none = classifyResult({ subtype: "success" });
  assert.equal(none.kind, "no-output");
  assert.equal(none.retryable, false);
});

test("classifyResult: transport errors are retryable — the ECONNRESET regression", () => {
  // The old quota pattern ended in `resets?\b`, which matches "ECONNRESET" at the
  // end-of-string word boundary. Every one of these was classified NOT retryable,
  // so withRetry gave up on the first attempt and the panel reported a false
  // infrastructure failure for an ordinary network blip.
  for (const detail of ["ECONNRESET", "read ECONNRESET", "connection reset by peer", "socket hang up", "fetch failed"]) {
    const c = classifyResult({ is_error: true, result: detail });
    assert.equal(c.retryable, true, `${detail} must be retryable`);
  }
});

test("classifyResult: a plain 429 stays retryable even when it mentions rate limiting", () => {
  // `rate limit` used to be in the non-retryable pattern, directly contradicting
  // the docblock. A rate limit is the canonical retry-with-backoff case.
  for (const detail of ["rate limit exceeded, please retry", "Too Many Requests", "rate_limit_error"]) {
    assert.equal(classifyResult({ is_error: true, api_error_status: 429, result: detail }).retryable, true, detail);
  }
});

test("classifyResult: a session/usage limit overrides an otherwise-retryable 429", () => {
  // The one case that must stay non-retryable: it resets on a fixed schedule, so
  // no amount of in-run backoff clears it. Status alone cannot decide this —
  // both a plain rate limit and a session limit arrive as 429 — which is why the
  // narrow text override survives.
  for (const detail of ["You've hit your session limit · resets 3:30pm (UTC)", "usage limit reached for this month"]) {
    assert.equal(classifyResult({ is_error: true, api_error_status: 429, result: detail }).retryable, false, detail);
  }
});

test("classifyResult: deterministic 4xx is not retried; 5xx is", () => {
  // Keyed on status, not prose. A bad token fails identically three times, so
  // retrying only delays the real error.
  for (const status of [400, 401, 403, 404, 422]) {
    assert.equal(classifyResult({ is_error: true, api_error_status: status, result: "nope" }).retryable, false, `${status}`);
  }
  for (const status of [408, 429, 500, 502, 503, 529]) {
    assert.equal(classifyResult({ is_error: true, api_error_status: status, result: "nope" }).retryable, true, `${status}`);
  }
  // A stringified status must not silently become non-retryable.
  assert.equal(classifyResult({ is_error: true, api_error_status: "529", result: "overloaded" }).retryable, true);
});

test("withRetry: retries retryable errors, honors the cap, never retries non-retryable", async () => {
  const noSleep = { baseMs: 0, sleep: async () => {} };

  let calls = 0;
  const out = await withRetry(async () => {
    calls++;
    if (calls < 3) { const e = new Error("transient"); e.retryable = true; throw e; }
    return "ok";
  }, noSleep);
  assert.equal(out, "ok");
  assert.equal(calls, 3);

  let capped = 0;
  await assert.rejects(withRetry(async () => { capped++; const e = new Error("x"); e.retryable = true; throw e; }, { ...noSleep, retries: 2 }));
  assert.equal(capped, 3, "retries:2 means 3 total attempts, then give up");

  let once = 0;
  await assert.rejects(withRetry(async () => { once++; const e = new Error("quota"); e.retryable = false; throw e; }, noSleep));
  assert.equal(once, 1, "a non-retryable error must not be attempted twice");
});

test("withRetry: backoff grows exponentially from baseMs", async () => {
  // The delay argument was previously unobserved — both suites stubbed sleep with
  // a zero-arg no-op, so a backoff of 0 would have passed.
  const delays = [];
  await assert.rejects(
    withRetry(async () => { const e = new Error("x"); e.retryable = true; throw e; },
      { retries: 3, baseMs: 100, sleep: async (ms) => { delays.push(ms); }, jitter: () => 0 }),
  );
  assert.deepEqual(delays, [100, 200, 400]);
});

test("REGRESSION: a run-limit failure is NOT a retryable API error", () => {
  // Captured verbatim from a live hunt run. This shape cost two already-reproduced
  // findings: `is_error` is set but there is no `api_error_status` and no `result`
  // text, so the old rule filed it under api-error with detail "" -> "unknown" and
  // marked it retryable. Retrying a turn ceiling burns 3x the cost to fail
  // identically, and the log blamed the network for a config problem.
  const maxTurns = {
    type: "result",
    subtype: "error_max_turns",
    is_error: true,
    api_error_status: undefined,
    terminal_reason: "max_turns",
    num_turns: 9,
    result: "",
    errors: [],
  };
  const c = classifyResult(maxTurns);
  assert.equal(c.kind, "limit", "must NOT be classified as api-error");
  assert.equal(c.retryable, false, "retrying a ceiling cannot help");
  assert.equal(c.turns, 9);
  assert.match(c.detail, /error_max_turns/, "the detail must name the real cause, not 'unknown'");
  assert.match(c.detail, /9 turns/, "and say how many turns were spent");
});

test("classifyResult: every deterministic ceiling subtype is non-retryable", () => {
  for (const subtype of ["error_max_turns", "error_max_budget_usd", "error_max_structured_output_retries"]) {
    const c = classifyResult({ subtype, is_error: true });
    assert.equal(c.kind, "limit", subtype);
    assert.equal(c.retryable, false, subtype);
  }
  // `terminal_reason` alone is enough, even if the subtype is unfamiliar.
  assert.equal(classifyResult({ subtype: "error_something_new", is_error: true, terminal_reason: "max_turns" }).kind, "limit");
});

test("classifyResult: a run limit does NOT shadow a genuine API error", () => {
  // The ordering change must not swallow real transient failures — those still
  // need to be retryable, which is the counterweight to the test above.
  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 529, result: "overloaded_error" }).kind, "api-error");
  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 529, result: "overloaded_error" }).retryable, true);
  assert.equal(classifyResult({ terminal_reason: "api_error", result: "fetch failed" }).retryable, true);
  // ...and a session limit stays non-retryable for its own separate reason.
  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 429, result: "You've hit your session limit" }).retryable, false);
});

test("classifyResult: a limit surfaces the SDK's own errors[] when present", () => {
  const c = classifyResult({
    subtype: "error_max_turns", is_error: true, num_turns: 20,
    errors: [{ type: "turn_limit", message: "reached the configured turn ceiling" }],
  });
  assert.match(c.detail, /reached the configured turn ceiling/);
});

test("REGRESSION: structured output from a FAILED session is not a verdict", () => {
  // The fail-open. Testing `subtype === "success" && structured_output` alone
  // accepted output from a session the SDK had flagged as failed — and
  // `subtype: "success"` with `is_error: true` is precisely how this SDK reports an
  // API failure, which is the whole reason classifyResult exists.
  //
  // In review-panel that means a lens's findings from a failed session reach the
  // merge gate. In the issue hunter it means a verifier's "confirmed" from a failed
  // session can cause a report to a real maintainer. A fail-open path is the one
  // thing a fail-quiet gate cannot tolerate.
  const output = { verdict: "confirmed", confidence: "high" };

  const apiErrored = classifyResult({ subtype: "success", structured_output: output, is_error: true, api_error_status: 529, result: "overloaded_error" });
  assert.equal(apiErrored.ok, false, "an api-errored session's output is not a verdict");
  assert.equal(apiErrored.kind, "api-error");

  const statusOnly = classifyResult({ subtype: "success", structured_output: output, api_error_status: 500 });
  assert.equal(statusOnly.ok, false, "an api_error_status alone disqualifies the output");

  const terminal = classifyResult({ subtype: "success", structured_output: output, terminal_reason: "api_error" });
  assert.equal(terminal.ok, false, "terminal_reason api_error disqualifies the output");

  const ceilinged = classifyResult({ subtype: "error_max_turns", structured_output: output, is_error: true, num_turns: 20 });
  assert.equal(ceilinged.ok, false, "partial output from a run that hit its ceiling is not a verdict");
  assert.equal(ceilinged.kind, "limit");
});

test("classifyResult: the fix is strictly tightening — clean successes still pass", () => {
  // The counterweight. A genuinely successful result sets none of the error flags,
  // so no previously-accepted verdict is now rejected.
  const clean = classifyResult({ type: "result", subtype: "success", structured_output: { findings: [] }, is_error: false, terminal_reason: "completed", num_turns: 14 });
  assert.equal(clean.ok, true);
  assert.deepEqual(clean.output, { findings: [] });
});

// --- the invariant that CI depends on ----------------------------------------

test("no module under scripts/agent statically imports a third-party package", () => {
  // `verify-self.mjs`'s `agent:tests` lane runs with `scripts/agent/node_modules`
  // ABSENT — CI never installs it, which is what keeps the lane fast and makes the
  // pure helpers here testable without the SDK. A single static third-party import
  // breaks every test in its file AND every file importing it, with
  // ERR_MODULE_NOT_FOUND rather than an assertion.
  //
  // That invariant was documented in prose at the top of ask.mjs and nothing
  // enforced it, so hunt-tool.mjs was written with `import … from
  // "@anthropic-ai/claude-agent-sdk"` and `import { z } from "zod"`, and CI failed
  // with 377/379 while every local run was green. Prose invariants get broken;
  // tested ones do not. Hence this.
  //
  // The rule: a top-level `import`/`export … from` specifier must be relative
  // (`./`, `../`) or a builtin (`node:`). Third-party dependencies are reachable
  // only through `await import(...)` inside the function that needs them.
  const dir = path.dirname(fileURLToPath(import.meta.url));
  // Recursive: a guard against a future mistake has to cover where a future module
  // might be put, and `node --test *.test.mjs` globbing flatly is exactly the trap
  // that makes a nested module easy to add and easy to miss.
  const walk = (d) =>
    readdirSync(d, { withFileTypes: true }).flatMap((e) => {
      if (e.name === "node_modules" || e.name.startsWith(".")) return [];
      const full = path.join(d, e.name);
      return e.isDirectory() ? walk(full) : e.name.endsWith(".mjs") ? [full] : [];
    });
  const files = walk(dir);
  assert.ok(files.length > 10, "expected to find the agent modules");

  // Two forms, because both load the package: `import x from "pkg"` and the bare
  // side-effect `import "pkg"`. Matching only the first would leave the simpler
  // one — the one someone reaches for to register a polyfill — undetected.
  const WITH_FROM = /^\s*(?:import|export)\b[^;]*?\sfrom\s+["']([^"']+)["']/gm;
  const SIDE_EFFECT = /^\s*import\s+["']([^"']+)["']/gm;
  const isLocal = (spec) => spec.startsWith("./") || spec.startsWith("../") || spec.startsWith("node:");
  const offenders = [];
  for (const file of files) {
    const src = readFileSync(file, "utf8");
    for (const re of [WITH_FROM, SIDE_EFFECT]) {
      for (const m of src.matchAll(re)) {
        if (!isLocal(m[1])) offenders.push(`${path.relative(dir, file)} → ${m[1]}`);
      }
    }
  }
  assert.deepEqual(
    offenders,
    [],
    "static third-party import(s) found; use `await import(...)` inside the function instead:\n  " +
      offenders.join("\n  "),
  );
});

// --- credential safety at the source (docs/tasks/active/20260815-…) -----------

test("classifyResult: `detail` is scrubbed where it is born, not at each renderer", () => {
  // `detail` reaches six published surfaces. A per-surface redaction list is one
  // nobody can keep complete — the incident happened because one renderer was
  // missing from it — so the scrub happens once, here.
  const token = "sk-ant-oat01-AbCdEf1234567890XyZw QqRrSs9876543210AbCd";
  const c = classifyResult({
    subtype: "success",
    is_error: true,
    result: `Header 'Authorization' has invalid value: ${token}`,
  });
  for (const fragment of token.split(/\s+/)) {
    assert.ok(!c.detail.includes(fragment), `raw detail survived: ${c.detail}`);
  }
  assert.equal(c.code, "AUTH_MALFORMED_CREDENTIAL");
  assert.ok(!c.reason.includes("Authorization"), "reason quoted upstream prose");
});

test("classifyResult: scrubbing cannot change a classification", () => {
  // Retryability is decided from the RAW text, before the scrub. If it were decided
  // afterwards, a session limit whose prose got masked would come back RETRYABLE and
  // burn the credential pool retrying a window that cannot reopen inside this run.
  const limit = classifyResult({
    subtype: "success",
    is_error: true,
    api_error_status: 429,
    result: "You've hit your session limit · resets 3:30pm (UTC)",
  });
  assert.equal(limit.retryable, false, "a session limit must stay non-retryable");
  assert.equal(limit.code, "USAGE_LIMIT");
  assert.ok(isAccountLimit(limit), "failover must still recognise a closed window");

  // A plain 429 is still retryable — the distinction the pool depends on.
  const rate = classifyResult({ subtype: "success", is_error: true, api_error_status: 429, result: "rate limit" });
  assert.equal(rate.retryable, true);
  assert.equal(rate.code, "RATE_LIMITED");
  assert.equal(isAccountLimit(rate), false);
});

test("classifyResult: a standardized code on every failure branch", () => {
  assert.equal(classifyResult({ subtype: "error_max_turns", is_error: true, num_turns: 9 }).code, "RUN_LIMIT_TURNS");
  assert.equal(classifyResult({ subtype: "error_max_budget_usd", is_error: true }).code, "RUN_LIMIT_BUDGET");
  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 401 }).code, "AUTH_REJECTED");
  assert.equal(classifyResult({ subtype: "success", is_error: true, api_error_status: 500 }).code, "UPSTREAM_ERROR");
  assert.equal(classifyResult({ subtype: "error" }).code, "NO_OUTPUT");
  // A successful verdict carries no failure vocabulary at all.
  assert.equal(classifyResult({ subtype: "success", structured_output: { findings: [] } }).code, undefined);
});

test("isAccountLimit: prefers the code, falls back to prose for pre-vocabulary callers", () => {
  // `code` is decided before redaction, so it is authoritative.
  assert.ok(isAccountLimit({ kind: "api-error", code: "USAGE_LIMIT", detail: "<REDACTED>" }));
  assert.equal(isAccountLimit({ kind: "api-error", code: "AUTH_REJECTED", detail: "session limit" }), false,
    "a code must win over prose that merely mentions a limit");
  // Bare `{ kind, detail }` — several call sites and every stored record predating
  // the vocabulary look like this.
  assert.ok(isAccountLimit({ kind: "api-error", detail: "You've hit your usage limit" }));
});

test("sessionNeverRan: the kinds where no reviewer ever opened", () => {
  // The two failures that mean no session ran. `pool-exhausted` is the same
  // outage as `api-error` — a closed window or a refused credential — and reaches
  // the panel under a different `kind` only because failover ran out of slots.
  assert.ok(sessionNeverRan({ kind: "api-error" }));
  assert.ok(sessionNeverRan(poolExhaustedError({ label: "review", pool: { size: 2 } })));
});

test("sessionNeverRan: a model that RAN is never infrastructure", () => {
  // The safety property. A run ceiling and an empty verdict both mean the model
  // ran, spent turns and returned nothing usable — there IS a review to re-attempt
  // and a verifier can reason about it, so both stay ordinary fail-closed
  // blockers. Widening this predicate to either of them would silently drop real
  // no-verdict rounds out of the carry-forward, which is the #521 false negative.
  assert.equal(sessionNeverRan({ kind: "limit" }), false);
  assert.equal(sessionNeverRan({ kind: "no-output" }), false);
  // Total on junk: an unrecognised or absent kind is not a licence to drop.
  assert.equal(sessionNeverRan({ kind: "unknown" }), false);
  assert.equal(sessionNeverRan({}), false);
  assert.equal(sessionNeverRan(null), false);
  assert.equal(sessionNeverRan(undefined), false);
});

test("poolExhaustedError: carries the same publishable vocabulary as every other failure", () => {
  const err = poolExhaustedError({ label: "review", pool: { size: 3 } });
  assert.equal(err.kind, "pool-exhausted");
  assert.equal(err.retryable, false);
  // Without a `code`/`reason` this was the one failure reaching a renderer with
  // nothing to publish, so the renderer re-derived one from `detail` — which for a
  // drained pool is empty, and came out as "no response from the API".
  assert.equal(err.code, "POOL_EXHAUSTED");
  assert.match(err.reason, /^\[POOL_EXHAUSTED\]/);
  // Empty BY CONSTRUCTION, not merely redacted: nothing upstream is quoted here.
  assert.equal(err.detail, "");
  // The operator-facing message is unchanged.
  assert.match(err.message, /^review: every credential in the pool \(3\) was retired/);
});
