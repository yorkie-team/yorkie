import "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { IMPLEMENT_PROMPT_PATHS, missingImplementPaths } from "./conventions.mjs";
import { readWorkflow } from "./workflow-presence.mjs";

// The list and the prompt are two copies of one fact, in two files. Neither
// direction of drift errors: a path dropped from the prompt leaves a
// requirement nobody needs, and a path ADDED to the prompt leaves every
// adopter's test green while its agent is told to follow a file that is not
// there. So both directions are checked against the workflow's own text.

/** The issue-to-PR prompt's numbered process: from "Authoritative process" to the step that says STOP. */
function processBlock() {
  const text = readWorkflow("agent-implement.yml");
  if (text === null) return null;
  const from = text.indexOf("Authoritative process:");
  const to = text.indexOf("6. STOP.", from);
  assert.ok(from >= 0 && to > from, "agent-implement.yml no longer has the process block this test reads");
  return text.slice(from, to);
}

test("every path the list requires is one the issue-to-PR prompt names", (t) => {
  const block = processBlock();
  if (block === null) return t.skip("agent-implement.yml is not installed");
  assert.ok(IMPLEMENT_PROMPT_PATHS.length >= 6);
  for (const entry of IMPLEMENT_PROMPT_PATHS) {
    assert.ok(block.includes(entry.named), `the prompt no longer names ${entry.named}; drop it from IMPLEMENT_PROMPT_PATHS or restore it`);
    assert.ok(entry.named.startsWith(entry.path), `${entry.path}: \`named\` is how the prompt writes this path`);
    assert.ok(["file", "directory"].includes(entry.kind) && entry.role !== "");
  }
});

test("every repository path the issue-to-PR prompt names is on the list", (t) => {
  const block = processBlock();
  if (block === null) return t.skip("agent-implement.yml is not installed");
  // Path-like words ending `.md`. A placeholder segment (`<slug>`, `<topic>`,
  // `__TODAY__`) makes it a file the agent CREATES, which needs only its
  // directory; `-lessons.md` is the suffix of the todo's pair.
  const words = [...block.matchAll(/(?<![\w<>./-])(?:[\w.<>-]+\/)*[\w.<>-]+\.md\b/g)].map((m) => m[0]).filter((w) => !w.startsWith("-"));
  assert.ok(words.length >= 7, `expected the prompt to name its files, found ${JSON.stringify(words)}`);
  const files = new Set(IMPLEMENT_PROMPT_PATHS.filter((e) => e.kind === "file").map((e) => e.path));
  const directories = new Set([
    ...IMPLEMENT_PROMPT_PATHS.filter((e) => e.kind === "directory").map((e) => e.path),
    ...[...files].filter((f) => f.includes("/")).map((f) => path.posix.dirname(f)),
  ]);
  for (const word of new Set(words)) {
    const created = /[<>]|__/.test(word);
    const covered = created ? directories.has(path.posix.dirname(word)) : files.has(word);
    assert.ok(covered, `the prompt names ${word}, which IMPLEMENT_PROMPT_PATHS does not ${created ? "have the directory of" : "list"} — an adopter's test would not ask for it`);
  }
});

test("missingImplementPaths: names what a repository lacks, and why", (t) => {
  const root = mkdtempSync(path.join(tmpdir(), "harness-conventions-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  // An empty repository lacks all of them.
  assert.deepEqual(missingImplementPaths(root).map((m) => m.path), IMPLEMENT_PROMPT_PATHS.map((e) => e.path));
  assert.ok(missingImplementPaths(root).every((m) => m.problem === "does not exist" && m.role !== ""));
  for (const entry of IMPLEMENT_PROMPT_PATHS) {
    const at = path.join(root, ...entry.path.split("/"));
    if (entry.kind === "directory") mkdirSync(at, { recursive: true });
    else {
      mkdirSync(path.dirname(at), { recursive: true });
      writeFileSync(at, "x\n");
    }
  }
  assert.deepEqual(missingImplementPaths(root), []);
  // The wrong kind is not the path: a FILE named `docs/tasks/active`, a
  // DIRECTORY named `CLAUDE.md`.
  rmSync(path.join(root, "docs", "tasks", "active"), { recursive: true });
  writeFileSync(path.join(root, "docs", "tasks", "active"), "x\n");
  rmSync(path.join(root, "CLAUDE.md"));
  mkdirSync(path.join(root, "CLAUDE.md"));
  assert.deepEqual(missingImplementPaths(root).map((m) => [m.path, m.problem]), [
    ["CLAUDE.md", "is not a file"],
    ["docs/tasks/active", "is not a directory"],
  ]);
});
