// The PR-diff fingerprint decides whether a head's approval is CARRIED without
// any lens reading it, so what it ignores is code nobody reviews. This runs the
// workflow's own command — extracted from the step, not restated — against real
// repositories.
//
// The first version used `git patch-id --stable`, which discards whitespace: an
// indentation change that moves a Python call out of an `if` fingerprinted the
// same, and would have carried an approval over a change in behaviour.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WF = readFileSync(path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"), "utf8");
// The one line of the "Fingerprint the PR diff" step that computes it.
const CMD = (() => {
  const at = WF.indexOf("- name: Fingerprint the PR diff\n");
  assert.ok(at > 0, "no fingerprint step");
  const line = WF.slice(at).split("\n").find((l) => l.includes("FP=\"$("));
  assert.ok(line, "no FP= line in the fingerprint step");
  return line.trim().replace(/^FP="\$\(/, "").replace(/\)"$/, "");
})();

function repo() {
  const dir = mkdtempSync(path.join(tmpdir(), "fp-"));
  const git = (...a) => execFileSync("git", a, { cwd: dir, encoding: "utf8" });
  git("init", "-q", "-b", "main");
  git("config", "user.email", "t@t");
  git("config", "user.name", "t");
  const write = (f, s) => writeFileSync(path.join(dir, f), s);
  const commit = (m) => { git("add", "-A"); git("commit", "-qm", m); return git("rev-parse", "HEAD").trim(); };
  // `origin/main` is what the step measures against.
  const fp = () => execFileSync("bash", ["-c", `git update-ref refs/remotes/origin/main main && ${CMD}`], { cwd: dir, encoding: "utf8" }).trim();
  return { git, write, commit, fp };
}

test("fingerprint: a whitespace-only change that alters meaning is a DIFFERENT diff", () => {
  const r = repo();
  r.write("m.py", "def f():\n    if x:\n        a()\n    b()\n");
  r.commit("base");
  r.git("checkout", "-qb", "pr");
  r.write("m.py", "def f():\n    if x:\n        a()\n        c()\n    b()\n");
  r.commit("c() inside the if");
  const approved = r.fp();
  r.write("m.py", "def f():\n    if x:\n        a()\n    c()\n    b()\n");
  r.commit("c() moved out of the if — whitespace only");
  assert.notEqual(r.fp(), approved, "an indentation change must not carry an approval");
});

test("fingerprint: a merge of main away from the PR's hunks is the SAME diff", () => {
  const r = repo();
  r.write("a.txt", Array.from({ length: 100 }, (_, i) => `${i + 1}`).join("\n") + "\n");
  r.write("b.txt", "one\n");
  r.commit("base");
  r.git("checkout", "-qb", "pr");
  r.write("a.txt", readFileSync(path.join(r.git("rev-parse", "--show-toplevel").trim(), "a.txt"), "utf8").replace("\n50\n", "\nfifty\n"));
  r.commit("pr");
  const approved = r.fp();
  assert.match(approved, /^[0-9a-f]{40}$/);
  r.git("checkout", "-q", "main");
  r.write("b.txt", "two\n");
  r.commit("main moves elsewhere");
  r.git("checkout", "-q", "pr");
  r.git("merge", "-q", "--no-edit", "main");
  assert.equal(r.fp(), approved, "#1426's update-branch shape must carry");
});
