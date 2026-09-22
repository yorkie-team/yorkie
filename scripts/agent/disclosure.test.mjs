import { test } from "node:test";
import assert from "node:assert/strict";
import { disclosesAiAuthorship, hasDisclosureTrailer, DISCLOSURE_TRAILER, HANDOFF_MARKER } from "./disclosure.mjs";

// --- the promotion gate's self-report -----------------------------------------
//
// `mark-ready.mjs` will not flip a PR to ready-for-review unless this returns
// true, so what it accepts is the whole of the contract. There was no test file
// for this module at all, which is how it came to accept the opposite of a
// disclosure: both `autonomous` and `Claude` tested against the whole body, so
// "This PR was NOT authored autonomously with Claude" satisfied the gate whose
// entire job is to establish that an agent DID write the change.

test("an affirmative disclosure passes, in the shapes people actually write", () => {
  for (const body of [
    "This PR was worked on autonomously by Claude Code.",
    "Autonomous AI assistance (Claude) was used.",
    "Implemented autonomously with Claude Code. Test plan below.",
    // A disclosure and an unrelated sentence in one body: the gate is per
    // clause, so the second sentence cannot veto the first.
    "Parts of this change were written autonomously by Claude; the tests are mine.",
    "## Summary\n\nFixed the tree-split case.\n\nWritten autonomously by Claude Code.\n",
    // Case and hyphenation variants of the AI term.
    "AI-assisted, autonomous run.",
    "autonomous run, ai tools used throughout",
  ]) {
    assert.equal(disclosesAiAuthorship(body), true, JSON.stringify(body));
  }
});

test("a NEGATED statement does not pass — it is the opposite of a disclosure", () => {
  for (const body of [
    "This PR was NOT authored autonomously with Claude.",
    "This PR wasn't authored autonomously with Claude.",
    "No autonomous Claude involvement here.",
    "Written by hand; no AI tools, autonomous or otherwise.",
    "Claude reviewed it but nothing autonomous happened.",
    "Reviewed by Claude, never autonomously.",
    "Built without autonomous Claude assistance.",
  ]) {
    assert.equal(disclosesAiAuthorship(body), false, JSON.stringify(body));
  }
});

test("a body missing either half does not pass", () => {
  // Both halves are required: "an agent" and "by itself". Either alone is a
  // different claim — Claude reviewing a human's PR is not autonomous
  // authorship, and "autonomous" alone names no actor.
  for (const body of [
    "Claude reviewed this PR.",
    "An autonomous run produced this.",
    "Just a normal PR.",
    "",
  ]) {
    assert.equal(disclosesAiAuthorship(body), false, JSON.stringify(body));
  }
});

test("junk in, false out — never a throw", () => {
  // This runs inside the promote job. A throw here fails promotion for every
  // PR, which is a worse outcome than any answer it could give.
  for (const junk of [null, undefined, 42, [], {}, { body: "autonomous Claude" }]) {
    assert.equal(disclosesAiAuthorship(junk), false, JSON.stringify(junk));
  }
});

test("the ambiguous phrasing fails CLOSED, and that is the intended direction", () => {
  // "autonomously with no human edits" is a genuine disclosure carrying a
  // negator, and it is refused. Pinned rather than left implicit: the cost is
  // one edit to a PR body — mark-ready's refusal names the line to add — and
  // the alternative is a gate that can be satisfied by a sentence meaning the
  // opposite. If this ever needs to pass, the fix is a clause-splitting rule,
  // not deleting the negator check.
  assert.equal(disclosesAiAuthorship("Written autonomously by Claude with no human edits."), false);
});

// --- the commit trailer -------------------------------------------------------

test("hasDisclosureTrailer matches the exact trailer, anywhere in the message", () => {
  assert.equal(hasDisclosureTrailer(`Fix the thing\n\n${DISCLOSURE_TRAILER}`), true);
  assert.equal(hasDisclosureTrailer(DISCLOSURE_TRAILER), true);
  assert.equal(hasDisclosureTrailer("Fix the thing\n\nAssisted-by: somebody else"), false);
  assert.equal(hasDisclosureTrailer(""), false);
  assert.equal(hasDisclosureTrailer(null), false);
});

test("the constants are pinned — both are contracts with something outside this file", () => {
  // DISCLOSURE_TRAILER is written into commit messages by the fixer prompts, and
  // HANDOFF_MARKER is written by mark-ready.mjs and read by whatever consumes
  // the hand-off. A silent edit to either breaks a producer/consumer pair with
  // no error anywhere — the reader simply finds nothing.
  assert.equal(DISCLOSURE_TRAILER, "Assisted-by: Claude Code (autonomous)");
  assert.equal(HANDOFF_MARKER, "<!-- agent-handoff -->");
});
