import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { HANDOFF_REF, branchPattern, gitEnv, prepareRepository, pushBranch, verifyBundle } from "./bundle-handoff.mjs";
import { fixtureGitEnv, repoScopedEnv } from "./git-env.mjs";

// Every bundle here is a real `git bundle`, built from a real repository, and
// most of them are hostile: the point of the module is what it refuses. Each
// test gets a fresh "upstream" (the repository the base lives in), a fresh
// verification repository holding only the base — exactly what the `finish`
// job builds — and builds the agent's side by hand.

const HERE = path.dirname(fileURLToPath(import.meta.url));
const CLI = path.join(HERE, "bundle-handoff.mjs");

/** No user configuration and a fixed identity, so the fixtures are the same everywhere. */
const FIXED = {
  GIT_CONFIG_GLOBAL: "/dev/null",
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_AUTHOR_NAME: "a",
  GIT_AUTHOR_EMAIL: "a@example.invalid",
  GIT_COMMITTER_NAME: "a",
  GIT_COMMITTER_EMAIL: "a@example.invalid",
};
/**
 * git in `cwd`, scoped to it: a working repository gets `fixtureGitEnv`, and
 * anything else (a bare repository, the directory a fixture is created in)
 * `repoScopedEnv` — never the caller's git variables, which under a hook name
 * the real repository.
 */
const envFor = (cwd) => ({ ...(existsSync(path.join(cwd, ".git")) ? fixtureGitEnv(cwd) : repoScopedEnv(cwd)), ...FIXED });
const run = (cwd, ...args) => execFileSync("git", args, { cwd, env: envFor(cwd), encoding: "utf8", stdio: ["pipe", "pipe", "pipe"] }).trim();
const runIn = (cwd, input, ...args) => execFileSync("git", args, { cwd, env: envFor(cwd), encoding: "utf8", input }).trim();
/** For the CLI: the script scopes its own git calls. */
const ENV = { ...repoScopedEnv(tmpdir()), ...FIXED };

/**
 * An upstream with `main` at c0 → base, base holding `.github/workflows/ci.yml`,
 * and a verification repository that has fetched the base and nothing else.
 */
function world(t) {
  const root = mkdtempSync(path.join(tmpdir(), "bundle-handoff-"));
  // Retried as a backstop. The cause of the ENOTEMPTY this once hit on hosted
  // runners was git's detached background maintenance after a fetch, which
  // gitEnv now turns off; see "gitEnv: no background maintenance" below.
  t.after(() => rmSync(root, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 }));
  const up = path.join(root, "up");
  run(root, "init", "-q", "-b", "main", up);
  run(up, "commit", "-q", "--allow-empty", "-m", "c0");
  const c0 = run(up, "rev-parse", "HEAD");
  writeFile(up, ".github/workflows/ci.yml", "on: push\n");
  writeFile(up, "README.md", "hello\n");
  run(up, "add", "-A");
  run(up, "commit", "-q", "-m", "base");
  const base = run(up, "rev-parse", "HEAD");
  const gitDir = path.join(root, "verify.git");
  prepareRepository({ gitDir, remote: `file://${up}`, base });
  return { root, up, c0, base, gitDir, remote: `file://${up}` };
}

function writeFile(dir, rel, text) {
  const file = path.join(dir, rel);
  execFileSync("mkdir", ["-p", path.dirname(file)]);
  writeFileSync(file, text);
}

/** A commit on the current branch of `up` that writes `rel`. */
function commit(up, rel, text, message = `edit ${rel}`) {
  writeFile(up, rel, text);
  run(up, "add", "-A");
  run(up, "commit", "-q", "-m", message);
  return run(up, "rev-parse", "HEAD");
}

/** Bundle `revs` out of `up`, the way the implement job's packaging step does. */
function bundle(w, name, ...revs) {
  const file = path.join(w.root, `${name}.bundle`);
  run(w.up, "bundle", "create", file, ...revs);
  return file;
}

const verify = (w, file, extra = {}) => verifyBundle({ gitDir: w.gitDir, bundle: file, base: w.base, issue: 42, ...extra });
const refused = (r, why) => {
  assert.equal(r.verdict, "refused", `expected a refusal, got ${JSON.stringify(r)}`);
  if (why) assert.match(r.reason, why);
};

// --- what passes -------------------------------------------------------------------

test("verify: one agent branch built on the base is handed over", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-fix-thing");
  commit(w.up, "pkg/a.go", "package a\n");
  const tip = commit(w.up, "pkg/b.go", "package a\n");
  const r = verify(w, bundle(w, "ok", "--branches=agent/42-*", `^${w.base}`));
  assert.deepEqual(r, { verdict: "ok", branch: "agent/42-fix-thing", head: tip, commits: 2 });
  assert.equal(run(w.root, "--git-dir", w.gitDir, "rev-parse", HANDOFF_REF), tip, "the verified tip is kept for the push");
});

test("verify: a merge of two lines of the agent's own work is still built on the base", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  run(w.up, "checkout", "-q", "-b", "side", w.base);
  commit(w.up, "b.txt", "b\n");
  run(w.up, "checkout", "-q", "agent/42-x");
  run(w.up, "merge", "-q", "--no-edit", "side");
  const r = verify(w, bundle(w, "merge", "agent/42-x", `^${w.base}`));
  assert.equal(r.verdict, "ok", JSON.stringify(r));
  assert.equal(r.commits, 3);
});

test("verify: no bundle at all is nothing to hand over, not a refusal", (t) => {
  const w = world(t);
  assert.equal(verify(w, path.join(w.root, "absent.bundle")).verdict, "none");
});

// --- the file itself -----------------------------------------------------------------

test("verify: refuses an empty file, a file that is not a bundle, and a symlink", (t) => {
  const w = world(t);
  const empty = path.join(w.root, "empty.bundle");
  writeFileSync(empty, "");
  refused(verify(w, empty), /empty/);
  const junk = path.join(w.root, "junk.bundle");
  writeFileSync(junk, "# v2 git bundle\nnot a header\n\nPACK nonsense");
  refused(verify(w, junk));
  // A link could point anywhere on the runner; the handoff is a file the
  // artifact carried, not a path the agent chose.
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const real = bundle(w, "real", "agent/42-x", `^${w.base}`);
  const link = path.join(w.root, "link.bundle");
  symlinkSync(real, link);
  refused(verify(w, link), /not a regular file/);
});

test("verify: refuses a bundle over the size limit before reading it", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const file = bundle(w, "big", "agent/42-x", `^${w.base}`);
  const size = readFileSync(file).length;
  refused(verify(w, file, { maxBytes: size - 1 }), /over the \d+-byte limit/);
  assert.equal(verify(w, file, { maxBytes: size }).verdict, "ok", "exactly the limit is allowed");
});

// --- the ref --------------------------------------------------------------------------

test("verify: refuses every branch name but agent/<this issue>-<slug>", (t) => {
  const w = world(t);
  for (const [name, why] of [
    ["feature/x", /is not agent\/42-<slug>/],
    ["agent/43-x", /is not agent\/42-<slug>/],
    ["agent/420-x", /is not agent\/42-<slug>/],
    ["agent/42-", /is not agent\/42-<slug>/],
    ["agent/42-a/b", /is not agent\/42-<slug>/],
    ["agent/42--x", /is not agent\/42-<slug>/],
  ]) {
    run(w.up, "checkout", "-q", "-B", name, w.base);
    commit(w.up, "a.txt", `${name}\n`);
    refused(verify(w, bundle(w, name.replace(/\W/g, "_"), name, `^${w.base}`)), why);
  }
});

/** Rewrite the ref a bundle's header names, leaving its pack untouched: git will not build some of these. */
function renameRef(file, from, to) {
  const raw = readFileSync(file);
  const cut = raw.indexOf("\n\n") + 2;
  const header = raw.subarray(0, cut).toString("latin1").replace(from, to);
  writeFileSync(file, Buffer.concat([Buffer.from(header, "latin1"), raw.subarray(cut)]));
}

test("verify: refuses a branch name git itself would not accept", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  for (const bad of ["agent/42-a..b", "agent/42-x.lock", "agent/42-x."]) {
    const file = bundle(w, bad.replace(/\W/g, "_"), "agent/42-x", `^${w.base}`);
    renameRef(file, "refs/heads/agent/42-x", `refs/heads/${bad}`);
    refused(verify(w, file), /not a valid ref name/);
  }
});

test("verify: refuses a tag, and a branch that names a tag object", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "work");
  commit(w.up, "a.txt", "a\n");
  run(w.up, "tag", "-a", "-m", "t", "v9");
  refused(verify(w, bundle(w, "tag", "refs/tags/v9", `^${w.base}`)), /not a branch/);
  // Git will not put a tag object on a branch, so the header is rewritten by
  // hand: the pack is untouched and the ref now claims to be the agent's branch.
  const file = bundle(w, "retag", "refs/tags/v9", `^${w.base}`);
  renameRef(file, "refs/tags/v9", "refs/heads/agent/42-x");
  refused(verify(w, file), /does not name a commit/);
});

test("verify: refuses a bundle with more than one ref", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-one");
  commit(w.up, "a.txt", "a\n");
  run(w.up, "checkout", "-q", "-b", "agent/42-two", w.base);
  commit(w.up, "b.txt", "b\n");
  refused(verify(w, bundle(w, "two", "--branches=agent/42-*", `^${w.base}`)), /carries 2 refs/);
  // HEAD beside the branch is a second ref too.
  refused(verify(w, bundle(w, "head", "HEAD", "agent/42-two", `^${w.base}`)), /carries 2 refs/);
});

// --- descent from the base ------------------------------------------------------------

test("verify: refuses history that is not built on the recorded base", (t) => {
  const w = world(t);
  // Unrelated history, with no prerequisites at all.
  run(w.up, "checkout", "-q", "--orphan", "agent/42-orphan");
  run(w.up, "rm", "-rqf", ".");
  commit(w.up, "x.txt", "x\n");
  refused(verify(w, bundle(w, "orphan", "agent/42-orphan")), /not built on the base/);

  // Built on an older commit of main: its prerequisite is not the base, so the
  // repository that holds only the base cannot complete it.
  run(w.up, "checkout", "-q", "-B", "agent/42-old", w.c0);
  commit(w.up, "y.txt", "y\n");
  refused(verify(w, bundle(w, "old", "agent/42-old", `^${w.c0}`)), /prerequisites are not in the base/);

  // The same, carrying its whole history so nothing is missing: still refused.
  refused(verify(w, bundle(w, "old-full", "agent/42-old")), /not built on the base|no parent/);
});

test("verify: refuses a merge that brings in history from beside the base", (t) => {
  const w = world(t);
  // An unrelated root merged into otherwise good work: everything is present
  // and the tip descends from the base, but one parent comes from nowhere.
  run(w.up, "checkout", "-q", "--orphan", "stray");
  run(w.up, "rm", "-rqf", ".");
  commit(w.up, "stray.txt", "s\n");
  run(w.up, "checkout", "-q", "-b", "agent/42-x", w.base);
  commit(w.up, "a.txt", "a\n");
  run(w.up, "merge", "-q", "--no-edit", "--allow-unrelated-histories", "stray");
  refused(verify(w, bundle(w, "stray", "agent/42-x", `^${w.base}`)), /no parent/);

  // A merge of an OLDER main commit: its parent is an ancestor of the base, so
  // the bundle needs a prerequisite the base does not have.
  run(w.up, "checkout", "-q", "-B", "older", w.c0);
  commit(w.up, "o.txt", "o\n");
  run(w.up, "checkout", "-q", "-B", "agent/42-y", w.base);
  commit(w.up, "b.txt", "b\n");
  run(w.up, "merge", "-q", "--no-edit", "older");
  refused(verify(w, bundle(w, "older", "agent/42-y", `^${w.base}`)));
});

test("verify: against a repository holding the base's whole history, an older parent is still refused", (t) => {
  // The finish job's repository holds the base alone, so a merge of an older
  // main commit is already refused for its missing prerequisite. The descent
  // rule does not lean on that: here the repository has every ancestor, the
  // bundle verifies, and the stray parent is what refuses it.
  const w = world(t);
  const full = path.join(w.root, "full.git");
  run(w.root, "clone", "-q", "--bare", "--no-local", w.up, full);
  run(w.up, "checkout", "-q", "-B", "older", w.c0);
  commit(w.up, "o.txt", "o\n");
  const older = run(w.up, "rev-parse", "HEAD");
  run(w.root, "--git-dir", full, "fetch", "-q", w.up, `${older}:refs/heads/older`);
  run(w.up, "checkout", "-q", "-B", "agent/42-y", w.base);
  commit(w.up, "b.txt", "b\n");
  run(w.up, "merge", "-q", "--no-edit", "older");
  const file = bundle(w, "older-full", "agent/42-y", `^${w.base}`, `^${older}`);
  refused(verifyBundle({ gitDir: full, bundle: file, base: w.base, issue: 42 }), /neither the base nor on the branch/);
});

test("verify: refuses a branch with no commit beyond the base, and one with too many", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  for (let i = 0; i < 4; i++) commit(w.up, `f${i}.txt`, `${i}\n`);
  const file = bundle(w, "four", "agent/42-x", `^${w.base}`);
  refused(verify(w, file, { maxCommits: 3 }), /4 commits, over the limit of 3/);
  assert.equal(verify(w, file, { maxCommits: 4 }).verdict, "ok");
  // A branch AT the base: git will not bundle it with the base excluded, so it
  // carries the base's whole history, and walks to nothing.
  run(w.up, "checkout", "-q", "-B", "agent/42-same", w.base);
  const w2 = world(t);
  const same = path.join(w.root, "same.bundle");
  run(w.up, "bundle", "create", same, "agent/42-same");
  refused(verifyBundle({ gitDir: w2.gitDir, bundle: same, base: w.base, issue: 42 }));
});

// --- the workflows ----------------------------------------------------------------------

test("verify: refuses any commit that changes .github/workflows — edit, add, delete, or in a merge alone", (t) => {
  const w = world(t);
  const cases = {
    edit: () => commit(w.up, ".github/workflows/ci.yml", "on: [push, pull_request]\n"),
    add: () => commit(w.up, ".github/workflows/new.yml", "on: push\n"),
    delete: () => {
      run(w.up, "rm", "-q", ".github/workflows/ci.yml");
      run(w.up, "commit", "-q", "-m", "delete");
    },
    // Changed and changed back: the tip matches the base, one commit does not.
    "edit then revert": () => {
      commit(w.up, ".github/workflows/ci.yml", "x\n");
      commit(w.up, ".github/workflows/ci.yml", "on: push\n");
    },
  };
  for (const [name, change] of Object.entries(cases)) {
    run(w.up, "checkout", "-q", "-B", "agent/42-x", w.base);
    commit(w.up, "a.txt", `${name}\n`);
    change();
    refused(verify(w, bundle(w, name.replace(/\W/g, "_"), "agent/42-x", `^${w.base}`)), /changes \.github\/workflows/);
  }
  // An evil merge: both parents leave the workflows alone, the merge result does not.
  run(w.up, "checkout", "-q", "-B", "side", w.base);
  commit(w.up, "b.txt", "b\n");
  run(w.up, "checkout", "-q", "-B", "agent/42-x", w.base);
  commit(w.up, "a.txt", "a\n");
  run(w.up, "merge", "-q", "--no-commit", "side");
  writeFile(w.up, ".github/workflows/ci.yml", "on: workflow_dispatch\n");
  run(w.up, "add", "-A");
  run(w.up, "commit", "-q", "--no-edit");
  refused(verify(w, bundle(w, "evil-merge", "agent/42-x", `^${w.base}`)), /changes \.github\/workflows/);
  // Everything else under .github is the reviewer's business, not this check's.
  run(w.up, "checkout", "-q", "-B", "agent/42-x", w.base);
  commit(w.up, ".github/PULL_REQUEST_TEMPLATE.md", "x\n");
  assert.equal(verify(w, bundle(w, "template", "agent/42-x", `^${w.base}`)).verdict, "ok");
});

// --- the objects --------------------------------------------------------------------------

test("verify: git's object checks refuse a tree that would write into .git", (t) => {
  const w = world(t);
  // Built by hand: git will not commit such a tree from a working directory.
  const blob = runIn(w.up, "", "hash-object", "-w", "--stdin");
  const inner = runIn(w.up, `100644 blob ${blob}\tconfig\n`, "mktree");
  const listing = run(w.up, "ls-tree", "HEAD");
  const tree = runIn(w.up, `${listing}\n040000 tree ${inner}\t.git\n`, "mktree");
  const evil = run(w.up, "commit-tree", tree, "-p", w.base, "-m", "evil");
  run(w.up, "update-ref", "refs/heads/agent/42-evil", evil);
  refused(verify(w, bundle(w, "dotgit", "agent/42-evil", `^${w.base}`)), /refused its objects|object check failed/);
});

test("verify: a tree with two .github entries is refused, not read by its first", (t) => {
  // The workflows rule reads `<commit>:.github/workflows`, and a tree holding
  // two `.github` entries answers that with the first. So the first is left
  // unchanged and the second carries the edit; only git's object checks stand
  // between that tree and the push.
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "edited", w.base);
  commit(w.up, ".github/workflows/ci.yml", "on: workflow_dispatch\n");
  const edited = run(w.up, "rev-parse", "HEAD:.github");
  const original = run(w.up, "rev-parse", `${w.base}:.github`);
  const readme = run(w.up, "rev-parse", `${w.base}:README.md`);
  const entry = (mode, name, oid) => Buffer.concat([Buffer.from(`${mode} ${name}\0`), Buffer.from(oid, "hex")]);
  const raw = Buffer.concat([entry("40000", ".github", original), entry("40000", ".github", edited), entry("100644", "README.md", readme)]);
  const tree = execFileSync("git", ["hash-object", "-t", "tree", "--literally", "-w", "--stdin"], { cwd: w.up, env: envFor(w.up), input: raw, encoding: "utf8" }).trim();
  const dup = run(w.up, "commit-tree", tree, "-p", w.base, "-m", "two .github entries");
  run(w.up, "update-ref", "refs/heads/agent/42-dup", dup);
  refused(verify(w, bundle(w, "dup", "agent/42-dup", `^${w.base}`)), /refused its objects|object check failed/);

  // The same bundle, through a git that does not check objects on a bundle
  // fetch — as some versions have not. The explicit check after the fetch is
  // what refuses it then.
  const bin = path.join(w.root, "lax-git");
  execFileSync("mkdir", ["-p", bin]);
  const real = execFileSync("sh", ["-c", "command -v git"], { encoding: "utf8" }).trim();
  writeFileSync(path.join(bin, "git"), [
    `#!${process.execPath}`,
    'const { spawnSync } = require("node:child_process");',
    "const a = process.argv.slice(2);",
    "const out = [];",
    'for (let i = 0; i < a.length; i++) { if (a[i] === "-c" && /fsckObjects/.test(a[i + 1] ?? "")) { i++; continue; } out.push(a[i]); }',
    `const r = spawnSync(${JSON.stringify(real)}, out, { stdio: "inherit" });`,
    "process.exit(r.status ?? 1);",
    "",
  ].join("\n"));
  execFileSync("chmod", ["+x", path.join(bin, "git")]);
  const gitDir = path.join(w.root, "lax.git");
  prepareRepository({ gitDir, remote: w.remote, base: w.base });
  const file = bundle(w, "dup-again", "agent/42-dup", `^${w.base}`);
  const path0 = process.env.PATH;
  process.env.PATH = `${bin}:${path0}`;
  try {
    refused(verifyBundle({ gitDir, bundle: file, base: w.base, issue: 42 }), /object check failed/);
  } finally {
    process.env.PATH = path0;
  }
});

test("verify: nothing in the bundle runs — hooks, attributes and filters stay data", (t) => {
  const w = world(t);
  const marker = path.join(w.root, "ran");
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  writeFile(w.up, "hooks/post-checkout", `#!/bin/sh\ntouch ${marker}\n`);
  writeFile(w.up, ".githooks/pre-push", `#!/bin/sh\ntouch ${marker}\n`);
  writeFile(w.up, ".gitattributes", "* filter=evil diff=evil\n");
  commit(w.up, ".gitconfig", `[filter "evil"]\n  smudge = touch ${marker}\n`);
  const r = verify(w, bundle(w, "inert", "agent/42-x", `^${w.base}`));
  assert.equal(r.verdict, "ok", JSON.stringify(r));
  assert.ok(!existsSync(marker), "something the bundle carried was executed");
});

test("verify: refuses a malformed base or issue number rather than guessing", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const file = bundle(w, "ok", "agent/42-x", `^${w.base}`);
  refused(verifyBundle({ gitDir: w.gitDir, bundle: file, base: "main", issue: 42 }), /base is not a commit id/);
  refused(verifyBundle({ gitDir: w.gitDir, bundle: file, base: w.base, issue: "42; rm" }), /not a number/);
  assert.throws(() => branchPattern("4.2"));
});

test("every step of the check is bounded in time, and running out is not a verdict", (t) => {
  // A bundle within the size limit can still unpack into far more, slowly.
  // Each git call has a ceiling; reaching it throws, which the CLI reports as
  // `error` — the check could not run — rather than a pass or a refusal.
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const file = bundle(w, "ok", "agent/42-x", `^${w.base}`);
  assert.throws(() => verifyBundle({ gitDir: w.gitDir, bundle: file, base: w.base, issue: 42, timeoutMs: 1 }), /did not finish within 1 ms/);
  assert.throws(() => prepareRepository({ gitDir: path.join(w.root, "slow.git"), remote: w.remote, base: w.base, timeoutMs: 1 }), /did not finish within 1 ms|ETIMEDOUT|timed out/i);
  const r = verify(w, file);
  assert.equal(r.verdict, "ok", "with time to run, the same bundle passes");
  assert.throws(() => pushBranch({ gitDir: w.gitDir, remote: remote(w), branch: r.branch, head: r.head, issue: 42, timeoutMs: 1 }), /did not finish within 1 ms/);
});

// --- the push -----------------------------------------------------------------------------

/** A bare remote holding main at the base; the test pushes to it. */
function remote(w) {
  const dir = path.join(w.root, "remote.git");
  run(w.root, "clone", "-q", "--bare", w.up, dir);
  // The clone carries every branch the test built in `up`; the remote starts with main alone.
  for (const ref of run(dir, "for-each-ref", "--format=%(refname)", "refs/heads").split("\n")) {
    if (ref !== "refs/heads/main") run(dir, "update-ref", "-d", ref);
  }
  return `file://${dir}`;
}

test("push: the verified commit lands on its branch, and only that commit", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  const tip = commit(w.up, "a.txt", "a\n");
  const r = verify(w, bundle(w, "ok", "agent/42-x", `^${w.base}`));
  const url = remote(w);
  const other = run(w.up, "rev-parse", `${w.base}`);
  assert.deepEqual(
    pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: other, issue: 42 }).pushed,
    false,
    "a commit other than the verified one must not be pushed",
  );
  assert.deepEqual(pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: r.head, issue: 42 }), { pushed: true });
  assert.equal(run(w.root, "--git-dir", url.slice("file://".length), "rev-parse", "refs/heads/agent/42-x"), tip);
});

test("push: refuses when the issue already has an agent branch, and never forces", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const r = verify(w, bundle(w, "ok", "agent/42-x", `^${w.base}`));
  const url = remote(w);
  const dir = url.slice("file://".length);
  // Another run's branch, under another slug.
  run(dir, "update-ref", "refs/heads/agent/42-other", w.base);
  const got = pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: r.head, issue: 42 });
  assert.equal(got.pushed, false);
  assert.match(got.reason, /agent\/42-other already exists/);
  // A different issue's branch is no obstacle.
  run(dir, "update-ref", "-d", "refs/heads/agent/42-other");
  run(dir, "update-ref", "refs/heads/agent/420-other", w.base);
  assert.deepEqual(pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: r.head, issue: 42 }), { pushed: true });
});

test("push: a branch that appears after the check is not overwritten, even by a fast-forward", (t) => {
  // The check and the push are two round trips, and another run can land
  // between them. Hiding a branch from the listing, but not from the push,
  // puts the remote in exactly that state. The branch sits at the base, which
  // the handoff repository has, so a plain push would fast-forward it.
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const r = verify(w, bundle(w, "ok", "agent/42-x", `^${w.base}`));
  const url = remote(w);
  const dir = url.slice("file://".length);
  run(dir, "update-ref", "refs/heads/agent/42-x", w.base);
  run(dir, "config", "uploadpack.hideRefs", "refs/heads/agent/");
  const got = pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: r.head, issue: 42 });
  assert.equal(got.pushed, false);
  assert.match(got.reason, /appeared on the remote after the check/);
  assert.equal(run(dir, "rev-parse", "refs/heads/agent/42-x"), w.base, "the other run's branch is untouched");
});

test("push: refuses a branch name or head that is not the shape verify hands on", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, "a.txt", "a\n");
  const r = verify(w, bundle(w, "ok", "agent/42-x", `^${w.base}`));
  const url = remote(w);
  for (const branch of ["main", "agent/43-x", "agent/42-x:refs/heads/main", "+agent/42-x"]) {
    assert.equal(pushBranch({ gitDir: w.gitDir, remote: url, branch, head: r.head, issue: 42 }).pushed, false, branch);
  }
  assert.equal(pushBranch({ gitDir: w.gitDir, remote: url, branch: r.branch, head: "HEAD", issue: 42 }).pushed, false);
  assert.equal(run(w.root, "--git-dir", url.slice("file://".length), "rev-parse", "refs/heads/main"), w.base, "main is untouched");
});

// --- the CLI, as the workflow calls it --------------------------------------------------------

function cli(w, args, extraEnv = {}) {
  const out = path.join(w.root, `out-${Math.random().toString(36).slice(2)}`);
  writeFileSync(out, "");
  let code = 0;
  let stdout = "";
  try {
    stdout = execFileSync(process.execPath, [CLI, ...args], { env: { ...ENV, GITHUB_OUTPUT: out, GH_TOKEN: "", ...extraEnv }, encoding: "utf8" });
  } catch (e) {
    code = e.status;
    stdout = String(e.stdout ?? "");
  }
  const outputs = Object.fromEntries(readFileSync(out, "utf8").split("\n").filter(Boolean).map((l) => l.split(/=(.*)/s).slice(0, 2)));
  return { code, stdout, outputs };
}

test("CLI: verify then push, with outputs written for the steps that follow", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-cli");
  const tip = commit(w.up, "a.txt", "a\n");
  const file = bundle(w, "ok", "agent/42-cli", `^${w.base}`);
  const gitDir = path.join(w.root, "cli.git");
  const v = cli(w, ["verify", "--bundle", file, "--base", w.base, "--issue", "42", "--git-dir", gitDir, "--remote", w.remote]);
  assert.equal(v.code, 0, v.stdout);
  assert.deepEqual(v.outputs, { verdict: "ok", branch: "agent/42-cli", head: tip, commits: "1" });
  const url = remote(w);
  const p = cli(w, ["push", "--git-dir", gitDir, "--branch", v.outputs.branch, "--head", v.outputs.head, "--issue", "42", "--remote", url]);
  assert.equal(p.code, 0, p.stdout);
  assert.deepEqual(p.outputs, { pushed: "true" });
});

test("CLI: a refusal and an absent bundle both exit non-zero, and say which", (t) => {
  const w = world(t);
  run(w.up, "checkout", "-q", "-b", "agent/42-x");
  commit(w.up, ".github/workflows/ci.yml", "x\n");
  const file = bundle(w, "wf", "agent/42-x", `^${w.base}`);
  const r = cli(w, ["verify", "--bundle", file, "--base", w.base, "--issue", "42", "--git-dir", path.join(w.root, "a.git"), "--remote", w.remote]);
  assert.equal(r.code, 1);
  assert.deepEqual(r.outputs, { verdict: "refused" });
  assert.match(r.stdout, /^::warning::/m);
  // Every line the bundle could have shaped is prefixed, so none of it is a
  // workflow command.
  for (const line of r.stdout.split("\n").filter((l) => l.startsWith("::"))) assert.match(line, /^::(?:warning|add-mask)::/);
  const n = cli(w, ["verify", "--bundle", path.join(w.root, "none.bundle"), "--base", w.base, "--issue", "42", "--git-dir", path.join(w.root, "b.git"), "--remote", w.remote]);
  assert.equal(n.code, 1);
  assert.deepEqual(n.outputs, { verdict: "none" });
  // And a verification directory that already exists is not one this run made.
  const again = cli(w, ["verify", "--bundle", file, "--base", w.base, "--issue", "42", "--git-dir", path.join(w.root, "a.git"), "--remote", w.remote]);
  assert.equal(again.code, 2);
  assert.deepEqual(again.outputs, { verdict: "error" }, "a check that could not run is not a verdict on the commits");
});

test("gitEnv: no background maintenance outlives a git call", () => {
  // From git 2.47 a fetch starts `maintenance run --auto` detached; it keeps
  // writing into the object store after the call returns (reproduced with git
  // 2.54: the refusal tests' cleanup failed with ENOTEMPTY in 5 of 12 runs).
  for (const env of [gitEnv(), gitEnv({ remote: "https://github.com/o/r.git", token: "tkn" })]) {
    const n = Number(env.GIT_CONFIG_COUNT);
    const config = Object.fromEntries(Array.from({ length: n }, (_, i) => [env[`GIT_CONFIG_KEY_${i}`], env[`GIT_CONFIG_VALUE_${i}`]]));
    assert.equal(config["core.hooksPath"], "/dev/null");
    assert.equal(config["maintenance.auto"], "false");
    assert.equal(config["gc.auto"], "0");
    assert.equal(env[`GIT_CONFIG_KEY_${n}`], undefined, "GIT_CONFIG_COUNT names every key");
  }
});

test("CLI: a token reaches git through the environment, never the command line", () => {
  // Not run against a server: what is checked is where the header is put.
  const src = readFileSync(CLI, "utf8");
  assert.match(src, /GIT_CONFIG_KEY_3 = `http\.\$\{origin\}\.extraheader`/);
  assert.doesNotMatch(src, /"-c",\s*`http\./, "the auth header must not be passed with -c, where it shows in the process list");
});
