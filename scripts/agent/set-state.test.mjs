import { test } from "node:test";
import assert from "node:assert/strict";
import {
  STATES,
  LIFECYCLE_LABELS,
  labelFor,
  stateFor,
  computeLabelSet,
  sameLabelSet,
  isValidTransition,
  deriveState,
} from "./set-state.mjs";

test("labelFor / stateFor: round-trip; non-lifecycle labels → null", () => {
  assert.equal(labelFor("reviewing"), "agent:reviewing");
  assert.equal(labelFor("nope"), null);
  assert.equal(stateFor("agent:blocked"), "blocked");
  assert.equal(stateFor({ name: "agent:fixing" }), "fixing"); // object-shaped (gh json)
  assert.equal(stateFor("bug"), null);
  assert.equal(stateFor("agent:candidate"), null); // provenance is NOT a lifecycle label
  // every state round-trips
  for (const s of STATES) assert.equal(stateFor(labelFor(s)), s);
});

test("computeLabelSet: swaps the lifecycle label, exactly one out", () => {
  const out = computeLabelSet(["agent:reviewing"], "fixing");
  assert.deepEqual(out, ["agent:fixing"]);
  // exactly one lifecycle label in the result
  assert.equal(out.filter((l) => LIFECYCLE_LABELS.includes(l)).length, 1);
});

test("computeLabelSet: preserves non-agent labels AND agent:candidate; strips legacy labels", () => {
  const out = computeLabelSet(
    ["bug", "enhancement", "agent:candidate", "agent:iterating", "agent:needs-human-review"],
    "reviewing",
  );
  assert.ok(out.includes("bug"));
  assert.ok(out.includes("enhancement"));
  assert.ok(out.includes("agent:candidate")); // provenance survives
  assert.ok(out.includes("agent:reviewing"));
  // clean cutover: pre-cutover labels are stripped so no PR keeps two states
  assert.ok(!out.includes("agent:iterating"));
  assert.ok(!out.includes("agent:needs-human-review"));
});

test("computeLabelSet: accepts object-shaped labels and de-dupes; empty → one label", () => {
  const out = computeLabelSet([{ name: "bug" }, { name: "agent:reviewing" }], "fixing");
  assert.deepEqual(out.sort(), ["agent:fixing", "bug"].sort());
  assert.deepEqual(computeLabelSet([], "implementing"), ["agent:implementing"]);
  // idempotent: applying the same state twice yields the same set
  assert.deepEqual(computeLabelSet(["agent:ready", "bug"], "ready"), computeLabelSet(computeLabelSet(["agent:ready", "bug"], "ready"), "ready"));
});

test("computeLabelSet: collapses multiple stray lifecycle labels (drift) to one", () => {
  const out = computeLabelSet(["agent:reviewing", "agent:fixing", "agent:blocked", "bug"], "ready");
  assert.deepEqual(out.filter((l) => LIFECYCLE_LABELS.includes(l)), ["agent:ready"]);
  assert.ok(out.includes("bug"));
});

test("computeLabelSet: unknown state throws (wiring bug, not silenced)", () => {
  assert.throws(() => computeLabelSet(["bug"], "bogus"));
});

test("isValidTransition: first assignment + representative legal edges", () => {
  assert.equal(isValidTransition(null, "implementing"), true); // no prior state
  assert.equal(isValidTransition(undefined, "reviewing"), true);
  assert.equal(isValidTransition("implementing", "fixing"), true);
  assert.equal(isValidTransition("fixing", "reviewing"), true);
  assert.equal(isValidTransition("reviewing", "ready"), true);
  assert.equal(isValidTransition("ready", "blocked"), true);
  assert.equal(isValidTransition("reviewing", "reviewing"), true); // self-assert
});

test("isValidTransition: illegal edges are refused", () => {
  assert.equal(isValidTransition("ready", "fixing"), false); // needs an intervening push
  assert.equal(isValidTransition("blocked", "reviewing"), false); // terminal without --force
  assert.equal(isValidTransition("blocked", "ready"), false);
  assert.equal(isValidTransition("reviewing", "bogus"), false); // unknown target
});

test("deriveState: paged latch dominates everything", () => {
  assert.equal(deriveState({ ciPagedLatch: true, isDraft: false, lensBlocked: true }), "blocked");
  assert.equal(deriveState({ reviewPagedLatch: true, ciConclusion: "success" }), "blocked");
});

test("deriveState: draft→ready flip beats lens/CI signals", () => {
  assert.equal(deriveState({ isDraft: false, ciConclusion: "success" }), "ready");
  assert.equal(deriveState({ isDraft: false, lensBlocked: true }), "ready"); // precedence: ready over fixing
});

test("deriveState: lensBlocked → fixing; review checks / green CI → reviewing", () => {
  assert.equal(deriveState({ isDraft: true, lensBlocked: true }), "fixing");
  assert.equal(deriveState({ isDraft: true, reviewChecksPresent: true }), "reviewing");
  assert.equal(deriveState({ isDraft: true, ciConclusion: "success" }), "reviewing");
});

test("deriveState: draft + CI pending/failed → awaiting-ci; empty → implementing", () => {
  assert.equal(deriveState({ isDraft: true, ciConclusion: null }), "awaiting-ci");
  assert.equal(deriveState({ isDraft: true, ciConclusion: "failure" }), "awaiting-ci");
  assert.equal(deriveState({}), "implementing"); // no signals at all
});

test("sameLabelSet: order-independent set equality; shape-agnostic", () => {
  assert.ok(sameLabelSet(["agent:fixing", "bug"], ["bug", "agent:fixing"]));
  assert.ok(sameLabelSet([{ name: "bug" }, "agent:ready"], ["agent:ready", "bug"]));
  assert.ok(!sameLabelSet(["agent:fixing"], ["agent:fixing", "bug"])); // size differs
  assert.ok(!sameLabelSet(["agent:reviewing"], ["agent:fixing"]));
  assert.ok(sameLabelSet([], []));
});

// Reconcile drift-collapse: when a PR carries the derived state AND a stray
// second lifecycle label, the desired (normalized) set differs from current, so
// reconcile must act — a first-label-equality check would wrongly skip it.
test("reconcile guard: desired set differs from a drifted current → not skipped", () => {
  const current = ["agent:fixing", "agent:reviewing", "bug"]; // drift: two lifecycle labels
  const desired = computeLabelSet(current, "fixing"); // → ["bug", "agent:fixing"]
  assert.ok(!sameLabelSet(current, desired)); // reconcile applies (collapses the stray)
  // once normalized, reconcile is a no-op
  assert.ok(sameLabelSet(desired, computeLabelSet(desired, "fixing")));
});
