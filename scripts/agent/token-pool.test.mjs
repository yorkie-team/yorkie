import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MAX_SLOTS,
  readPoolSlots,
  readPoolTokens,
  selectStartIndex,
  shardOffset,
  createTokenPool,
} from "./token-pool.mjs";

/** Env with every pool variable cleared, so a stray real token can't leak in. */
function env(overrides = {}) {
  const base = { CLAUDE_CODE_OAUTH_TOKEN: "" };
  for (let i = 1; i <= MAX_SLOTS; i++) base[`CLAUDE_CODE_OAUTH_TOKEN_${i}`] = "";
  return { ...base, ...overrides };
}

test("readPoolTokens: the unsuffixed variable is slot zero", () => {
  assert.deepEqual(readPoolTokens(env({ CLAUDE_CODE_OAUTH_TOKEN: "a" })), ["a"]);
});

test("readPoolTokens: suffixed slots follow, in declared order", () => {
  const tokens = readPoolTokens(
    env({ CLAUDE_CODE_OAUTH_TOKEN: "a", CLAUDE_CODE_OAUTH_TOKEN_1: "b", CLAUDE_CODE_OAUTH_TOKEN_2: "c" }),
  );
  assert.deepEqual(tokens, ["a", "b", "c"]);
});

test("readPoolTokens: empty and whitespace-only slots are holes, not members", () => {
  // Every workflow passes all MAX_SLOTS variables; unregistered secrets arrive
  // as "". A hole must not shift the slots after it out of position, and must
  // not become a member that authenticates as nothing.
  const tokens = readPoolTokens(env({ CLAUDE_CODE_OAUTH_TOKEN_1: "b", CLAUDE_CODE_OAUTH_TOKEN_3: "d" }));
  assert.deepEqual(tokens, ["b", "d"]);
  assert.deepEqual(readPoolTokens(env({ CLAUDE_CODE_OAUTH_TOKEN_1: "   " })), []);
});

test("readPoolTokens: a token repeated across slots counts once", () => {
  // The migration state: the unsuffixed secret is kept AND copied into _1.
  // Counting it twice would make failover 'switch' to the same dead account.
  const tokens = readPoolTokens(env({ CLAUDE_CODE_OAUTH_TOKEN: "a", CLAUDE_CODE_OAUTH_TOKEN_1: "a" }));
  assert.deepEqual(tokens, ["a"]);
});

test("readPoolTokens: surrounding whitespace is stripped", () => {
  assert.deepEqual(readPoolTokens(env({ CLAUDE_CODE_OAUTH_TOKEN_1: " a\n" })), ["a"]);
});

test("readPoolTokens: slots past MAX_SLOTS are ignored", () => {
  const beyond = MAX_SLOTS + 1;
  assert.deepEqual(readPoolTokens(env({ [`CLAUDE_CODE_OAUTH_TOKEN_${beyond}`]: "x" })), []);
});

test("readPoolSlots: each token carries the secret name that supplied it", () => {
  // Holes and dedup mean a position in the pool is NOT its suffix, so a
  // diagnostic that says "slot 2 is bad" would send an operator to the wrong
  // secret. The name is the only actionable identifier — and the only one that
  // can be printed, since the token itself never can.
  const slots = readPoolSlots(env({ CLAUDE_CODE_OAUTH_TOKEN_2: "b", CLAUDE_CODE_OAUTH_TOKEN_5: "e" }));
  assert.deepEqual(slots, [
    { name: "CLAUDE_CODE_OAUTH_TOKEN_2", token: "b" },
    { name: "CLAUDE_CODE_OAUTH_TOKEN_5", token: "e" },
  ]);
});

test("selectStartIndex: consecutive runs land on different tokens", () => {
  const picks = [101, 102, 103, 104].map((runId) => selectStartIndex(4, { runId: String(runId), runAttempt: "1" }));
  assert.deepEqual(picks, [1, 2, 3, 0]);
});

test("selectStartIndex: a re-run does not re-pick the token that just died", () => {
  const first = selectStartIndex(4, { runId: "100", runAttempt: "1" });
  const second = selectStartIndex(4, { runId: "100", runAttempt: "2" });
  assert.notEqual(first, second);
});

test("selectStartIndex: a missing or unparseable run id is index zero, not NaN", () => {
  // Local runs and the `act` harness have no GITHUB_RUN_ID. NaN % n is NaN,
  // which would index the pool to `undefined` and authenticate as nothing.
  for (const runId of [undefined, "", "not-a-number"]) {
    assert.equal(selectStartIndex(4, { runId, runAttempt: undefined }), 0);
  }
});

test("selectStartIndex: a run id beyond MAX_SAFE_INTEGER still lands in range", () => {
  const index = selectStartIndex(4, { runId: "9".repeat(25), runAttempt: "1" });
  assert.ok(Number.isInteger(index) && index >= 0 && index < 4, `got ${index}`);
});

test("selectStartIndex: an empty pool is index zero rather than a divide by zero", () => {
  assert.equal(selectStartIndex(0, { runId: "7", runAttempt: "1" }), 0);
});

test("selectStartIndex: matrix siblings sharing one run id land on different tokens", () => {
  // GITHUB_RUN_ID identifies the RUN, not the job, so every leg of eval-replay's
  // matrix shares it. Without a per-leg shard the pool would deliver that lane
  // nothing: all legs on one credential, which is the exact contention its
  // `max-parallel: 1` exists to avoid.
  const legs = ["r1", "r2", "r3", "r4"].map((shard) =>
    selectStartIndex(4, { runId: "500", runAttempt: "1", shard }),
  );
  assert.ok(new Set(legs).size > 1, `all legs collided on one token: ${legs.join(",")}`);
});

test("selectStartIndex: no shard leaves selection exactly as it was", () => {
  // Every workflow but eval-replay omits it; none of them may shift.
  for (const shard of [undefined, null, ""]) {
    assert.equal(
      selectStartIndex(4, { runId: "101", runAttempt: "1", shard }),
      selectStartIndex(4, { runId: "101", runAttempt: "1" }),
    );
  }
});

test("shardOffset: stable, non-negative, and integral for opaque leg ids", () => {
  // Leg identifiers are strings, not numbers — a digit parse would map "r1" and
  // "r2" both to 0 and collide exactly where the shard is needed.
  assert.equal(shardOffset("2026-08-14-r1"), shardOffset("2026-08-14-r1"));
  assert.notEqual(shardOffset("r1"), shardOffset("r2"));
  for (const s of ["", "r1", "a".repeat(500), "💥"]) {
    const n = shardOffset(s);
    assert.ok(Number.isInteger(n) && n >= 0, `${JSON.stringify(s)} → ${n}`);
  }
});

test("createTokenPool: isExhausted separates a drained pool from an unconfigured one", () => {
  // The two states both make current() null and must never be confused: an
  // unconfigured pool falls back to ambient credential resolution, a drained one
  // must fail. Falling back on a drained pool re-uses slot zero — a credential
  // the pool already retired.
  const unconfigured = createTokenPool({ env: env() });
  assert.equal(unconfigured.current(), null);
  assert.equal(unconfigured.isExhausted(), false, "an unconfigured pool has not been used up");

  const pool = createTokenPool({ env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b" }) });
  assert.equal(pool.isExhausted(), false);
  pool.advance("limit");
  assert.equal(pool.isExhausted(), false, "one token left");
  pool.advance("limit");
  assert.equal(pool.isExhausted(), true);
  assert.equal(pool.current(), null);
});

test("createTokenPool: a single token behaves exactly as before the pool existed", () => {
  const pool = createTokenPool({ env: env({ CLAUDE_CODE_OAUTH_TOKEN: "solo" }) });
  assert.equal(pool.size, 1);
  assert.equal(pool.current(), "solo");
  assert.equal(pool.advance("session limit"), null); // nothing to fall back to
});

test("createTokenPool: an empty pool reports no token instead of throwing", () => {
  // The SDK's own 'no credentials' error is a better diagnostic than ours, so
  // an unconfigured environment must reach it rather than dying here.
  const pool = createTokenPool({ env: env() });
  assert.equal(pool.size, 0);
  assert.equal(pool.current(), null);
});

test("createTokenPool: current() is stable across calls so one job keeps one cache", () => {
  // The whole reason distribution is per-job: prompt caches are per-account,
  // and review-panel's warmup gate pays for the shared prefix exactly once.
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b", CLAUDE_CODE_OAUTH_TOKEN_3: "c" }),
    runId: "1",
    runAttempt: "1",
  });
  const first = pool.current();
  for (let i = 0; i < 20; i++) assert.equal(pool.current(), first);
});

test("createTokenPool: advance() hands out every other token before giving up", () => {
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b", CLAUDE_CODE_OAUTH_TOKEN_3: "c" }),
    runId: "0",
    runAttempt: "1",
  });
  const seen = [pool.current()];
  for (let next = pool.advance("limit"); next !== null; next = pool.advance("limit")) seen.push(next);
  assert.deepEqual([...seen].sort(), ["a", "b", "c"]);
  assert.equal(pool.advance("limit"), null, "a dry pool stays dry");
});

test("createTokenPool: advance() wraps past the start index", () => {
  // Starting at the last slot must still reach the earlier ones — the failover
  // order is a rotation, not a suffix.
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b" }),
    runId: "1", // size 2 → starts at index 1 ("b")
    runAttempt: "1",
  });
  assert.equal(pool.current(), "b");
  assert.equal(pool.advance("limit"), "a");
});

test("createTokenPool: current() reflects the token advance() returned", () => {
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b" }),
    runId: "0",
    runAttempt: "1",
  });
  const next = pool.advance("limit");
  assert.equal(pool.current(), next);
});

test("createTokenPool: advancing off a token already retired is a no-op", () => {
  // Concurrent lenses in one panel round discover the same exhaustion. The
  // second report must not burn a second, healthy token.
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b", CLAUDE_CODE_OAUTH_TOKEN_3: "c" }),
    runId: "0",
    runAttempt: "1",
  });
  const dead = pool.current();
  const next = pool.advance("limit", dead);
  assert.equal(pool.advance("limit", dead), next, "same dead token reported twice");
  assert.equal(pool.current(), next);
});

test("createTokenPool: retired tokens are reported for the run summary", () => {
  const pool = createTokenPool({
    env: env({ CLAUDE_CODE_OAUTH_TOKEN_1: "a", CLAUDE_CODE_OAUTH_TOKEN_2: "b" }),
    runId: "0",
    runAttempt: "1",
  });
  assert.equal(pool.retiredCount(), 0);
  pool.advance("session limit");
  assert.equal(pool.retiredCount(), 1);
});

// --- slot NAMES: the cross-job hand-off ---------------------------------------

test("liveSlotNames/retiredSlotNames: names, never tokens, and they partition the pool", () => {
  // The fix job cannot consult this module (it runs `claude-code-action`), so the
  // panel reports which SECRETS are still usable and the fixer resolves the name
  // through the `secrets` context. Names only: the report travels in a workflow
  // artifact, which is not a credential store.
  const env = {
    GITHUB_RUN_ID: "11",
    CLAUDE_CODE_OAUTH_TOKEN: "tok-zero",
    CLAUDE_CODE_OAUTH_TOKEN_1: "tok-one",
    CLAUDE_CODE_OAUTH_TOKEN_2: "tok-two",
  };
  const pool = createTokenPool({ env });
  const all = pool.slotNames();
  assert.deepEqual(all, ["CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_1", "CLAUDE_CODE_OAUTH_TOKEN_2"]);
  assert.deepEqual(pool.liveSlotNames(), all);
  assert.deepEqual(pool.retiredSlotNames(), []);

  // No token value ever appears in either list.
  for (const name of [...pool.liveSlotNames(), ...pool.retiredSlotNames(), ...all]) {
    assert.ok(!name.startsWith("tok-"), `leaked a token value: ${name}`);
  }

  // Retiring moves exactly one name across, and the two lists stay a partition.
  const dead = pool.current();
  pool.advance("closed window", dead);
  assert.equal(pool.retiredSlotNames().length, 1);
  assert.equal(pool.liveSlotNames().length, 2);
  assert.deepEqual(
    [...pool.liveSlotNames(), ...pool.retiredSlotNames()].sort(),
    [...all].sort(),
    "every slot must be in exactly one list",
  );
});

test("liveSlotNames: a fully drained pool reports NO live slots", () => {
  // What makes the fixer's refusal safe: an empty `live` list with a non-zero size
  // is the one state that means "do not spend a fix round".
  const env = { GITHUB_RUN_ID: "3", CLAUDE_CODE_OAUTH_TOKEN: "a", CLAUDE_CODE_OAUTH_TOKEN_1: "b" };
  const pool = createTokenPool({ env });
  assert.equal(pool.size, 2);
  while (pool.current() !== null) pool.advance("drain", pool.current());
  assert.deepEqual(pool.liveSlotNames(), []);
  assert.equal(pool.retiredSlotNames().length, 2);
  assert.equal(pool.isExhausted(), true);
});

test("slotNames: an unconfigured pool has no slots and is not exhausted", () => {
  // `size === 0` must stay distinguishable from a drained pool — the fixer treats
  // the two oppositely.
  const pool = createTokenPool({ env: { GITHUB_RUN_ID: "1" } });
  assert.equal(pool.size, 0);
  assert.deepEqual(pool.slotNames(), []);
  assert.deepEqual(pool.liveSlotNames(), []);
  assert.equal(pool.isExhausted(), false, "an absent pool is not a spent one");
});

test("slotNames: a duplicated secret is ONE slot, so the report cannot promise a false failover", () => {
  // The migration state (unsuffixed secret also copied into `_1`) dedupes to one
  // token. If the report listed both names the fixer could be handed the very
  // credential the panel retired, under a different name.
  const env = { GITHUB_RUN_ID: "5", CLAUDE_CODE_OAUTH_TOKEN: "same", CLAUDE_CODE_OAUTH_TOKEN_1: "same" };
  const pool = createTokenPool({ env });
  assert.equal(pool.size, 1);
  assert.deepEqual(pool.slotNames(), ["CLAUDE_CODE_OAUTH_TOKEN"]);
});
