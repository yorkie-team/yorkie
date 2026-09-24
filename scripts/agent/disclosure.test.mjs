import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { disclosesAiAuthorship, hasDisclosureTrailer, ensureDisclosed, DISCLOSURE_TRAILER, DISCLOSURE_SENTENCE, DISCLOSURE_BLOCK_MARKER, HANDOFF_MARKER } from "./disclosure.mjs";

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

// --- the sentence the pipeline writes ----------------------------------------

test("the sentence this pipeline writes satisfies the gate that reads it", () => {
  // THE WHOLE POINT OF PAIRING THEM IN ONE MODULE. The writer and the gate were
  // two independent readings of the same English and they disagreed: the house
  // attribution line is "🤖 Generated with Claude Code", which names the actor
  // and never says `autonomous`, so every agent-pushed PR failed a gate nobody
  // could see failing until one sat unpromotable with six green lenses (#2030).
  // If someone edits either the sentence or the predicate, this fails.
  assert.equal(disclosesAiAuthorship(DISCLOSURE_SENTENCE), true, DISCLOSURE_SENTENCE);
  assert.equal(disclosesAiAuthorship(`${DISCLOSURE_SENTENCE}.`), true);
});

test("the sentence survives being appended to a real PR body", () => {
  // Not the same assertion as above. The predicate splits on `.`, `;`, `\n`,
  // `!` and `?`, so a sentence that discloses on its own can stop disclosing
  // once it sits next to other text — and every body this is appended to ends
  // with a markdown link whose URL contains dots.
  const body = [
    "## What this does",
    "",
    "Fixes the split order. See https://example.com/a.b.c for context.",
    "",
    "🤖 Generated with [Claude Code](https://claude.com/claude-code)",
  ].join("\n");
  const { changed, body: next } = ensureDisclosed(body);
  assert.equal(changed, true, "a body with only the house attribution line must gain the disclosure");
  assert.equal(disclosesAiAuthorship(next), true, `the appended body must satisfy the gate:\n${next}`);
  assert.ok(next.startsWith(body.trimEnd()), "the original body must be preserved verbatim");
});

test("ensureDisclosed never speaks twice, and never over-writes a human", () => {
  const { body: once } = ensureDisclosed("## Summary\n\nsomething");
  assert.equal(ensureDisclosed(once).changed, false, "appending must be idempotent");
  // A human who wrote their own disclosure keeps their words.
  const mine = "I ran this autonomously with Claude Code and checked it by hand";
  assert.equal(ensureDisclosed(mine).changed, false);
  assert.equal(ensureDisclosed(mine).body, mine);
  // The marker alone also stops a second append, in case the predicate is ever
  // narrowed: the block is still there and saying it again helps nobody.
  const marked = `x\n\n${DISCLOSURE_BLOCK_MARKER}\nsomething else entirely`;
  assert.equal(ensureDisclosed(marked).changed, false);
});

test("junk in, unchanged out — this runs beside a fix round that must not break", () => {
  for (const junk of [null, undefined, 42, [], {}]) {
    const r = ensureDisclosed(junk);
    assert.equal(typeof r.body, "string", JSON.stringify(junk));
  }
  assert.equal(ensureDisclosed("").changed, true, "an empty body still needs the disclosure");
  assert.equal(disclosesAiAuthorship(ensureDisclosed("").body), true);
});

test("every job that pushes a fix also discloses", () => {
  // THE DEAD END WAS STRUCTURAL, not a one-off. Three jobs push commits to a
  // PR — the on-demand fixer, the panel's fix round, and the CI-fix arm — and a
  // PR that any of them touches needs the disclosure before it can ever be
  // promoted. A fourth pushing job added without this step recreates the dead
  // end silently: the panel goes green and `promote` refuses forever.
  const HERE = path.dirname(fileURLToPath(import.meta.url));
  const dir = path.join(HERE, "..", "..", ".github", "workflows");
  // DERIVED, not listed. "Can push" is "mints a token with contents: write" —
  // that is the capability, and it is one line in the file that grants it. An
  // earlier version of this test keyed on the presence of `claude-code-action`
  // and was wrong in both directions: it missed nothing, but it flagged
  // agent-summarize.yml, which runs a model with `contents: read` and
  // `--allowedTools "Read,Write"` and cannot push at all. It did find a real
  // fourth pusher the author had missed — agent-review-reply.yml — which is why
  // the rule is derived rather than maintained by hand.
  const pushers = readdirSync(dir)
    .filter((f) => f.startsWith("agent-") && f.endsWith(".yml"))
    .filter((f) => /^\s+permission-contents: write/m.test(readFileSync(path.join(dir, f), "utf8")));
  assert.ok(pushers.length >= 4, `expected the pushing workflows to be found, got: ${pushers.join(", ")}`);
  for (const file of pushers) {
    const wf = readFileSync(path.join(dir, file), "utf8");
    assert.match(
      wf,
      /- name: Disclose agent authorship in the PR body/,
      `${file} can push commits to a PR but never discloses, so that PR can never be promoted`,
    );
    assert.match(wf, /disclose-pr\.mjs" "\$(?:PR|\{PR\})"/, `${file}: the disclosure step must run the CLI`);
  }
});
