// `sync.mjs` against two throwaway repositories: what it copies and deletes,
// what it leaves alone, what it records, and every case in which it refuses.

import "./fixtures/use-fixture-config.mjs";

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { fixtureGitEnv } from "./git-env.mjs";
import { MANIFEST_PATH, UPSTREAM_PATH, buildManifest, compareTree, describeDifference, manifestDigest } from "./manifest.mjs";
import { slugFromRemote, sync } from "./sync.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const real = (f) => readFileSync(path.join(HERE, f), "utf8");
const FIXTURE_CONFIG = JSON.parse(real("fixtures/harness-config/config.json"));

/** A git repository in a temp directory, with helpers scoped to it. */
function repo(t, prefix) {
  const dir = mkdtempSync(path.join(os.tmpdir(), prefix));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  // Pinned to the fixture: an inherited GIT_DIR would turn these into writes
  // to whichever repository the test was started from.
  const env = fixtureGitEnv(dir);
  const git = (...a) => execFileSync("git", a, { cwd: dir, env, encoding: "utf8" }).trim();
  git("init", "-q", "-b", "main");
  git("config", "user.email", "t@example.test");
  git("config", "user.name", "t");
  const write = (rel, text) => {
    mkdirSync(path.dirname(path.join(dir, rel)), { recursive: true });
    writeFileSync(path.join(dir, rel), text);
  };
  const read = (rel) => readFileSync(path.join(dir, rel), "utf8");
  const commit = (m) => {
    git("add", "-A");
    git("commit", "-qm", m);
    return git("rev-parse", "HEAD");
  };
  return { dir, git, write, read, commit, has: (rel) => existsSync(path.join(dir, rel)) };
}

/** The repository the core is written in: a small core, its manifest, `repository: null`. */
function source(t) {
  const r = repo(t, "sync-src-");
  r.write("scripts/agent/config.mjs", real("config.mjs")); // the real loader, so the schema report is real
  r.write("scripts/agent/keep.mjs", "export const v = 2;\n");
  r.write("scripts/agent/added.mjs", "export const added = true;\n");
  r.write("scripts/agent/lenses/x.md", "# new rubric\n");
  r.write(".github/workflows/agent-one.yml", "name: one v2\n");
  r.write(".github/workflows/ci.yml", "name: CI (the source's own)\n");
  r.write(".github/agent-harness/config.json", JSON.stringify(FIXTURE_CONFIG));
  r.write(".github/agent-harness/coverage-note.md", "the source's note\n");
  r.write(".github/actions/agent-toolchain/action.yml", "name: the source's toolchain\n");
  r.write(UPSTREAM_PATH, `${JSON.stringify({ repository: null })}\n`);
  r.write(MANIFEST_PATH, buildManifest(r.dir));
  r.git("remote", "add", "origin", "https://github.com/fixture-org/fixture-upstream.git");
  const head = r.commit("core");
  // As a clone has it after a fetch: the remote's default branch, here called
  // `trunk` so that nothing can pass by assuming `main`, at this commit.
  const publish = () => {
    r.git("update-ref", "refs/remotes/origin/trunk", "HEAD");
    r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk");
  };
  publish();
  return { ...r, head, publish };
}

/** Every file under `dir`, with its bytes, for "nothing changed" assertions. */
function snapshot(dir) {
  const out = {};
  const walk = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      if (e.name === ".git") continue;
      const p = path.join(d, e.name);
      if (e.isSymbolicLink()) out[path.relative(dir, p)] = "<link>";
      else if (e.isDirectory()) walk(p);
      else out[path.relative(dir, p)] = readFileSync(p, "utf8");
    }
  };
  walk(dir);
  return out;
}

/** A repository with an older copy of the core, and files of its own. */
function destination(t, changeConfig = (c) => c) {
  const r = repo(t, "sync-dst-");
  r.write("scripts/agent/config.mjs", "// an older loader\n");
  r.write("scripts/agent/keep.mjs", "export const v = 1;\n");
  r.write("scripts/agent/gone.mjs", "export const gone = true;\n");
  r.write("scripts/agent/lenses/old.md", "# dropped upstream\n");
  r.write(".github/workflows/agent-one.yml", "name: one v1\n");
  r.write(".github/workflows/agent-retired.yml", "name: retired\n");
  r.write(".github/workflows/ci.yml", "name: CI (the destination's own)\n");
  r.write(".github/agent-harness/config.json", JSON.stringify(changeConfig(structuredClone(FIXTURE_CONFIG))));
  r.write(".github/agent-harness/coverage-note.md", "the destination's note\n");
  r.write(".github/agent-harness/repo.test.mjs", "// the destination's\n");
  r.write(".github/actions/agent-toolchain/action.yml", "name: the destination's toolchain\n");
  r.write("scripts/own.mjs", "// not the harness's\n");
  r.commit("an older copy");
  return r;
}

test("sync: the core becomes the source's, and nothing else is touched", (t) => {
  const src = source(t);
  const dst = destination(t);
  const result = sync({ from: src.dir, to: dst.dir });

  // Copied, overwritten, deleted.
  assert.equal(dst.read("scripts/agent/keep.mjs"), "export const v = 2;\n");
  assert.equal(dst.read("scripts/agent/added.mjs"), "export const added = true;\n");
  assert.equal(dst.read("scripts/agent/config.mjs"), real("config.mjs"));
  assert.equal(dst.read("scripts/agent/lenses/x.md"), "# new rubric\n");
  assert.equal(dst.read(".github/workflows/agent-one.yml"), "name: one v2\n");
  for (const rel of ["scripts/agent/gone.mjs", "scripts/agent/lenses/old.md", ".github/workflows/agent-retired.yml"]) {
    assert.equal(dst.has(rel), false, `${rel} is not in the source and must be deleted`);
  }
  assert.deepEqual(result.deleted.sort(), [".github/workflows/agent-retired.yml", "scripts/agent/gone.mjs", "scripts/agent/lenses/old.md"]);
  assert.equal(result.copied.length, 5);

  // The destination now verifies against the source's manifest.
  assert.equal(dst.read(MANIFEST_PATH), src.read(MANIFEST_PATH));
  assert.deepEqual(describeDifference(compareTree(dst.dir)), []);

  // Never touched: the destination's own half, its other workflows, its other scripts.
  assert.equal(dst.read(".github/agent-harness/coverage-note.md"), "the destination's note\n");
  assert.equal(dst.read(".github/agent-harness/repo.test.mjs"), "// the destination's\n");
  assert.deepEqual(JSON.parse(dst.read(".github/agent-harness/config.json")), FIXTURE_CONFIG);
  assert.equal(dst.read(".github/actions/agent-toolchain/action.yml"), "name: the destination's toolchain\n");
  assert.equal(dst.read(".github/workflows/ci.yml"), "name: CI (the destination's own)\n");
  assert.equal(dst.read("scripts/own.mjs"), "// not the harness's\n");

  // Recorded: which repository, which commit, which manifest.
  const upstream = JSON.parse(dst.read(UPSTREAM_PATH));
  assert.deepEqual(upstream, {
    repository: "fixture-org/fixture-upstream",
    commit: src.head,
    manifestDigest: manifestDigest(src.read(MANIFEST_PATH)),
  });
  assert.deepEqual(result.upstream, upstream);
  // The fixture configuration is the one the copied loader was written for.
  assert.deepEqual(result.configProblems, []);
  // The source is untouched.
  assert.equal(src.git("status", "--porcelain"), "");
});

test("sync: reports the keys the new schema requires and the ones it no longer knows", (t) => {
  const src = source(t);
  const dst = destination(t, (c) => {
    delete c.repo.appSlug; // required by the source's schema
    delete c.fixer.commitNote;
    c.fixer.forbiddenCommands = "`fixture test-all`"; // a key an older schema had
    return c;
  });
  const { configProblems } = sync({ from: src.dir, to: dst.dir });
  assert.deepEqual(configProblems.sort(), ["fixer.commitNote is missing", "fixer.forbiddenCommands is not a known key", "repo.appSlug is missing"]);
  // Reported, not repaired: the configuration is the destination's to edit.
  assert.equal(JSON.parse(dst.read(".github/agent-harness/config.json")).fixer.forbiddenCommands, "`fixture test-all`");
});

test("sync: a repository adopting the harness for the first time", (t) => {
  const src = source(t);
  const dst = repo(t, "sync-new-");
  dst.write("README.md", "# a repository with no harness yet\n");
  dst.commit("start");
  const result = sync({ from: src.dir, to: dst.dir, repository: "another-org/renamed-upstream" });
  assert.equal(result.copied.length, 5);
  assert.deepEqual(result.deleted, []);
  assert.equal(JSON.parse(dst.read(UPSTREAM_PATH)).repository, "another-org/renamed-upstream");
  // No configuration yet: the report says so rather than passing.
  assert.equal(result.configProblems.length > 0, true);
  assert.match(result.configProblems.join("\n"), /no directory|cannot read/);
  assert.equal(dst.has(".github/actions"), false, "the toolchain action is the adopter's to write");
  // What is left for a person, in the order it has to be done: pin the
  // upstream, then stage BEFORE testing — the suite compares the manifest with
  // what git tracks, and nothing copied is tracked yet.
  assert.equal(result.todo.length, 2);
  assert.match(result.todo[0], /Pin the upstream in \.github\/agent-harness\/repo\.test\.mjs[\s\S]*"another-org\/renamed-upstream"/);
  assert.match(result.todo[1], /^Stage the result before testing — `git add scripts\/agent \.github\/workflows \.github\/agent-harness`[\s\S]*npm ci && npm test && npm run test:repo/);
  // No `.gitattributes` here yet: said, not fatal.
  assert.equal(result.warnings.length, 2);
  assert.match(result.warnings.join("\n"), /does not set `scripts\/agent\/\*\* -text`[\s\S]*does not set `\.github\/workflows\/agent-\*\.yml -text`/);
});

test("sync: no line-ending warning where .gitattributes already protects the core", (t) => {
  const src = source(t);
  const dst = destination(t);
  dst.write(".gitattributes", "scripts/agent/** -text\n.github/workflows/agent-*.yml -text\n");
  dst.commit("attributes");
  assert.deepEqual(sync({ from: src.dir, to: dst.dir }).warnings, []);
});

test("sync: only from the upstream's default branch, unless told it is a trial", (t) => {
  // The copy's CI looks the recorded commit up on upstream's default branch.
  // A local commit is not there at all; a pull request's head is there and
  // must not count.
  const src = source(t);
  const dst = destination(t);
  src.write("scripts/agent/keep.mjs", "export const v = 3;\n");
  src.write(MANIFEST_PATH, buildManifest(src.dir));
  const unpushed = src.commit("not pushed, not merged");
  const before = snapshot(dst.dir);
  assert.throws(
    () => sync({ from: src.dir, to: dst.dir }),
    new RegExp(`source's HEAD \\(${unpushed}\\) is not contained in origin/trunk as last fetched[\\s\\S]*git fetch origin[\\s\\S]*--allow-unmerged`),
  );
  assert.deepEqual(snapshot(dst.dir), before);

  // The trial switch proceeds and says, loudly, what it costs.
  const trial = sync({ from: src.dir, to: dst.dir, allowUnmerged: true });
  assert.equal(JSON.parse(dst.read(UPSTREAM_PATH)).commit, unpushed);
  assert.match(trial.warnings[0], /^NOT ON THE DEFAULT BRANCH[\s\S]*WILL FAIL[\s\S]*Do not merge this sync/);

  // Once that commit is on the default branch, it is an ordinary sync.
  const dst2 = destination(t);
  src.publish();
  assert.deepEqual(sync({ from: src.dir, to: dst2.dir }).warnings.filter((w) => /DEFAULT BRANCH/.test(w)), []);

  // A source that does not know its remote's default branch is not assumed to be on it.
  const src3 = source(t);
  src3.git("symbolic-ref", "--delete", "refs/remotes/origin/HEAD");
  assert.throws(() => sync({ from: src3.dir, to: destination(t).dir }), /does not know its remote's default branch[\s\S]*git remote set-head origin --auto/);
});

test("sync: a file git does not track under the core is never deleted, overwritten or stranded", (t) => {
  // `git status` does not list ignored files, and quotes names that are not
  // plain ASCII. Either way the file is one git could not give back.
  const cases = [
    ["an ignored file", (dst) => {
      dst.write(".gitignore", "*.log\n");
      dst.commit("ignore logs");
      dst.write("scripts/agent/debug.log", "hours of output\n");
    }, "scripts/agent/debug.log"],
    ["an ignored file where the source has one", (dst) => {
      dst.write(".gitignore", "scripts/agent/added.mjs\n");
      dst.commit("ignore it");
      dst.write("scripts/agent/added.mjs", "// local\n");
    }, "scripts/agent/added.mjs"],
    ["an untracked file with a non-ASCII name", (dst) => dst.write("scripts/agent/한글.mjs", "// 메모\n"), "scripts/agent/한글.mjs"],
    ["an untracked workflow", (dst) => dst.write(".github/workflows/agent-local.yml", "name: local\n"), ".github/workflows/agent-local.yml"],
  ];
  for (const [what, arrange, rel] of cases) {
    const src = source(t);
    const dst = destination(t);
    arrange(dst);
    const before = snapshot(dst.dir);
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), (err) => /files under the core that git does not track \(ignored ones count\)/.test(err.message) && err.message.includes(`  ${rel}`), what);
    assert.deepEqual(snapshot(dst.dir), before, `${what}: nothing may change`);
  }
  // A MODIFIED tracked file with such a name is caught as modified.
  {
    const src = source(t);
    const dst = destination(t);
    dst.write("scripts/agent/한글.mjs", "// v1\n");
    dst.commit("a tracked non-ASCII name");
    dst.write("scripts/agent/한글.mjs", "// edited by hand\n");
    const before = snapshot(dst.dir);
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), (err) => /destination has uncommitted changes under the core/.test(err.message) && err.message.includes("scripts/agent/한글.mjs"));
    assert.deepEqual(snapshot(dst.dir), before);
    // Committed, it is an ordinary core file the source lacks: deleted, and git has it.
    dst.commit("commit the edit");
    assert.ok(sync({ from: src.dir, to: dst.dir }).deleted.includes("scripts/agent/한글.mjs"));
  }
  // What the rule never calls core is left exactly where it is.
  {
    const src = source(t);
    const dst = destination(t);
    dst.write("scripts/agent/node_modules/dep/index.js", "installed\n");
    dst.write("scripts/agent/.scratch/notes.txt", "mine\n");
    sync({ from: src.dir, to: dst.dir });
    assert.equal(dst.read("scripts/agent/node_modules/dep/index.js"), "installed\n");
    assert.equal(dst.read("scripts/agent/.scratch/notes.txt"), "mine\n");
  }
});

test("sync: a symbolic link where the core goes is refused before anything is written", (t) => {
  const outside = mkdtempSync(path.join(os.tmpdir(), "sync-outside-"));
  t.after(() => rmSync(outside, { recursive: true, force: true }));
  writeFileSync(path.join(outside, "f"), "not the repository's\n");
  mkdirSync(path.join(outside, "dir"));
  writeFileSync(path.join(outside, "dir", "x.md"), "not the repository's either\n");

  const cases = [
    // A committed file link at a path the source has a file for.
    ["a link at a file the sync would write", (dst) => {
      rmSync(path.join(dst.dir, "scripts/agent/keep.mjs"));
      symlinkSync(path.join(outside, "f"), path.join(dst.dir, "scripts/agent/keep.mjs"));
    }, /scripts\/agent\/keep\.mjs is a symbolic link/],
    // A committed directory link on the way to one.
    ["a linked directory on the way to a file", (dst) => {
      rmSync(path.join(dst.dir, "scripts/agent/lenses"), { recursive: true });
      symlinkSync(path.join(outside, "dir"), path.join(dst.dir, "scripts/agent/lenses"));
    }, /scripts\/agent\/lenses is a symbolic link/],
    // A link the source has no file for: the walk that lists what to delete must not follow it.
    ["a stray link under the core", (dst) => symlinkSync(path.join(outside, "f"), path.join(dst.dir, "scripts/agent/x")), /scripts\/agent\/x is a symbolic link/],
    ["the package directory itself", (dst) => {
      rmSync(path.join(dst.dir, "scripts/agent"), { recursive: true });
      symlinkSync(path.join(outside, "dir"), path.join(dst.dir, "scripts/agent"));
    }, /scripts\/agent is a symbolic link/],
    ["the record's directory", (dst) => {
      rmSync(path.join(dst.dir, ".github/agent-harness"), { recursive: true });
      symlinkSync(path.join(outside, "dir"), path.join(dst.dir, ".github/agent-harness"));
    }, /\.github\/agent-harness is a symbolic link/],
    ["a directory where a file goes", (dst) => {
      rmSync(path.join(dst.dir, "scripts/agent/keep.mjs"));
      mkdirSync(path.join(dst.dir, "scripts/agent/keep.mjs"));
      writeFileSync(path.join(dst.dir, "scripts/agent/keep.mjs/inner"), "x\n");
    }, /scripts\/agent\/keep\.mjs is not a regular file/],
  ];
  for (const [what, arrange, why] of cases) {
    const src = source(t);
    const dst = destination(t);
    arrange(dst);
    dst.commit(what); // committed, so the "uncommitted changes" check is not what stops it
    const before = snapshot(dst.dir);
    const outsideBefore = snapshot(outside);
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), why, what);
    assert.deepEqual(snapshot(dst.dir), before, `${what}: the destination must be untouched`);
    assert.deepEqual(snapshot(outside), outsideBefore, `${what}: nothing outside the repository may be written`);
  }
});

test("sync: refuses, and changes nothing, when a precondition fails", (t) => {
  const untouched = (dst) => {
    assert.equal(dst.read("scripts/agent/keep.mjs"), "export const v = 1;\n");
    assert.equal(dst.has("scripts/agent/gone.mjs"), true);
    assert.equal(dst.has(UPSTREAM_PATH), false);
  };

  // The source's tree is not the one its manifest describes.
  {
    const src = source(t);
    const dst = destination(t);
    src.write("scripts/agent/keep.mjs", "export const v = 3;\n");
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), /source's tree does not match[\s\S]*changed: scripts\/agent\/keep\.mjs/);
    untouched(dst);
  }
  // …or matches it, but neither is committed: HEAD would not describe the bytes.
  {
    const src = source(t);
    const dst = destination(t);
    src.write("scripts/agent/keep.mjs", "export const v = 3;\n");
    src.write(MANIFEST_PATH, buildManifest(src.dir));
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), /source has uncommitted changes under the core/);
    untouched(dst);
  }
  // The source is itself a copy.
  {
    const src = source(t);
    const dst = destination(t);
    src.write(UPSTREAM_PATH, JSON.stringify({ repository: "org/origin", commit: "0123456789abcdef0123456789abcdef01234567", manifestDigest: "a".repeat(64) }));
    src.commit("pretend to be a copy");
    src.publish();
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), /source is itself a copy \(of org\/origin\)/);
    untouched(dst);
  }
  // The destination has work under the core that git could not give back.
  for (const dirty of [
    (dst) => dst.write("scripts/agent/keep.mjs", "export const v = 'edited by hand';\n"),
    (dst) => dst.write("scripts/agent/untracked.mjs", "x\n"),
    (dst) => dst.write(".github/workflows/agent-one.yml", "name: edited\n"),
  ]) {
    const src = source(t);
    const dst = destination(t);
    dirty(dst);
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), /destination has (?:uncommitted changes under the core|files under the core that git does not track)/);
    assert.equal(dst.has("scripts/agent/gone.mjs"), true);
    assert.equal(dst.has(UPSTREAM_PATH), false);
  }
  // Uncommitted work OUTSIDE the core is none of its business.
  {
    const src = source(t);
    const dst = destination(t);
    dst.write(".github/workflows/ci.yml", "name: CI, being edited\n");
    dst.write("scripts/own.mjs", "// being edited\n");
    dst.write(".github/agent-harness/config.json", JSON.stringify(FIXTURE_CONFIG, null, 2));
    sync({ from: src.dir, to: dst.dir });
    assert.equal(dst.read(".github/workflows/ci.yml"), "name: CI, being edited\n");
    assert.equal(dst.read("scripts/own.mjs"), "// being edited\n");
  }
  // Not checkouts, the same checkout, an origin that names no repository.
  {
    const src = source(t);
    const dst = destination(t);
    assert.throws(() => sync({ from: path.join(src.dir, "scripts"), to: dst.dir }), /--from .* is not the root of a git checkout/);
    assert.throws(() => sync({ from: src.dir, to: os.tmpdir() }), /--to .* is not the root of a git checkout/);
    assert.throws(() => sync({ from: src.dir, to: src.dir }), /same checkout/);
    src.git("remote", "set-url", "origin", "/srv/git/upstream.git");
    assert.throws(() => sync({ from: src.dir, to: dst.dir }), /Pass --repository owner\/name/);
    untouched(dst);
    // …unless it is told.
    sync({ from: src.dir, to: dst.dir, repository: "fixture-org/fixture-upstream" });
    assert.equal(JSON.parse(dst.read(UPSTREAM_PATH)).repository, "fixture-org/fixture-upstream");
  }
});

test("slugFromRemote: GitHub URLs in the forms git writes them", () => {
  for (const url of ["https://github.com/org/repo.git", "https://github.com/org/repo", "git@github.com:org/repo.git", "ssh://git@github.com/org/repo.git", "https://github.com/org/repo/"]) {
    assert.equal(slugFromRemote(url), "org/repo", url);
  }
  assert.equal(slugFromRemote("https://github.com/org/repo.name.git"), "org/repo.name");
  for (const url of ["/srv/git/repo.git", "https://gitlab.com/org/repo.git", "", null]) assert.equal(slugFromRemote(url), null, String(url));
});

test("CLI: syncs into the repository named by --to and says what is left to do", (t) => {
  const src = source(t);
  const dst = destination(t, (c) => {
    delete c.repo.appSlug;
    return c;
  });
  const run = (...args) => spawnSync(process.execPath, [path.join(HERE, "sync.mjs"), ...args], { encoding: "utf8" });
  for (const args of [[], ["--to", dst.dir], ["--from"], ["--from", src.dir, "--force", "yes"], ["--allow-unmerged"]]) {
    const bad = run(...args);
    assert.equal(bad.status, 1, JSON.stringify(args));
    assert.match(bad.stderr, /usage: node sync\.mjs --from/);
  }
  const ok = run("--from", src.dir, "--to", dst.dir);
  assert.equal(ok.status, 0, ok.stderr);
  assert.match(ok.stdout, /Copied 5 files, deleted 3\./);
  assert.match(ok.stdout, /deleted: scripts\/agent\/gone\.mjs/);
  assert.match(ok.stdout, new RegExp(`upstream\\.json: fixture-org/fixture-upstream at ${src.head}`));
  assert.match(ok.stdout, /does not load under the new schema[\s\S]*- repo\.appSlug is missing/);
  // Staging comes before testing in what it tells the person to do next.
  assert.match(ok.stdout, /To do:\n {2}1\. Pin the upstream[\s\S]*\n {2}2\. Stage the result before testing — `git add /);
  assert.ok(!/NOT ON THE DEFAULT BRANCH/.test(ok.stdout));
  // A second run has uncommitted changes under the core to refuse.
  const again = run("--from", src.dir, "--to", dst.dir);
  assert.equal(again.status, 1);
  assert.match(again.stderr, /destination has (?:uncommitted changes under the core|files under the core that git does not track)/);
  // The trial switch is a flag, and its warning is printed.
  const dst2 = destination(t);
  src.write("scripts/agent/keep.mjs", "export const v = 4;\n");
  src.write(MANIFEST_PATH, buildManifest(src.dir));
  src.commit("ahead of the default branch");
  assert.equal(run("--from", src.dir, "--to", dst2.dir).status, 1);
  const trial = run("--from", src.dir, "--to", dst2.dir, "--allow-unmerged");
  assert.equal(trial.status, 0, trial.stderr);
  assert.match(trial.stdout, /\nWARNING: NOT ON THE DEFAULT BRANCH/);
});
