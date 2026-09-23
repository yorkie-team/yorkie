#!/usr/bin/env node
// Write the AI-authorship disclosure into a PR body, once, after an agent has
// actually pushed to it.
//
// WHY THIS EXISTS. `mark-ready.mjs` refuses to promote a PR whose body does not
// disclose autonomous AI authorship, and nothing in the pipeline ever wrote that
// disclosure. A human opens a PR, `@claude loop` opts it in, the fixer pushes
// commits, the panel approves every lens — and `promote` then refuses forever,
// because the body says what the author wrote before any agent touched it. The
// gate was doing its job and the PR was in a dead end (#2030).
//
// The disclosure belongs to whoever made the PR agent-authored, so it is written
// at the moment that becomes true: a push landed. Not at opt-in, when nothing has
// been written yet, and not by asking the model to remember — the same prompt
// that is supposed to remember is the one an injected issue can talk to.
//
// FAIL-SAFE IN BOTH DIRECTIONS. It exits 0 on every error: a disclosure this
// cannot write leaves a PR unpromotable, which is visible and recoverable, while
// a failure here breaking a fix round would lose the round's work. And it never
// overwrites: a body that already discloses — including one where a human wrote
// their own sentence — is left exactly as it is.
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { ensureDisclosed, DISCLOSURE_SENTENCE } from "./disclosure.mjs";

const warn = (msg) => process.stderr.write(`disclose-pr: ${msg}\n`);

function gh(args) {
  return execFileSync("gh", args, { encoding: "utf8" });
}

function main() {
  const pr = process.argv[2];
  if (!/^[0-9]+$/.test(String(pr ?? ""))) {
    warn(`usage: disclose-pr.mjs <pr-number>  (got: ${JSON.stringify(pr)})`);
    return;
  }

  let body;
  try {
    body = JSON.parse(gh(["pr", "view", pr, "--json", "body"])).body ?? "";
  } catch (e) {
    warn(`could not read PR #${pr}: ${e.message}`);
    return;
  }

  const next = ensureDisclosed(body);
  if (!next.changed) {
    process.stdout.write(`PR #${pr} already discloses agent authorship; leaving it alone\n`);
    return;
  }

  // THROUGH A FILE, never `--body "<text>"`. The body is arbitrary text written
  // by whoever opened the PR; as an argument it is one quoting bug away from
  // being interpreted, and `execFileSync` protecting the shell does not protect
  // `gh`'s own parsing of a leading `-`.
  let dir;
  try {
    dir = mkdtempSync(path.join(tmpdir(), "disclose-pr-"));
    const file = path.join(dir, "body.md");
    writeFileSync(file, next.body);
    gh(["pr", "edit", pr, "--body-file", file]);
  } catch (e) {
    warn(`could not update PR #${pr}: ${e.message} — it will not promote until its body discloses`);
    return;
  } finally {
    if (dir) rmSync(dir, { recursive: true, force: true });
  }
  process.stdout.write(`PR #${pr}: appended "${DISCLOSURE_SENTENCE}."\n`);
}

main();
