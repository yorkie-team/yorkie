// What THIS repository's harness configuration must say.
//
// `scripts/agent/*.test.mjs` grade the harness code against a fixture
// configuration and are the same bytes in every repository that runs the
// harness. This file is the other half: it grades the values in this directory
// — the ones that are different in every repository — and it is the only test
// that reads them.
//
// Run from `scripts/agent`: `npm run test:repo`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const AGENT = path.join(HERE, "..", "..", "scripts", "agent");

// This directory, whatever the caller's environment says. Set before the first
// harness module is imported — which is why the imports below are dynamic: a
// static import would be evaluated before this line.
process.env.AGENT_HARNESS_CONFIG_DIR = HERE;

const agent = (file) => import(path.join(AGENT, file));
const checks = await agent("checks.mjs");
const panel = await agent("review-panel.mjs");
const captureMeta = await agent("capture-meta.mjs");
const rounds = await agent("rounds.mjs");
const metrics = await agent("metrics.mjs");
const setState = await agent("set-state.mjs");
const rebuttal = await agent("rebuttal.mjs");
const fixGuard = await agent("fix-guard.mjs");
const testRemovals = await agent("test-removals.mjs");
const protection = await agent("branch-protection.mjs");

// --- the values the harness shipped with, before they were configuration ------
//
// `repo.snapshot.json` was written from the harness modules while every value
// below was still a literal in `scripts/agent/`. Moving the values into this
// directory must not change one character of what the harness exports, says to
// a reviewer, or decides — so each is compared against that recording.
//
// It stays after the move as a change detector: an edit to `config.json` or
// `coverage-note.md` that alters a gate, a prompt or a comment fails here until
// the recording is updated in the same change, which puts the before and after
// of every such edit in front of whoever reviews it.

const SNAPSHOT = JSON.parse(readFileSync(path.join(HERE, "repo.snapshot.json"), "utf8"));

test("snapshot: the paths that define CI", () => {
  assert.deepEqual([...checks.CI_DEFINING_PATHS], SNAPSHOT.CI_DEFINING_PATHS);
});

test("snapshot: the file-class rules", () => {
  assert.deepEqual(JSON.parse(JSON.stringify(panel.CLASS_RULES)), SNAPSHOT.CLASS_RULES);
});

test("snapshot: the mechanical-coverage note", () => {
  assert.equal(panel.MECHANICAL_COVERAGE_NOTE, SNAPSHOT.MECHANICAL_COVERAGE_NOTE);
});

test("snapshot: every lens's appliesWhen and rubric, as the panel loads them", () => {
  const lenses = panel.loadLenses(path.join(AGENT, "lenses"));
  assert.deepEqual(
    Object.fromEntries(lenses.map((l) => [l.id, [...l.appliesWhen]])),
    SNAPSHOT.lensAppliesWhen,
  );
  assert.deepEqual(Object.fromEntries(lenses.map((l) => [l.id, l.rubric])), SNAPSHOT.lensRubrics);
  assert.ok(lenses.length >= 6, `expected the whole panel, loaded ${lenses.length} lenses`);
});

test("snapshot: the ready gate's CI gate name and the hand-off comment's CI bullet", () => {
  assert.equal(checks.CI_GATE_NAME, SNAPSHOT.ciGateName);
  assert.deepEqual([...checks.HANDOFF_CI_SUMMARY], SNAPSHOT.handoffCiBullet);
});

test("snapshot: the capture schema id and the lens prompt's opening line", () => {
  assert.equal(captureMeta.CAPTURE_META_SCHEMA, SNAPSHOT.CAPTURE_META_SCHEMA);
  assert.equal(
    panel.lensCacheKey({ diff: "x", scopeNote: "" }).split("\n")[0],
    SNAPSHOT.lensPromptFirstLine,
  );
});

test("snapshot: the bot identities each reader believes", () => {
  assert.deepEqual([...rounds.PAGE_AUTHOR_LOGINS], SNAPSHOT.PAGE_AUTHOR_LOGINS);
  assert.deepEqual([...metrics.LEDGER_AUTHOR_LOGINS], SNAPSHOT.LEDGER_AUTHOR_LOGINS);
  assert.deepEqual([...setState.LATCH_AUTHOR_LOGINS], SNAPSHOT.LATCH_AUTHOR_LOGINS);
  assert.deepEqual([...rebuttal.REBUTTAL_AUTHOR_LOGINS], SNAPSHOT.REBUTTAL_AUTHOR_LOGINS);
});

test("snapshot: the branch-protection decline message", () => {
  assert.equal(
    protection.decideProtection({ classicError: { status: 403 } }).reason,
    SNAPSHOT.protectionUnreadableReason,
  );
});

test("snapshot: which changes the fix guard calls a replicated rule, and what it says", () => {
  const paths = Object.entries(SNAPSHOT.fixGuard.replicatedRule);
  assert.ok(paths.length >= 9, `expected the recorded sample paths, found ${paths.length}`);
  for (const [file, expected] of paths) {
    assert.equal(fixGuard.crdtRuleChanges([{ filename: file }]).length === 1, expected, file);
  }
  const files = [{ filename: "pkg/document/crdt/tree.go" }];
  assert.deepEqual(
    fixGuard.lintRound({ files, prBody: "see Yorkie-JS-SDK#1" }).crdt,
    SNAPSHOT.fixGuard.lintWithBodyNamingCounterpart,
  );
  assert.deepEqual(fixGuard.lintRound({ files, prBody: "nothing" }).crdt, SNAPSHOT.fixGuard.lintWithoutIt);
  assert.equal(
    fixGuard.renderFixGuard(fixGuard.lintRound({ files, prBody: "" }), { head: "a".repeat(40), after: "b".repeat(40) }),
    SNAPSHOT.fixGuard.rendered,
  );
});

test("snapshot: what the test-removal record counts as a test, and as a case", () => {
  const { isTestFile, isRunnableTest, countCasesPatch, countCases } = SNAPSHOT.testRemovals;
  assert.ok(Object.keys(isTestFile).length >= 25, "expected the recorded sample paths");
  for (const [file, expected] of Object.entries(isTestFile)) {
    assert.equal(testRemovals.isTestFile(file), expected, `isTestFile(${file})`);
  }
  for (const [file, expected] of Object.entries(isRunnableTest)) {
    assert.equal(testRemovals.isRunnableTest(file), expected, `isRunnableTest(${file})`);
  }
  assert.ok(countCases.length >= 4, "expected the recorded patches");
  for (const { file, status, result } of countCases) {
    assert.deepEqual(testRemovals.countCases(countCasesPatch, { file, status }), result, `${file} (${status})`);
  }
});
