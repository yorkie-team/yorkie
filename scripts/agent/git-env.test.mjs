import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  GIT_LOCATION_VARS,
  GIT_BEHAVIOUR_VARS,
  repoScopedEnv,
  fixtureGitEnv,
  readHeadSha,
} from "./git-env.mjs";

/** git with a fixed identity, fully scoped — the helper under test. */
function git(dir, ...args) {
  return execFileSync("git", ["-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", ...args], {
    cwd: dir,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    env: fixtureGitEnv(dir),
  });
}

function makeRepo(prefix = "git-env-") {
  const dir = mkdtempSync(path.join(tmpdir(), prefix));
  git(dir, "init", "-q", "-b", "main");
  writeFileSync(path.join(dir, "a.mjs"), "export const A = 1;\n");
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "base");
  return dir;
}

// --- the strip list ----------------------------------------------------------

test("repoScopedEnv removes every location and behaviour variable", () => {
  // A denylist that misses one entry fails silently, which is how this class of
  // bug survives — so assert the whole list, derived from the module's own
  // exports rather than re-typed here.
  const hostile = {};
  for (const k of [...GIT_LOCATION_VARS, ...GIT_BEHAVIOUR_VARS]) hostile[k] = "/hostile";
  const env = repoScopedEnv("/tmp/some/repo", { ...hostile, PATH: "/usr/bin" });

  for (const k of GIT_LOCATION_VARS) {
    if (k === "GIT_CEILING_DIRECTORIES") continue; // deliberately re-set, below
    assert.equal(env[k], undefined, `${k} survived`);
  }
  for (const k of GIT_BEHAVIOUR_VARS) assert.equal(env[k], undefined, `${k} survived`);
  assert.equal(env.PATH, "/usr/bin", "unrelated variables must be preserved");
});

test("repoScopedEnv removes numbered GIT_CONFIG_KEY_n / VALUE_n pairs", () => {
  // git exports these for every `-c` it was given, so they arrive numbered and
  // cannot be listed literally.
  const env = repoScopedEnv("/tmp/r", {
    GIT_CONFIG_COUNT: "2",
    GIT_CONFIG_KEY_0: "core.hooksPath",
    GIT_CONFIG_VALUE_0: "/evil",
    GIT_CONFIG_KEY_11: "user.name",
    GIT_CONFIG_VALUE_11: "x",
    KEEP: "yes",
  });
  for (const k of ["GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_KEY_11", "GIT_CONFIG_VALUE_11"]) {
    assert.equal(env[k], undefined, `${k} survived`);
  }
  assert.equal(env.KEEP, "yes");
});

test("repoScopedEnv caps discovery at the PARENT of root, not root itself", () => {
  // git only honours ceiling entries that are strict ancestors of the directory
  // it searches from, so a ceiling equal to cwd is inert. Pinning that here
  // because the inert form looks correct and silently protects nothing.
  assert.equal(repoScopedEnv("/a/b/repo").GIT_CEILING_DIRECTORIES, "/a/b");
  assert.equal(repoScopedEnv("/a/b/repo/").GIT_CEILING_DIRECTORIES, "/a/b");
});

test("repoScopedEnv omits the ceiling at a filesystem root", () => {
  // dirname("/") === "/", so a ceiling there would be inert and misleading.
  assert.equal(repoScopedEnv("/").GIT_CEILING_DIRECTORIES, undefined);
});

// --- the write path ----------------------------------------------------------

test("fixtureGitEnv pins the git dir AND strips the redirecting variables", () => {
  // The critical property. Pinning GIT_DIR alone leaves GIT_INDEX_FILE and the
  // object-store variables inherited, and `git add` through an inherited
  // GIT_INDEX_FILE rewrites another repository's index — leaving it CORRUPT,
  // since the objects it records live in this fixture's store.
  const env = fixtureGitEnv("/tmp/fx", {
    GIT_INDEX_FILE: "/victim/.git/index",
    GIT_OBJECT_DIRECTORY: "/victim/.git/objects",
    GIT_COMMON_DIR: "/victim/.git",
    GIT_ALTERNATE_OBJECT_DIRECTORIES: "/victim/.git/objects",
  });
  assert.equal(env.GIT_DIR, path.join("/tmp/fx", ".git"));
  assert.equal(env.GIT_WORK_TREE, "/tmp/fx");
  for (const k of ["GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_ALTERNATE_OBJECT_DIRECTORIES"]) {
    assert.equal(env[k], undefined, `${k} survived into a WRITE environment`);
  }
});

// --- readHeadSha: the verify:self guard's only moving part -------------------

test("readHeadSha answers about `root`, not an inherited GIT_DIR", () => {
  // The guard in verify-self.mjs is top-level script code with no test file of
  // its own; this covers the one function that decides whether it works. A guard
  // that reads HEAD with an inherited environment watches the wrong repository
  // and is silently inert exactly under a hook, where it is needed.
  const mine = makeRepo("git-env-mine-");
  const other = makeRepo("git-env-other-");
  const saved = process.env.GIT_DIR;
  try {
    const expected = git(mine, "rev-parse", "HEAD").trim();
    const foreign = git(other, "rev-parse", "HEAD").trim();
    process.env.GIT_DIR = path.join(other, ".git");
    const got = readHeadSha(mine);
    assert.equal(got, expected);
    if (foreign !== expected) assert.notEqual(got, foreign);
  } finally {
    if (saved === undefined) delete process.env.GIT_DIR;
    else process.env.GIT_DIR = saved;
    rmSync(mine, { recursive: true, force: true });
    rmSync(other, { recursive: true, force: true });
  }
});

test("readHeadSha returns null outside a repository rather than an ancestor's sha", () => {
  // Two properties at once: the guard degrades to "not applicable" outside a
  // checkout (vendored copy, exported tarball), and the ceiling stops discovery
  // from ascending into a surrounding repository and answering about IT.
  const outer = makeRepo("git-env-outer-");
  const nested = path.join(outer, "plain");
  mkdirSync(nested, { recursive: true });
  try {
    assert.equal(readHeadSha(nested), null);
  } finally {
    rmSync(outer, { recursive: true, force: true });
  }
});

test("readHeadSha works in a linked worktree — the reason GIT_DIR is deleted, not pinned", () => {
  // The stated rationale for deleting rather than pinning: in a linked worktree
  // `.git` is a FILE holding a `gitdir:` pointer, so a computed
  // `GIT_DIR: root/.git` would be wrong exactly where it matters most. This is
  // the fixture that makes that claim testable rather than asserted.
  const main = makeRepo("git-env-wt-");
  const linked = path.join(main, "..", path.basename(main) + "-linked");
  try {
    git(main, "worktree", "add", "-q", "-b", "side", linked);
    const viaGit = execFileSync("git", ["rev-parse", "HEAD"], {
      cwd: linked,
      encoding: "utf8",
      env: repoScopedEnv(linked),
    }).trim();
    assert.equal(readHeadSha(linked), viaGit);
    assert.match(readHeadSha(linked) ?? "", /^[0-9a-f]{40}$/);
  } finally {
    try {
      git(main, "worktree", "remove", "--force", linked);
    } catch {
      rmSync(linked, { recursive: true, force: true });
    }
    rmSync(main, { recursive: true, force: true });
  }
});
