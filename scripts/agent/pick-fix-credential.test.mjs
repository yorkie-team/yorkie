import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { slotSuffix, chooseCredential, readPoolState, capacityNote } from "./pick-fix-credential.mjs";
import { MAX_SLOTS, TOKEN_ENV } from "./token-pool.mjs";

// --- slotSuffix: the name→suffix map, and the security boundary ---------------

test("slotSuffix: slot zero is the empty suffix, `_N` maps to N", () => {
  assert.equal(slotSuffix(TOKEN_ENV), "");
  for (let i = 1; i <= MAX_SLOTS; i++) {
    assert.equal(slotSuffix(`${TOKEN_ENV}_${i}`), String(i));
  }
});

test("slotSuffix: a name outside this pool is refused, not forwarded", () => {
  // Load-bearing for more than tidiness. The caller interpolates this into a
  // `secrets[...]` lookup in the workflow, so a name arriving from a malformed or
  // tampered artifact must not be able to aim that lookup at a different secret.
  // The suffix is re-derived from the KNOWN slot list rather than trusted.
  for (const bad of [
    "GITHUB_TOKEN",
    "CLAUDE_CODE_OAUTH_TOKEN_0", // slot zero is the UNSUFFIXED name, not `_0`
    `${TOKEN_ENV}_${MAX_SLOTS + 1}`, // past the ceiling
    `${TOKEN_ENV}_1x`,
    `${TOKEN_ENV}_`,
    "claude_code_oauth_token_1", // case matters
    "", null, undefined, 3, {},
  ]) {
    assert.equal(slotSuffix(bad), null, JSON.stringify(bad));
  }
});

// --- chooseCredential: the two DIFFERENT fail directions ---------------------

test("chooseCredential: a live slot is named, first in slot order", () => {
  const state = { v: 1, size: 3, live: [`${TOKEN_ENV}_2`, `${TOKEN_ENV}_3`], retired: [TOKEN_ENV] };
  assert.deepEqual(chooseCredential(state), { slot: "2", available: true, reason: "live-slot" });
});

test("chooseCredential: a KNOWN-drained pool refuses to spend a round", () => {
  // The fail-CLOSED half. Here we know the fixer cannot start, and dispatching
  // anyway is what cost #876 a fix round on a session that died 1.6s after init.
  const state = { v: 1, size: 2, live: [], retired: [TOKEN_ENV, `${TOKEN_ENV}_1`] };
  assert.deepEqual(chooseCredential(state), { slot: "", available: false, reason: "all-slots-retired" });
});

test("chooseCredential: an unreadable, absent OR self-contradictory state PROCEEDS", () => {
  // The fail-OPEN half, and the reason the two directions differ: not knowing
  // whether the fixer would work is not a reason to skip a fix that might. Every
  // one of these is today's behaviour — the unsuffixed secret, fixer dispatched.
  for (const state of [
    null, undefined, 42, "nope", {},
    { v: 1, size: 2 },                                   // no `live` at all
    { v: 1, size: 2, live: "not-an-array" },
    { size: 2, live: [], retired: [TOKEN_ENV, `${TOKEN_ENV}_1`] },  // no version
    { v: 99, size: 2, live: [], retired: [TOKEN_ENV, `${TOKEN_ENV}_1`] }, // future version
    { v: 1, size: "banana", live: [], retired: [TOKEN_ENV] },        // size not a number
    { v: 1, size: 2.5, live: [], retired: [TOKEN_ENV] },             // size not an integer
    { v: 1, size: -1, live: [], retired: [] },                       // size negative
    { v: 1, size: 99, live: [], retired: [] },                       // size past the ceiling
    { v: 1, size: 2, live: [], retired: [TOKEN_ENV] },               // retired count < size
    { v: 1, size: 2, live: [], retired: [TOKEN_ENV, TOKEN_ENV] },    // retired duplicated
    { v: 1, size: 2, live: [], retired: [TOKEN_ENV, "GITHUB_TOKEN"] }, // retired unrecognised
    { v: 1, size: 2, live: [], retired: "not-an-array" },
    { v: 1, size: 2, live: [] },                                     // no retired at all
  ]) {
    const got = chooseCredential(state);
    assert.equal(got.available, true, JSON.stringify(state));
    assert.equal(got.slot, "", JSON.stringify(state));
  }
});

test("chooseCredential: an UNCONFIGURED pool proceeds on the ambient credential", () => {
  // `size: 0` is not a drained pool — it is a repo with no pool at all, which is a
  // supported configuration. Reading it as "drained" would refuse to ever dispatch
  // a fixer on such a repo.
  const state = { v: 1, size: 0, live: [], retired: [] };
  assert.deepEqual(chooseCredential(state), { slot: "", available: true, reason: "pool-unconfigured" });
});

test("chooseCredential: an unrecognised live list fails OPEN, an empty one fails closed", () => {
  // The distinction that matters. `live: []` with slots configured is the panel
  // saying it retired everything — evidence, so fail closed. A NON-empty list whose
  // names we do not recognise is a malformed artifact — no evidence either way, so
  // fail open. Collapsing the two would block every fixer the moment the artifact
  // shape drifted.
  const foreign = { v: 1, size: 2, live: ["GITHUB_TOKEN", `${TOKEN_ENV}_99`], retired: [] };
  assert.deepEqual(chooseCredential(foreign), { slot: "", available: true, reason: "unrecognised-slots" });
  const empty = { v: 1, size: 2, live: [], retired: [TOKEN_ENV, `${TOKEN_ENV}_1`] };
  assert.equal(chooseCredential(empty).available, false);
  // Mixed: one foreign, one real → the real one wins.
  assert.deepEqual(
    chooseCredential({ v: 1, size: 2, live: ["GITHUB_TOKEN", `${TOKEN_ENV}_4`], retired: [] }),
    { slot: "4", available: true, reason: "live-slot" },
  );
});

test("chooseCredential: slot zero being the only live slot is expressible", () => {
  // The empty suffix is a real answer, not a missing one — the workflow's
  // `slot != ''` fallback lands on the same secret either way, but the reason must
  // read `live-slot` rather than a degraded path.
  assert.deepEqual(
    chooseCredential({ v: 1, size: 2, live: [TOKEN_ENV], retired: [`${TOKEN_ENV}_1`] }),
    { slot: "", available: true, reason: "live-slot" },
  );
});

// --- readPoolState -----------------------------------------------------------

test("readPoolState: reads a directory or an explicit file, and never throws", (t) => {
  const dir = mkdtempSync(path.join(tmpdir(), "poolstate-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, "review-pool-state.json");
  writeFileSync(file, JSON.stringify({ v: 1, size: 1, live: [TOKEN_ENV] }));

  assert.equal(readPoolState(dir).size, 1, "directory form");
  assert.equal(readPoolState(file).size, 1, "explicit file form");

  writeFileSync(file, "{not json");
  assert.equal(readPoolState(dir), null, "malformed JSON is null, not a throw");

  assert.equal(readPoolState(path.join(dir, "absent.json")), null);
  assert.equal(readPoolState(path.join(dir, "no-such-dir")), null);
  assert.equal(readPoolState(""), null);
  assert.equal(readPoolState(null), null);
});

// --- capacityNote ------------------------------------------------------------

test("capacityNote: a small pool tells the operator to register more", () => {
  const note = capacityNote({ v: 1, size: 2, maxSlots: 8, retired: ["a", "b"] });
  assert.match(note, /2 of 9 credential slot\(s\) configured/);
  assert.match(note, /2 retired this round/);
  assert.match(note, /register more/);
});

test("capacityNote: an unconfigured pool says the pool is off, not that it is small", () => {
  const note = capacityNote({ v: 1, size: 0, maxSlots: 8, retired: [] });
  assert.match(note, /pool is OFF/);
  assert.doesNotMatch(note, /register more/);
});

test("capacityNote: a healthy pool reports without nagging", () => {
  const note = capacityNote({ v: 1, size: 5, maxSlots: 8, retired: [] });
  assert.match(note, /5 of 9/);
  assert.doesNotMatch(note, /register more/);
  assert.doesNotMatch(note, /OFF/);
});

test("capacityNote: junk yields an empty note rather than a wrong number", () => {
  for (const bad of [null, undefined, {}, { v: 1, size: -1 }, { v: 1, size: "two" }]) {
    assert.equal(capacityNote(bad), "", JSON.stringify(bad));
  }
});

// --- workflow wiring ---------------------------------------------------------

test("the fix job picks a credential BEFORE recording the round, and gates on it", () => {
  const wf = readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), "../../.github/workflows/agent-review-panel.yml"),
    "utf8",
  );
  // The picker runs from the STAGED trusted copy. The first version of this test
  // asserted `.trusted/scripts/agent/...` and passed — while the fix job has no
  // `.trusted` directory at all, so the guard was always false, the step exited 0,
  // and the whole gate was inert. Asserting a path string proves nothing unless the
  // path is one this job actually creates, which is what the next test checks.
  assert.match(wf, /node "\$RUNNER_TEMP\/agent-tools\/pick-fix-credential\.mjs" --state/);
  assert.match(wf, /\[ -f "\$RUNNER_TEMP\/agent-tools\/pick-fix-credential\.mjs" \]/);

  // ORDER IS LOAD-BEARING: recording the round after the check is what makes a
  // refusal cost nothing. Assert the picker's index precedes the dispatch record's.
  const iPick = wf.indexOf("name: Pick a live fixer credential");
  const iDispatch = wf.indexOf("name: Record the fix-round dispatch");
  const iFixer = wf.indexOf("name: Address panel findings");
  assert.ok(iPick > 0 && iDispatch > 0 && iFixer > 0, "all three steps must exist");
  assert.ok(iPick < iDispatch, "the credential check must precede the dispatch record");
  assert.ok(iDispatch < iFixer, "the dispatch record must still precede the fixer");

  // BOTH gates are `!= 'false'`, never `== 'true'`: an unset output has to proceed,
  // or a skipped/older picker would silently stop every fixer in the pipeline.
  const gate = /if: steps\.guard\.outputs\.proceed == 'true' && steps\.cred\.outputs\.available != 'false'/g;
  assert.equal((wf.match(gate) ?? []).length, 2, "the dispatch record and the fixer must both carry the gate");
  assert.doesNotMatch(wf, /steps\.cred\.outputs\.available == 'true'/, "a positive gate would fail closed");

  // The fixer is handed the picked slot, resolved through `secrets` so the token
  // never travels in a step output.
  assert.match(
    wf,
    /claude_code_oauth_token: \$\{\{ steps\.cred\.outputs\.slot != '' && secrets\[format\('CLAUDE_CODE_OAUTH_TOKEN_\{0\}', steps\.cred\.outputs\.slot\)\] \|\| secrets\.CLAUDE_CODE_OAUTH_TOKEN \}\}/,
  );

  // A refusal must PAGE — skipping the steps leaves the job green, so the
  // `stalled` net (which keys on `r.fix === 'failure'`) cannot see it.
  assert.match(wf, /name: Page — no live credential for the fixer/);
  assert.match(wf, /if: steps\.guard\.outputs\.proceed == 'true' && steps\.cred\.outputs\.available == 'false'/);
  const page = wf.slice(wf.indexOf("name: Page — no live credential"), wf.indexOf("name: Record the fix-round dispatch"));
  assert.match(page, /agent-review-paged/, "the page must carry the latch marker or nothing reads it");
  assert.match(page, /No fix round was consumed/);
  // The capacity line is the ACTIONABLE half of the page — "2 of 9 credential
  // slot(s) configured, register more" is something an operator can do, where "a
  // lens failed" is not. Both halves are asserted: the env binding and the printf
  // that consumes it. Dropping either degrades the page to "capacity: unknown".
  assert.match(page, /CAPACITY: \$\{\{ steps\.cred\.outputs\.capacity \}\}/);
  assert.match(page, /Credential capacity: %s/);
  // Writing the latch freezes the agent-review-* checks, so every page must say so
  // (rounds.test.mjs enforces this fleet-wide; asserted here too so a reader of
  // THIS step sees the requirement).
  assert.match(page, /The review panel will not run again on this PR/);
});

test("the panel records the pool state the picker reads", () => {
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const panel = readFileSync(path.join(dir, "review-panel.mjs"), "utf8");
  // Written by the panel...
  assert.match(panel, /review-pool-state\.json/);
  // ...names only, never tokens. If this ever carried `current()` the artifact
  // would become a credential store.
  assert.match(panel, /liveSlotNames\(\)/);
  assert.match(panel, /retiredSlotNames\(\)/);
  assert.doesNotMatch(panel, /pool\.current\(\)/, "the artifact must never contain a token");

  // ...and uploaded, or the fix job downloads nothing.
  const wf = readFileSync(path.join(dir, "../../.github/workflows/agent-review-panel.yml"), "utf8");
  assert.match(wf, /\.agent-review\/review-pool-state\.json/);
});

test("no fix-job step reads from a path the fix job never creates", () => {
  // THE TEST THAT WOULD HAVE CAUGHT THE INERT GATE. `.trusted/` is created by the
  // review-panel job, not this one; the fix job snapshots trusted main into
  // `$RUNNER_TEMP/agent-tools` BEFORE checking out the branch, and every script it
  // runs must come from there. A `.trusted` reference here is silently dead: the
  // `[ -f ]` guard fails, the step exits 0, and whatever it was gating just does not
  // happen.
  const wf = readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), "../../.github/workflows/agent-review-panel.yml"),
    "utf8",
  );
  // Slice out the `fix:` job. Keyed on the two job headers around it so a reordered
  // workflow fails loudly here instead of silently checking the wrong region.
  const start = wf.indexOf("\n  fix:\n");
  assert.ok(start > 0, "could not locate the fix job");
  const after = wf.indexOf("\n  close-stuck-checks:\n", start);
  assert.ok(after > start, "could not locate the job after fix");
  const fixJob = wf.slice(start, after);

  // The staging step is what makes the path real.
  assert.match(fixJob, /cp -R \.\/scripts\/agent "\$\{\{ runner\.temp \}\}\/agent-tools"/);

  // And nothing in the job may reference `.trusted`, in a run: line or a guard.
  const offenders = fixJob
    .split("\n")
    .filter((l) => l.includes(".trusted") && !/^\s*#/.test(l));
  assert.deepEqual(offenders, [], `fix-job steps reference .trusted, which this job never creates:\n${offenders.join("\n")}`);
});

test("each unreadable shape reports its OWN reason, so a log says what is wrong", () => {
  // The `reason` is not decoration: it is logged, published as a step output, and is
  // the operator's only clue why a fixer did or did not run. It also makes the size
  // sanity check load-bearing — the retired-count check happens to reject the same
  // inputs, so without pinning the reason a dropped size check is invisible.
  const T = TOKEN_ENV;
  const expect = [
    [{ v: 1, size: "banana", live: [], retired: [T] }, "pool-size-unreadable"],
    [{ v: 1, size: "2", live: [], retired: [T, `${T}_1`] }, "pool-size-unreadable"],
    [{ v: 1, size: 2.5, live: [], retired: [T, `${T}_1`] }, "pool-size-unreadable"],
    [{ v: 1, size: -1, live: [], retired: [] }, "pool-size-unreadable"],
    [{ v: 1, size: 99, live: [], retired: [] }, "pool-size-unreadable"],
    [{ v: 2, size: 2, live: [], retired: [T, `${T}_1`] }, "pool-state-version"],
    [{ size: 2, live: [], retired: [T] }, "pool-state-version"],
    [{ v: 1, size: 0, live: [], retired: [] }, "pool-unconfigured"],
    [{ v: 1, size: 2, live: ["GITHUB_TOKEN"], retired: [] }, "unrecognised-slots"],
    [{ v: 1, size: 2, live: [], retired: [T] }, "retired-count-mismatch"],
    [{ v: 1, size: 2, live: [], retired: [T, T] }, "retired-duplicated"],
    [{ v: 1, size: 2, live: [], retired: [T, "GITHUB_TOKEN"] }, "retired-unreadable"],
    [{ v: 1, size: 2, live: [], retired: "nope" }, "retired-unreadable"],
    [{ v: 1, size: 2, live: [] }, "retired-unreadable"],
    [{ v: 1, size: 2, live: "nope" }, "pool-state-unreadable"],
    [null, "no-pool-state"],
  ];
  for (const [state, reason] of expect) {
    const got = chooseCredential(state);
    assert.equal(got.reason, reason, JSON.stringify(state));
    assert.equal(got.available, true, `${reason} must proceed`);
  }
});

test("the refusal requires a self-CONSISTENT artifact, not just an empty live list", () => {
  // Pins the asymmetry directly. Refusing stalls the PR and pages a human; proceeding
  // costs at most one wasted fix round. So `available: false` is reachable ONLY from
  // an artifact whose own metadata agrees that the pool is spent.
  const T = TOKEN_ENV;
  const consistent = { v: 1, size: 2, live: [], retired: [T, `${T}_1`] };
  assert.equal(chooseCredential(consistent).available, false, "the one refusing shape");

  // Every neighbouring shape differs by ONE field and must proceed.
  const proceeds = [
    { ...consistent, v: 2 },
    { ...consistent, size: 3 },
    { ...consistent, retired: [T] },
    { ...consistent, retired: [T, T] },
    { ...consistent, retired: [T, "GITHUB_TOKEN"] },
    { ...consistent, live: [`${T}_1`] },
  ];
  for (const st of proceeds) {
    assert.equal(chooseCredential(st).available, true, JSON.stringify(st));
  }
});

test("the panel's pool-state version matches the one the picker gates on", () => {
  // The version is now a GATE: an artifact whose `v` the picker does not recognise is
  // treated as uninterpretable and the fixer proceeds as before. That is the safe
  // direction, but it also means a writer/reader mismatch silently disables this
  // whole feature — so the two literals are pinned against each other here.
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const panel = readFileSync(path.join(dir, "review-panel.mjs"), "utf8");
  const picker = readFileSync(path.join(dir, "pick-fix-credential.mjs"), "utf8");

  const written = /review-pool-state\.json[\s\S]{0,400}?\bv:\s*(\d+)/.exec(panel);
  assert.ok(written, "could not find the version the panel stamps");
  const read = /POOL_STATE_VERSION = (\d+)/.exec(picker);
  assert.ok(read, "could not find the version the picker gates on");
  assert.equal(
    written[1],
    read[1],
    `the panel writes v:${written[1]} but the picker accepts only v:${read[1]} — the gate would be permanently off`,
  );
});
