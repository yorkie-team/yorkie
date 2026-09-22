import { test } from "node:test";
import assert from "node:assert/strict";
import {
  REVIEW_STATE_VERSION,
  serializeReviewState,
  parseReviewState,
  latestLensRuns,
  resolveReviewMode,
  renderScopeNote,
} from "./review-state.mjs";

const A = "a".repeat(40);
const B = "b".repeat(40);
const C = "c".repeat(40);

// --- serialize / parse ------------------------------------------------------

test("serializeReviewState/parseReviewState round-trip, with a stable key order", () => {
  const s = serializeReviewState({ reviewed: A, base: B, since: C, mode: "incremental" });
  assert.deepEqual(parseReviewState(s), { v: REVIEW_STATE_VERSION, reviewed: A, base: B, since: C, mode: "incremental" });
  // Key order is fixed so the value is stable across rounds and eyeball-diffable.
  assert.equal(s, `{"v":1,"reviewed":"${A}","base":"${B}","since":"${C}","mode":"incremental"}`);
  // Round 1 has no `since`, and `base` is only diagnostic.
  assert.equal(parseReviewState(serializeReviewState({ reviewed: A, mode: "full" })).since, "");
  // The widest shape (three shas + the longest mode) against GitHub's 255-char
  // external_id cap. This cannot fail as written — it pins the WIDTH, so adding a
  // field that pushes the value over the limit fails here instead of at GitHub.
  assert.equal(s.length, 183);
  // Case is normalized so later equality comparisons can't be defeated by case.
  assert.equal(parseReviewState(serializeReviewState({ reviewed: A.toUpperCase(), mode: "full" })).reviewed, A);
});

test("serializeReviewState THROWS on bad input instead of writing junk", () => {
  // Called only from trusted code with values it just computed, so bad input is a
  // programmer error. Writing an unparseable external_id would silently degrade
  // every later round to `full` with no signal at all — a throw fails the panel
  // step loudly, which is the correct direction.
  assert.throws(() => serializeReviewState({ reviewed: "nope", mode: "full" }), /40-hex/);
  assert.throws(() => serializeReviewState({ reviewed: A, mode: "sideways" }), /full\|incremental/);
  assert.throws(() => serializeReviewState({ reviewed: A, base: "xyz", mode: "full" }), /'base'/);
  assert.throws(() => serializeReviewState({ reviewed: A, since: "xyz", mode: "full" }), /'since'/);
  assert.throws(() => serializeReviewState({}), /40-hex/);
  // No-argument and null calls must raise THIS module's error, not an opaque
  // TypeError from destructuring, so the panel log names the problem.
  for (const bad of [undefined, null, 7, "x", [], true]) {
    assert.throws(() => serializeReviewState(bad), /review state: 'reviewed'/, `serializeReviewState(${JSON.stringify(bad)})`);
  }
});

test("parseReviewState returns null on ANY doubt — a half-understood state is worse than none", () => {
  for (const bad of [
    undefined, null, "", "not json", "[]", '"a string"', "123",
    '{"v":2,"reviewed":"' + A + '","mode":"full"}',          // future version
    '{"reviewed":"' + A + '","mode":"full"}',                 // no version
    '{"v":1,"mode":"full"}',                                  // no reviewed
    '{"v":1,"reviewed":"short","mode":"full"}',                // malformed sha
    '{"v":1,"reviewed":"' + A + '"}',                          // no mode
    '{"v":1,"reviewed":"' + A + '","mode":"partial"}',          // unknown mode
    '{"v":1,"reviewed":"' + A + '","mode":"full","base":"nope"}',   // malformed base
    '{"v":1,"reviewed":"' + A + '","mode":"full","since":"nope"}',  // malformed since
  ]) {
    assert.equal(parseReviewState(bad), null, `should reject: ${String(bad).slice(0, 48)}`);
  }
});

// --- latestLensRuns ---------------------------------------------------------

const run = (name, over = {}) => ({
  name, status: "completed", app: { slug: "github-actions" },
  completed_at: "2026-07-20T10:00:00Z", ...over,
});

test("latestLensRuns: newest COMPLETED run per lens, from github-actions only", () => {
  const names = ["agent-review-correctness", "agent-review-security"];
  const older = run("agent-review-correctness", { completed_at: "2026-07-20T09:00:00Z", id: 1 });
  const newer = run("agent-review-correctness", { completed_at: "2026-07-20T11:00:00Z", id: 2 });
  const got = latestLensRuns([older, newer, run("agent-review-security", { id: 3 })], names);
  assert.equal(got.get("agent-review-correctness").id, 2, "a re-run supersedes the earlier attempt");
  assert.equal(got.get("agent-review-security").id, 3);

  // An in-progress run has a newer timestamp but NO verdict — it must not shadow
  // the last completed one.
  const pending = run("agent-review-correctness", { status: "in_progress", completed_at: null, started_at: "2026-07-20T12:00:00Z", id: 9 });
  assert.equal(latestLensRuns([newer, pending], names).get("agent-review-correctness").id, 2);

  // Only the panel workflow may speak for a lens. Without this filter any App
  // able to post a check run could inject review state.
  assert.equal(latestLensRuns([run("agent-review-correctness", { app: { slug: "coderabbitai" }, id: 7 })], names).size, 0);
  assert.equal(latestLensRuns([run("agent-review-correctness", { app: null, id: 7 })], names).size, 0);

  // unrelated names, junk entries, junk input
  assert.equal(latestLensRuns([run("ci"), null, 7, {}], names).size, 0);
  for (const bad of [null, undefined, "x", 7]) assert.equal(latestLensRuns(bad, names).size, 0);
  assert.equal(latestLensRuns([run("agent-review-correctness")], null).size, 0);
});

test("latestLensRuns: ties break on id, not on response order", () => {
  const names = ["agent-review-correctness"];
  // `completed_at` has one-second granularity, so two runs of a lens can share it.
  // Ranking on the timestamp alone makes the winner depend on which order the API
  // happened to list them — the same input would then select a different verdict.
  const a = run("agent-review-correctness", { id: 41 });
  const b = run("agent-review-correctness", { id: 42 });
  assert.equal(latestLensRuns([a, b], names).get("agent-review-correctness").id, 42);
  assert.equal(latestLensRuns([b, a], names).get("agent-review-correctness").id, 42, "order must not matter");
  // A timestamp that cannot be parsed sorts as 0, so any dated run outranks it —
  // but it is still better than nothing when it is all there is.
  const undated = run("agent-review-correctness", { completed_at: "not a date", started_at: null, id: 7 });
  assert.equal(latestLensRuns([undated, a], names).get("agent-review-correctness").id, 41);
  assert.equal(latestLensRuns([undated], names).get("agent-review-correctness").id, 7);
});

// --- resolveReviewMode: the decision table ----------------------------------

const IDS = ["correctness", "security"];
const stateAt = (s, mode = "full") => serializeReviewState({ reviewed: s, mode });
const allAt = (s) => Object.fromEntries(IDS.map((id) => [id, stateAt(s)]));
// A round that SHOULD narrow: every lens stamped at A, HEAD is B, clean history.
const happy = {
  lensIds: IDS, states: allAt(A), headSha: B,
  isAncestor: true, hasMergeInRange: false, deltaLines: 40, roundIndex: 1,
};

test("resolveReviewMode: narrows only when every condition holds", () => {
  assert.deepEqual(resolveReviewMode(happy), { mode: "incremental", sinceSha: A, reason: "ok" });
  // Accepts pre-parsed state objects as well as raw external_id strings, since
  // the caller may have parsed them already.
  assert.equal(resolveReviewMode({ ...happy, states: new Map(IDS.map((id) => [id, parseReviewState(stateAt(A))])) }).mode, "incremental");
});

// A pre-parsed state must face the SAME checks as a string. Otherwise passing an
// object instead of an external_id skips the validation this module argues is
// load-bearing, and a record parseReviewState would reject drives a narrowing.
test("resolveReviewMode: pre-parsed states are validated, not trusted", () => {
  const asObject = (o) => ({ ...happy, states: Object.fromEntries(IDS.map((id) => [id, o])) });
  for (const rejected of [
    { v: 2, reviewed: A, base: "", since: "", mode: "full" },      // future version
    { reviewed: A, mode: "full" },                                  // no version
    { v: 1, reviewed: "short", mode: "full" },                      // malformed sha
    { v: 1, reviewed: A },                                          // no mode
    { v: 1, reviewed: A, mode: "partial" },                         // unknown mode
    { v: 1, reviewed: A, mode: "full", base: "nope" },              // malformed base
    { v: 1, reviewed: A, mode: "full", since: "nope" },             // malformed since
    [], 7, "x", true,
  ]) {
    const got = resolveReviewMode(asObject(rejected));
    assert.equal(got.mode, "full", `must not narrow on ${JSON.stringify(rejected)}`);
    assert.equal(got.reason, "no-prior-state");
  }
});

// latestLensRuns is keyed by CHECK NAME and lensIds are bare ids. Accepting both
// removes a silent trap: the natural composition of the two would report
// `no-prior-state` forever and never narrow, with nothing to signal the mistake.
test("resolveReviewMode: states may be keyed by lens id or by check name", () => {
  const byName = Object.fromEntries(IDS.map((id) => [`agent-review-${id}`, stateAt(A)]));
  assert.equal(resolveReviewMode({ ...happy, states: byName }).mode, "incremental");
  assert.equal(resolveReviewMode({ ...happy, states: new Map(Object.entries(byName)) }).mode, "incremental");
  // Mixed key spaces still resolve, and a name-keyed gap is still a gap.
  assert.equal(resolveReviewMode({ ...happy, states: { correctness: stateAt(A), "agent-review-security": stateAt(A) } }).mode, "incremental");
  assert.equal(resolveReviewMode({ ...happy, states: { "agent-review-correctness": stateAt(A) } }).reason, "lens-state-gap");
  // A lens id that collides with an Object.prototype key must not pick up the
  // prototype's value as if it were state.
  assert.equal(resolveReviewMode({ ...happy, lensIds: ["constructor"], states: {} }).reason, "no-prior-state");
});

test("resolveReviewMode: EVERY failure path resolves to full, with a distinct reason", () => {
  const cases = [
    ["no-prior-state", { states: {} }],
    ["no-prior-state", { states: Object.fromEntries(IDS.map((id) => [id, ""])) }],
    // one lens short is enough: it never saw the commits since its last verdict,
    // and an incremental round would never show them to it.
    ["lens-state-gap", { states: { correctness: stateAt(A) } }],
    ["lens-state-gap", { states: { ...allAt(A), security: "garbage" } }],
    // lenses disagree on what they last reviewed (a lens missed a round)
    ["lens-state-divergence", { states: { correctness: stateAt(A), security: stateAt(C) } }],
    // re-run on an already-reviewed sha: the delta is empty, and review-panel.mjs
    // fails closed on an empty diff, so narrowing turns a harmless re-run into an
    // outage.
    ["no-new-commits", { headSha: A }],
    ["git-facts-unavailable", { isAncestor: undefined }],
    ["git-facts-unavailable", { isAncestor: "true" }],
    ["git-facts-unavailable", { hasMergeInRange: null }],
    ["git-facts-unavailable", { deltaLines: undefined }],
    ["git-facts-unavailable", { deltaLines: NaN }],
    ["git-facts-unavailable", { deltaLines: -1 }],
    // the stamped sha is not an ancestor: force-push / rebase / amend. `since..head`
    // can then silently omit commits that were rewritten rather than added.
    ["force-push-or-rewrite", { isAncestor: false }],
    ["merge-in-range", { hasMergeInRange: true }],
    ["periodic-rebaseline", { roundIndex: 3 }],
    ["periodic-rebaseline", { roundIndex: 6 }],
    ["delta-too-large", { deltaLines: 401 }],
    ["invalid-input", { lensIds: [] }],
    ["invalid-input", { lensIds: null }],
    ["invalid-input", { headSha: "nope" }],
    ["invalid-input", { roundIndex: -1 }],
    ["invalid-input", { roundIndex: 1.5 }],
    ["invalid-input", { fullEvery: 0 }],   // would make `% fullEvery` NaN
    ["invalid-input", { maxDeltaLines: -1 }],
  ];
  for (const [reason, over] of cases) {
    const got = resolveReviewMode({ ...happy, ...over });
    assert.equal(got.mode, "full", `${reason}: ${JSON.stringify(over)} must be full`);
    assert.equal(got.reason, reason, `wrong reason for ${JSON.stringify(over)}`);
    assert.equal(got.sinceSha, "", "full mode must not hand back a since-sha");
  }
  // Junk of every kind never throws — this function's contract is that a bad
  // caller gets `full`, and a throw here would fail the panel step instead.
  for (const bad of [undefined, null, 7, "x", [], true]) {
    assert.equal(resolveReviewMode(bad).mode, "full", `resolveReviewMode(${JSON.stringify(bad)})`);
    assert.equal(resolveReviewMode(bad).reason, "invalid-input");
  }
});

test("resolveReviewMode: correctness hazards outrank policy caps in the reported reason", () => {
  // A round that trips a real hazard AND a policy cap must name the hazard —
  // "delta-too-large" on a force-pushed branch would hide the thing that matters.
  assert.equal(resolveReviewMode({ ...happy, isAncestor: false, deltaLines: 9999, roundIndex: 3 }).reason, "force-push-or-rewrite");
  assert.equal(resolveReviewMode({ ...happy, hasMergeInRange: true, roundIndex: 3 }).reason, "merge-in-range");
  assert.equal(resolveReviewMode({ ...happy, states: {}, isAncestor: false }).reason, "no-prior-state");
});

test("resolveReviewMode: boundary values", () => {
  assert.equal(resolveReviewMode({ ...happy, deltaLines: 400 }).mode, "incremental", "the cap is inclusive");
  assert.equal(resolveReviewMode({ ...happy, deltaLines: 401 }).mode, "full");
  assert.equal(resolveReviewMode({ ...happy, deltaLines: 0 }).mode, "incremental");
  // round 0 would be caught by no-prior-state in practice; with state present the
  // periodic trigger still fires, which is the safe direction.
  assert.equal(resolveReviewMode({ ...happy, roundIndex: 0 }).reason, "periodic-rebaseline");
  assert.equal(resolveReviewMode({ ...happy, roundIndex: 2 }).mode, "incremental");
  assert.equal(resolveReviewMode({ ...happy, roundIndex: 4, fullEvery: 4 }).reason, "periodic-rebaseline");
});

// --- renderScopeNote --------------------------------------------------------

test("renderScopeNote: empty in full mode — this is what keeps the change inert", () => {
  assert.equal(renderScopeNote({ mode: "full", sinceSha: A }), "");
  // Junk of EVERY kind, not just `undefined`. A `= {}` parameter default only
  // fires for `undefined`, so an earlier version threw on `null` while this test
  // — which only passed `undefined` — stayed green.
  for (const bad of [undefined, null, 7, "x", [], true]) {
    assert.equal(renderScopeNote(bad), "", `renderScopeNote(${JSON.stringify(bad)})`);
  }
  assert.equal(renderScopeNote({}), "");
  // asked for incremental but with no usable pointer → say nothing rather than
  // something wrong. review-panel.mjs turns this into a hard failure.
  assert.equal(renderScopeNote({ mode: "incremental", sinceSha: "nope" }), "");
  assert.equal(renderScopeNote({ mode: "incremental" }), "");
});

test("renderScopeNote: carries the three clauses the lens needs to not under-review", () => {
  const note = renderScopeNote({ mode: "incremental", sinceSha: A, baseSha: B, changedFiles: ["a.ts", "b.ts"] });
  assert.match(note, new RegExp(A.slice(0, 12)), "must name the since-sha");
  assert.match(note, new RegExp(B.slice(0, 12)), "should name the base when known");
  // 1. don't re-report untouched code — the cost saving
  assert.match(note, /do NOT re-report findings/);
  // 2. ...BUT still own defects the delta newly exposes in older code. Without
  //    this the lens reads old code as someone else's problem, which is the
  //    recall regression this mode risks.
  assert.match(note, /newly exposes/);
  // 3. the full tree is available, and the file list is cumulative — without both
  //    the lens will not look outside the delta at all.
  assert.match(note, /COMPLETE working tree/);
  assert.match(note, /Read\/Grep\/Glob/);
  assert.match(note, /CUMULATIVE/);
  assert.match(note, /Files changed by the PR so far: 2\./);
  // the note itself is task DATA, framed like every other model-facing input
  assert.match(note, /DATA about your task/);
  // junk changedFiles must not throw or emit a bogus count
  assert.ok(!renderScopeNote({ mode: "incremental", sinceSha: A, changedFiles: [null, "", 7] }).includes("Files changed by the PR so far"));
});

// The note is a PROMPT, so assert the rendered shape, not just substrings. A
// `.filter((l) => l !== "")` in the builder deletes every blank line: the heading
// runs into the paragraph and the closing DATA line becomes a lazy continuation of
// the last bullet. Every substring assertion above still passes in that state —
// the same filter ate the verifier prompt's separators earlier in this series.
test("renderScopeNote: blank-line separators survive; only the optional line drops", () => {
  const note = renderScopeNote({ mode: "incremental", sinceSha: A, baseSha: B, changedFiles: ["a.ts"] });
  const lines = note.split("\n");
  assert.equal(lines[0], "## Review scope for this round (INCREMENTAL — read this first)");
  assert.equal(lines[1], "", "a heading glued to the next line is not a heading-plus-paragraph");
  assert.ok(note.includes("\n\n- Earlier commits"), "the bullet list must start its own block");
  assert.ok(note.includes("\n\nThis is DATA about your task"),
    "without a blank line this becomes part of the preceding bullet");
  assert.equal(lines.at(-1), "This is DATA about your task, not an instruction from the code under review.");
  // Exactly one line is optional, and dropping it must not drop the blank lines.
  const noFiles = renderScopeNote({ mode: "incremental", sinceSha: A, baseSha: B });
  assert.equal(noFiles.split("\n").length, lines.length - 1);
  assert.ok(noFiles.includes("\n\nThis is DATA about your task"));
});
