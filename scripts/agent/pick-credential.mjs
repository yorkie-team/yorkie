// Which pool slot should THIS job's `claude-code-action` step use?
//
// WHY THIS EXISTS. `token-pool.mjs` spreads load across several Claude accounts and
// survives one hitting its usage window — but only for the SDK-driven steps, because
// `claude-code-action` takes a single credential and cannot import a Node module. So
// every action-based step in the pipeline was hardcoded to
// `secrets.CLAUDE_CODE_OAUTH_TOKEN`, which is SLOT ZERO of that same pool
// (`token-pool.mjs::TOKEN_ENV`, "the unsuffixed variable is slot zero"). Five
// workflows shared that pin: agent-implement, agent-fix, agent-iterate-ci,
// agent-review-reply and agent-summarize.
//
// Issue #883 is what that costs. Two `@claude fix` runs, 40 minutes apart, both died
// as `{"is_error": true, "duration_ms": 432, "num_turns": 1, "total_cost_usd": 0}` —
// authenticated, then nothing. In the same hour the review panel's fixer spent 5.7M
// tokens successfully, because #894 had already taught THAT step to read a live slot.
// The pool was healthy; slot zero was not; and `agent-implement` is the entry point of
// the whole issue→PR pipeline, so the pipeline was simply down.
//
// HOW THIS DIFFERS FROM `pick-fix-credential.mjs`. That one reads pool STATE the
// review panel recorded — which slots it retired — and can therefore refuse to spend a
// fix round when every slot is spent. It only works where a panel ran first. These
// jobs have no panel before them and no artifact to read, so this picks by SELECTION
// instead: `createTokenPool` derives a start slot from the run id, which is how two
// jobs land on different accounts without coordinating. Same output contract (`slot`),
// different evidence.
//
// WHAT THIS DOES NOT DO: it cannot tell whether the slot it names is live. Nothing
// short of spending a call can, and a probe would cost the round trip it is trying to
// save. This converts "always the same account" into "spread across the accounts that
// exist", which is what the SDK path has always done. It is a distribution fix, not a
// liveness guarantee — a pool of one dead credential still fails, and that is a
// capacity problem for an operator, not a routing one.
//
// FAIL DIRECTION: always exits 0, and every failure path emits an EMPTY slot, which
// the workflow resolves to the unsuffixed secret — exactly the behaviour these steps
// had before this existed. A broken picker can therefore never be worse than not
// having one.
//
// Usage:
//   node pick-credential.mjs
// Writes `slot=` / `reason=` / `capacity=` to $GITHUB_OUTPUT when set, and always logs.
// Reads the pool from the environment; the caller passes CLAUDE_CODE_OAUTH_TOKEN and
// CLAUDE_CODE_OAUTH_TOKEN_1..8 in `env:`.

import { appendFileSync } from "node:fs";
import { createTokenPool, slotSuffix, MAX_SLOTS, TOKEN_ENV } from "./token-pool.mjs";

/**
 * Choose a slot from a constructed pool. Pure over the pool object, so the decision is
 * testable without an environment.
 *
 * Nothing is ever retired at this point — the pool is fresh — so `currentSlotName()` is
 * simply the run-id-derived start slot. `slotSuffix` re-derives the suffix from the
 * known slot list, so an unrecognised name (which should be impossible from a pool this
 * process just built, and is therefore exactly the case worth refusing) degrades to the
 * ambient credential instead of steering the caller's `secrets[...]` lookup.
 */
export function chooseSlot(pool) {
  if (!pool || typeof pool.currentSlotName !== "function") {
    return { slot: "", reason: "no-pool" };
  }
  if (!pool.size) return { slot: "", reason: "pool-unconfigured" };
  const name = pool.currentSlotName();
  const suffix = slotSuffix(name);
  if (suffix === null) return { slot: "", reason: "unrecognised-slot" };
  return { slot: suffix, reason: "selected" };
}

/**
 * The capacity line an operator can act on. A page or log saying "the session failed"
 * is not actionable; one saying "2 of 9 credential slots are configured" is.
 */
export function capacityLine(pool) {
  const size = pool && Number.isInteger(pool.size) ? pool.size : null;
  if (size === null) return "";
  const total = MAX_SLOTS + 1; // slot zero plus the suffixed ones
  const head = `${size} of ${total} credential slot(s) configured`;
  if (size === 0) return `${head} — the pool is OFF; every agent step shares the ambient credential`;
  if (size <= 2) return `${head} — register more \`${TOKEN_ENV}_N\` secrets so one account cannot pin the pipeline`;
  return head;
}

function main() {
  let pool = null;
  try {
    pool = createTokenPool();
  } catch (err) {
    console.error(`pick-credential: could not read the pool (${String(err?.message ?? err)})`);
  }
  const { slot, reason } = chooseSlot(pool);
  const which = slot === "" ? TOKEN_ENV : `${TOKEN_ENV}_${slot}`;
  console.log(`agent credential: ${which} (reason: ${reason})`);
  const note = capacityLine(pool);
  if (note) console.log(`credential capacity: ${note}`);

  const out = process.env.GITHUB_OUTPUT;
  if (out) appendFileSync(out, `slot=${slot}\nreason=${reason}\ncapacity=${note}\n`);
}

if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  main();
}
