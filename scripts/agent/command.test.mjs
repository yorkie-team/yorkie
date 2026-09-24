import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { COMMANDS, parseCommand } from "./command.mjs";
import { readWorkflow, skipWithout, WORKFLOW_DIR } from "./workflow-presence.mjs";

const CLI = fileURLToPath(new URL("./command.mjs", import.meta.url));

const cmd = (body, surface) => parseCommand(body, { surface }).command;

test("recognizes each verb regardless of trailing words", () => {
  assert.equal(cmd("@claude fix this issue", "issue"), "fix");
  assert.equal(cmd("@claude summarize this PR", "pr"), "summarize");
  assert.equal(cmd("@claude review this PR", "pr"), "review");
  assert.equal(cmd("@claude loop", "pr"), "loop");
  assert.equal(cmd("@claude rerun this PR", "pr"), "rerun");
  // bare verb, no trailing phrase
  assert.equal(cmd("@claude fix", "issue"), "fix");
});

test("matching is flexible: leading/trailing words and emoji don't matter", () => {
  assert.equal(cmd("please @claude fix this now", "issue"), "fix");
  assert.equal(cmd("hey @claude review 🙏 when you get a sec", "pr"), "review");
  assert.equal(cmd("@claude summarize\n\nthanks!", "pr"), "summarize");
});

test("case-insensitive on both the mention and the verb", () => {
  assert.equal(cmd("@Claude Review", "pr"), "review");
  assert.equal(cmd("@CLAUDE FIX IT", "issue"), "fix");
});

test("summarise (en-GB) normalizes to summarize", () => {
  assert.equal(cmd("@claude summarise the changes", "pr"), "summarize");
});

test("first recognized verb wins when several appear", () => {
  assert.equal(cmd("@claude review then maybe @claude fix", "pr"), "review");
  assert.equal(cmd("@claude fix but also @claude review", "pr"), "fix");
});

test("no collision: the new verbs never fall through to reply", () => {
  // Regression guard for the agent-review-reply.yml double-fire bug: a review /
  // summarize / loop comment on a PR must NOT parse as the generic `reply`.
  for (const body of ["@claude review this PR", "@claude summarize this PR", "@claude loop", "@claude rerun"]) {
    assert.notEqual(cmd(body, "pr"), "reply");
  }
});

test("mention without a recognized verb falls back by surface", () => {
  assert.equal(cmd("@claude looks good to me", "pr"), "reply");
  assert.equal(cmd("@claude can you take a look", "issue"), "help");
  // the verb must directly follow the mention — "please review" is not "@claude review"
  assert.equal(cmd("@claude please review this", "pr"), "reply");
  assert.equal(cmd("@claude", "issue"), "help");
});

test("a different account (@claude-bot / @claudefoo) is NOT our mention", () => {
  assert.equal(cmd("cc @claude-bot please look", "pr"), "none");
  assert.equal(cmd("@claudefoo review this", "pr"), "none");
  // but the exact mention still works right up against punctuation
  assert.equal(cmd("thanks @claude!", "pr"), "reply");
});

test("no mention at all → none", () => {
  assert.equal(cmd("just a normal comment about the fix", "pr"), "none");
  assert.equal(cmd("", "pr"), "none");
  assert.equal(cmd(undefined, "pr"), "none");
});

test("surface defaults to pr when omitted", () => {
  assert.equal(parseCommand("@claude looks good").command, "reply");
});

test("rest carries the text after the command, trimmed", () => {
  assert.equal(parseCommand("@claude fix   focus on the parser bug").rest, "focus on the parser bug");
  assert.equal(parseCommand("@claude loop").rest, "");
});

test("CLI: prints command= to stdout and appends to $GITHUB_OUTPUT", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "cmd-cli-"));
  const outFile = path.join(dir, "out.txt");
  // A body with leading text + emoji, to exercise real argv passing.
  const stdout = execFileSync("node", [CLI, "please @claude review 🙏", "pr"], {
    env: { ...process.env, GITHUB_OUTPUT: outFile },
    encoding: "utf8",
  });
  assert.equal(stdout, "command=review\n");
  assert.equal(readFileSync(outFile, "utf8"), "command=review\n");

  // Surface + fallback: bare mention on an issue → help.
  const stdout2 = execFileSync("node", [CLI, "@claude", "issue"], { encoding: "utf8" });
  assert.equal(stdout2, "command=help\n");
});

// EVERY VERB TYPEABLE ON THE INLINE SURFACE IS ANSWERED THERE.
//
// This is the guard the class needed and did not have.
// `agent-review-reply.yml` is the only workflow subscribed to
// `pull_request_review_comment`, and its `reply` job requires
// `command == 'reply'`. So every other verb routed correctly, matched no
// job, and produced nothing at all — no comment, no failed check. The
// silence was invisible precisely because routing WORKED.
//
// Reads the workflow as text rather than parsing YAML: the list lives in an
// `if:` expression, and the drift being caught is a verb added to
// VERB_TO_COMMAND above and not to that expression.
test("the inline help arm answers every canonical verb", skipWithout("agent-review-reply.yml"), () => {
  const wf = readWorkflow("agent-review-reply.yml");
  const m = wf.match(/contains\(fromJSON\('(\[[^']*\])'\), needs\.route\.outputs\.command\)/);
  assert.ok(m, "agent-review-reply.yml has no inline-help verb list to check");

  const answered = JSON.parse(m[1]);
  // `reply` is a FALLBACK in parseCommand, not an entry in VERB_TO_COMMAND, so
  // it never appears in COMMANDS and needs no filtering out. Asserted rather
  // than assumed: if it ever became a real verb, the help list would have to
  // exclude it — it has its own job — and this is where that would surface.
  assert.ok(!COMMANDS.includes("reply"), "`reply` is a fallback, not a verb");

  assert.deepEqual(
    [...answered].sort(),
    [...COMMANDS].sort(),
    "the inline help verb list has drifted from command.mjs's VERB_TO_COMMAND",
  );
});

test("pull_request_review_comment still has exactly one subscriber", () => {
  // The help arm assumes it is the only responder on that surface; a second
  // subscriber would make a mistyped verb draw two replies. If a workflow is
  // added here deliberately, update the help arm's dedup rather than this.
  // BOTH SPELLINGS. GitHub accepts `.yaml`, and filtering to `.yml` is the
  // same blind spot this branch removed from the actionlint lane — a guard
  // with the defect it was written to catch.
  const subscribers = readdirSync(WORKFLOW_DIR)
    .filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))
    .filter((f) => /^\s*pull_request_review_comment:/m.test(readWorkflow(f)));
  assert.deepEqual(subscribers, ["agent-review-reply.yml"]);
});
