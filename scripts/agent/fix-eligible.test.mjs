import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyLensRuns, decideEligibility, readPrHead } from "./fix-eligible.mjs";

const NAMES = ["agent-review-correctness", "agent-review-security"];
const HEAD = "abcdef1234567890";

const run = (name, over = {}) => ({
  name,
  app: { slug: "github-actions" },
  status: "completed",
  conclusion: "success",
  started_at: "2026-01-01T00:00:00Z",
  ...over,
});

// --- classifyLensRuns -------------------------------------------------------

test("classifyLensRuns: counts completed, collects pending and failing", () => {
  const c = classifyLensRuns([
    run("agent-review-correctness", { conclusion: "failure" }),
    run("agent-review-security", { status: "in_progress", conclusion: null }),
  ], NAMES);
  assert.deepEqual(c, { total: 2, completed: 1, pending: ["agent-review-security"], failing: ["agent-review-correctness"] });
});

test("classifyLensRuns: a run from another app is invisible", () => {
  // Otherwise any integration could create a check named `agent-review-security`
  // and manufacture a panel verdict — and therefore an eligibility — from nothing.
  assert.equal(classifyLensRuns([run("agent-review-security", { app: { slug: "evil" } })], NAMES).total, 0);
});

test("classifyLensRuns: latest run per lens wins", () => {
  const c = classifyLensRuns([
    run("agent-review-correctness", { conclusion: "failure", started_at: "2026-01-01T00:00:00Z" }),
    run("agent-review-correctness", { conclusion: "success", started_at: "2026-01-02T00:00:00Z" }),
  ], NAMES);
  assert.deepEqual(c.failing, []);
  assert.equal(c.completed, 1);
});

// --- decideEligibility ------------------------------------------------------

test("eligible: a completed panel with at least one failing lens on the current head", () => {
  const d = decideEligibility({
    pr: "7", isFork: false, head: HEAD, names: NAMES,
    runs: [run("agent-review-correctness", { conclusion: "failure" }), run("agent-review-security")],
  });
  assert.equal(d.eligible, true);
  assert.deepEqual(d.failing, ["agent-review-correctness"]);
  assert.match(d.reason, /abcdef12/);
});

test("REQUIREMENT: a commit after the panel makes it ineligible", () => {
  // The panel's lens runs are attested against the sha it reviewed. A commit moves
  // the head, so the new head carries no lens runs — which is simultaneously "no
  // panel ran for this code" and "something landed after the last review".
  const d = decideEligibility({ pr: "7", isFork: false, head: "newsha00", names: NAMES, runs: [] });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /No review-panel verdict for the current head commit/);
  // It must NOT send them to `@claude review`: that verb is advisory and records
  // no check runs, so following it could never make the PR eligible. The two real
  // answers are "wait for the next panel round" and "opt the PR into the loop".
  assert.match(d.reason, /wait for CI to finish/);
  assert.match(d.reason, /@claude loop/);
  assert.match(d.reason, /advisory and posts no check runs/);
});

test("REQUIREMENT: no previous panel run at all is the same refusal", () => {
  const d = decideEligibility({ pr: "7", isFork: false, head: HEAD, names: NAMES, runs: [run("codecov/patch")] });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /No review-panel verdict/);
});

test("ineligible: a panel still running is a wait, not a refusal to ever run", () => {
  const d = decideEligibility({
    pr: "7", isFork: false, head: HEAD, names: NAMES,
    runs: [run("agent-review-correctness", { status: "in_progress", conclusion: null })],
  });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /still running/);
  assert.match(d.reason, /agent-review-correctness/);
});

test("ineligible: no lens is requesting changes — there is nothing to fix", () => {
  const d = decideEligibility({
    pr: "7", isFork: false, head: HEAD, names: NAMES,
    runs: [run("agent-review-correctness"), run("agent-review-security")],
  });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /No lens is requesting changes/);
  // NOT "passed all N lenses": `completed` counts every CONCLUDED run, and a lens
  // that does not apply to the diff concludes `neutral`, not `success`. The old
  // wording asserted a pass that never happened.
  assert.equal(/passed all/.test(d.reason), false);
  const withNeutral = decideEligibility({
    pr: "7", isFork: false, head: HEAD, names: NAMES,
    runs: [run("agent-review-correctness"), run("agent-review-security", { conclusion: "neutral" })],
  });
  assert.equal(withNeutral.eligible, false);
  assert.match(withNeutral.reason, /2 concluded/);
});

test("ineligible: a fork, because the app cannot push there", () => {
  const d = decideEligibility({
    pr: "7", isFork: true, head: HEAD, names: NAMES,
    runs: [run("agent-review-correctness", { conclusion: "failure" })],
  });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /fork/);
});

test("FAIL DIRECTION: an unknown head refuses, even with failing lenses in hand", () => {
  const d = decideEligibility({
    pr: "7", isFork: false, head: "", names: NAMES,
    runs: [run("agent-review-correctness", { conclusion: "failure" })],
  });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /Could not determine/);
});

test("an API failure is reported as an unknown head, NOT as a fork", () => {
  // readPrHead reports a failed lookup as {head:"", isFork:true} — fork is the
  // safe default for unknown provenance. Checking isFork first therefore claimed
  // "this is a fork" whenever the API merely failed, sending a maintainer to look
  // for a fork problem on a same-repo PR. Both refuse; only one reason is true.
  const d = decideEligibility({ pr: "7", isFork: true, head: "", names: NAMES, runs: [] });
  assert.equal(d.eligible, false);
  assert.match(d.reason, /Could not determine this PR's head commit/);
  assert.equal(/fork/.test(d.reason), false);
  // A real fork — head known, different repo — still gets the fork message.
  const f = decideEligibility({ pr: "7", isFork: true, head: HEAD, names: NAMES, runs: [] });
  assert.match(f.reason, /cannot do on a fork/);
});

test("FAIL DIRECTION: every refusal reports failing as an empty array, never undefined", () => {
  // The workflow joins this into a step output; `undefined.join` would red the
  // gate step, turning a clean refusal into a broken-looking workflow.
  for (const args of [
    { isFork: true, head: HEAD, runs: [] },
    { isFork: false, head: "", runs: [] },
    { isFork: false, head: HEAD, runs: [] },
    { isFork: false, head: HEAD, runs: [run("agent-review-correctness", { status: "queued" })] },
    { isFork: false, head: HEAD, runs: [run("agent-review-correctness")] },
  ]) {
    const d = decideEligibility({ pr: "7", names: NAMES, ...args });
    assert.equal(d.eligible, false);
    assert.deepEqual(d.failing, []);
    assert.equal(typeof d.reason, "string");
    assert.ok(d.reason.length > 0);
    assert.equal(d.reason.includes("\n"), false); // written as a single output line
  }
});

// --- readPrHead -------------------------------------------------------------

test("readPrHead: same-repo PR", () => {
  const api = () => ({ head: { sha: HEAD, repo: { full_name: "wafflebase/wafflebase" } } });
  assert.deepEqual(readPrHead("7", { api, repo: "wafflebase/wafflebase" }), { head: HEAD, isFork: false });
});

test("readPrHead: a fork, and a DELETED fork, both read as fork", () => {
  const fork = () => ({ head: { sha: HEAD, repo: { full_name: "someone/wafflebase" } } });
  assert.equal(readPrHead("7", { api: fork, repo: "wafflebase/wafflebase" }).isFork, true);
  // No head repo at all: unknown provenance must never resolve to "same repo".
  const deleted = () => ({ head: { sha: HEAD, repo: null } });
  assert.equal(readPrHead("7", { api: deleted, repo: "wafflebase/wafflebase" }).isFork, true);
});

test("FAIL DIRECTION: an unknown GITHUB_REPOSITORY refuses rather than assuming same-repo", () => {
  // With nothing to compare against, provenance is unproven — and unproven must
  // not resolve to "pushable". Skipping the comparison here would make every PR
  // look same-repo the moment the env var went missing.
  const api = () => ({ head: { sha: HEAD, repo: { full_name: "wafflebase/wafflebase" } } });
  assert.equal(readPrHead("7", { api, repo: "" }).isFork, true);
  // Control: the SAME response with the repo known is not a fork, so the refusal
  // above is the missing comparand and not a matcher that always says yes.
  assert.equal(readPrHead("7", { api, repo: "wafflebase/wafflebase" }).isFork, false);

  // The PRODUCTION path — `repo` omitted so the default parameter reads the
  // environment. Passing `repo: undefined` does NOT test this: a default parameter
  // substitutes for `undefined`, so that call silently became "whatever
  // GITHUB_REPOSITORY happens to be" — green locally where it is unset, red in
  // Actions where it is `wafflebase/wafflebase`. Clear it explicitly instead, and
  // restore it, so the assertion means the same thing in both places.
  const saved = process.env.GITHUB_REPOSITORY;
  try {
    delete process.env.GITHUB_REPOSITORY;
    assert.equal(readPrHead("7", { api }).isFork, true);
  } finally {
    if (saved === undefined) delete process.env.GITHUB_REPOSITORY;
    else process.env.GITHUB_REPOSITORY = saved;
  }
});

test("FAIL DIRECTION: an API error refuses rather than assuming same-repo", () => {
  const boom = () => { throw new Error("500"); };
  assert.deepEqual(readPrHead("7", { api: boom, repo: "wafflebase/wafflebase", log: () => {} }), { head: "", isFork: true });
});
