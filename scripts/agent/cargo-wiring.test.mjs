import { FIXTURE_CONFIG_DIR } from "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { fixtureGitEnv } from "./git-env.mjs";

// The cargo rule set, as `test-removals.mjs` and `fix-guard.mjs` apply it.
// Both read `tests.rules` once, at import, and the fixture configuration
// switches on `go` and `vitest` — so everything here runs in a process of its
// own, under a copy of the fixture with the cargo rules on
// (`fixtures/rule-set-probe.mjs`). The rules themselves, which read no
// configuration, are tested directly in `cargo-rules.test.mjs`.

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PROBE = path.join(HERE, "fixtures", "rule-set-probe.mjs");
const COUNTER = readFileSync(path.join(HERE, "fixtures", "cargo", "counter.rs"), "utf8");

/** The fixture configuration with the cargo rules on, beside vitest unless `rules` says otherwise. */
function cargoConfig(t, rules = ["cargo", "vitest"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "harness-cargo-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  cpSync(FIXTURE_CONFIG_DIR, dir, { recursive: true });
  const config = JSON.parse(readFileSync(path.join(dir, "config.json"), "utf8"));
  config.tests = {
    rules,
    cargo: {
      integrationTestDirs: ["crates/*/tests", "tests"],
      testOnlyFileGlobs: ["crates/core/src/sync_scenarios.rs"],
    },
  };
  config.fixGuard.replicatedRuleGlobs = ["crates/core/src/rules/*.rs"];
  writeFileSync(path.join(dir, "config.json"), JSON.stringify(config));
  return dir;
}

/** `{ key: [module, export, ...args] }` → `{ key: result }`, under the configuration in `dir`. */
function probe(dir, calls) {
  const run = spawnSync(process.execPath, [PROBE], {
    input: JSON.stringify(calls),
    encoding: "utf8",
    env: { ...process.env, AGENT_HARNESS_CONFIG_DIR: dir },
  });
  assert.equal(run.status, 0, run.stderr);
  return JSON.parse(run.stdout);
}

/** The patch git writes for `before` → `after`, from the first hunk header on, as the API gives it. */
function diffOf(t, before, after) {
  const dir = mkdtempSync(path.join(tmpdir(), "harness-diff-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(path.join(dir, "a.rs"), before);
  writeFileSync(path.join(dir, "b.rs"), after);
  const run = spawnSync("git", ["diff", "--no-index", "--no-color", "-U3", "a.rs", "b.rs"], { cwd: dir, encoding: "utf8", env: fixtureGitEnv(dir) });
  const at = run.stdout.indexOf("\n@@ ");
  assert.ok(at >= 0, "expected the two sides to differ");
  return run.stdout.slice(at + 1).replace(/\n$/, "");
}

function edit(src, from, to) {
  assert.equal(src.split(from).length, 2, `expected exactly one ${JSON.stringify(from)} in the source`);
  return src.replace(from, to);
}

/** One entry of a commit's file list for a modified Rust file, with the contents the report job attaches. */
function modified(t, filename, before, after, { contents = true } = {}) {
  return { filename, status: "modified", patch: diffOf(t, before, after), ...(contents ? { contents: { before, after } } : {}) };
}

// The edits a fix round makes, on the fixture source.
const WITH_REPRODUCER = edit(
  COUNTER,
  "    #[test]\n    fn merge_keeps",
  "    #[test]\n    #[ignore = \"still reproduces: merge drops the smaller count\"]\n    fn merge_is_commutative() {\n        assert_eq!(1 + 1, 2);\n    }\n\n    #[test]\n    fn merge_keeps",
);
const PRODUCT_CHANGED = edit(COUNTER, "self.counts.values().sum()", "self.counts.values().copied().sum()");
const NESTED = "pub mod inner {\n    pub fn two() -> u8 {\n        debug_assert!(true);\n        2\n    }\n\n    #[cfg(test)]\n    mod tests {\n        #[test]\n        fn is_two() {\n            assert_eq!(super::two(), 2);\n        }\n    }\n}\n";
const SMUGGLED = `${COUNTER}\n#[cfg(any(test, feature = "internal"))]\nmod extra {\n    pub fn reachable_in_release() {}\n}\n`;

test("with the cargo rules: which Rust files are tests as a whole, and which cargo runs by name", (t) => {
  const dir = cargoConfig(t);
  const paths = {
    // Integration-test targets: directly in a configured directory, or `<name>/main.rs`.
    "crates/core/tests/merge.rs": [true, true],
    "crates/core/tests/suite/main.rs": [true, true],
    "tests/workspace_level.rs": [true, true],
    // Helpers a target declares: test code, not a target.
    "crates/core/tests/support/mod.rs": [true, false],
    "crates/core/tests/suite/cases.rs": [true, false],
    "crates/core/tests/fixtures/pack.json": [true, false],
    // Unit-test modules in files of their own, by the convention's names.
    "crates/core/src/counter_tests.rs": [true, false],
    "crates/core/src/history/tests.rs": [true, false],
    "crates/core/src/merge_test.rs": [true, false],
    "src/test.rs": [true, false],
    // CARGO'S RULES AND NOTHING ELSE decide what a Rust path is. The
    // directory names that mean "test" in other languages do not: each of
    // these was a test file, and so in a fix round's scope, by its name.
    "crates/client/src/client/tests/reissue.rs": [false, false], // a module directory; list it in testOnlyFileGlobs
    "crates/core/src/test/x.rs": [false, false],
    "crates/core/src/__tests__/x.rs": [false, false],
    "crates/core/src/bin/evil_tests.rs": [false, false], // cargo builds every file here as a BINARY
    "crates/core/src/bin/tests/main.rs": [false, false],
    "crates/core/src/bin/tests.rs": [false, false],
    "crates/core/examples/merge_tests.rs": [false, false],
    "crates/core/benches/tests.rs": [false, false],
    "crates/core/tests/build.rs": [false, false],
    "crates/core/tests/nested/Cargo.toml": [false, false],
    "crates/core/tests/Cargo.lock": [false, false],
    "tests/.cargo/config.toml": [false, false],
    // Named in `tests.cargo.testOnlyFileGlobs`.
    "crates/core/src/sync_scenarios.rs": [true, false],
    // Source files — where the inline tests are. Not test files.
    "crates/core/src/counter.rs": [false, false],
    "crates/core/src/rules/merge.rs": [false, false],
    "crates/core/src/testing.rs": [false, false],
    "crates/core/src/contest.rs": [false, false],
    "crates/core/src/latests.rs": [false, false],
    "crates/core/benches/merge.rs": [false, false],
    "crates/core/examples/tests.rs": [false, false],
    "crates/core/build.rs": [false, false],
    "crates/core/Cargo.toml": [false, false],
    // A directory NAMED like a target directory, where no target can be.
    "crates/core/src/tests.rs/x.rs": [false, false],
    // The other enabled rule set is untouched.
    "scripts/agent/rounds.test.mjs": [true, true],
    "tools/interop/pack_test.ts": [true, true],
  };
  const calls = {};
  for (const f of Object.keys(paths)) {
    calls[`test ${f}`] = ["test-removals", "isTestFile", f];
    calls[`run ${f}`] = ["test-removals", "isRunnableTest", f];
  }
  const got = probe(dir, calls);
  for (const [f, [isTest, runnable]] of Object.entries(paths)) {
    assert.equal(got[`test ${f}`], isTest, `isTestFile(${f})`);
    assert.equal(got[`run ${f}`], runnable, `isRunnableTest(${f})`);
  }
});

test("with the cargo rules: the removal record sees a test inside a source file", (t) => {
  const dir = cargoConfig(t);
  const deleted = edit(COUNTER, "    #[test]\n    fn merge_keeps_the_larger_count() {\n        let mut a = Counter::default();\n        a.increase(\"x\", 4);\n        let mut b = Counter::default();\n        b.increase(\"x\", 1);\n        a.merge(&b);\n        assert_eq!(a.value(), 4);\n        assert!(a.label().starts_with(\"count\"));\n    }\n", "");
  const ignored = edit(COUNTER, "    #[test]\n    fn merge_keeps", "    #[test]\n    #[ignore]\n    fn merge_keeps");
  const src = "crates/core/src/counter.rs";
  const got = probe(dir, {
    // THE CASE THE FILE MODEL COULD NOT SEE: neither path is a test file.
    deleted: ["test-removals", "testRemovals", [{ filename: src, status: "modified", patch: diffOf(t, COUNTER, deleted) }]],
    ignored: ["test-removals", "testRemovals", [{ filename: src, status: "modified", patch: diffOf(t, COUNTER, ignored) }]],
    productOnly: ["test-removals", "testRemovals", [{ filename: src, status: "modified", patch: diffOf(t, COUNTER, PRODUCT_CHANGED) }]],
    added: ["test-removals", "testRemovals", [{ filename: src, status: "modified", patch: diffOf(t, COUNTER, WITH_REPRODUCER) }]],
    counted: ["test-removals", "countCases", diffOf(t, COUNTER, WITH_REPRODUCER), { file: src, status: "modified" }],
    // A source file deleted outright takes its inline tests with it…
    fileGone: ["test-removals", "testRemovals", [{ filename: src, status: "removed", patch: diffOf(t, COUNTER, "") }]],
    // …and one with no tests in it is not a test deleted.
    plainGone: ["test-removals", "testRemovals", [{ filename: "crates/core/src/lib.rs", status: "removed", patch: "@@ -1,2 +0,0 @@\n-pub mod counter;\n-pub use counter::Counter;" }]],
    // A diff too large to show is "unknown", for a source file as for a test file.
    unread: ["test-removals", "testRemovals", [{ filename: src, status: "modified" }]],
    // An integration target deleted, or renamed to where cargo does not look.
    targetGone: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/merge.rs", status: "removed", patch: "@@ -1,6 +0,0 @@\n-#[test]\n-fn a() {}\n-\n-#[tokio::test]\n-async fn b() {}\n-" }]],
    targetAway: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/off/merge.rs", status: "renamed", previous_filename: "crates/core/tests/merge.rs" }]],
    targetOff: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/merge.rs.off", status: "renamed", previous_filename: "crates/core/tests/merge.rs" }]],
    targetRenamed: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/merging.rs", status: "renamed", previous_filename: "crates/core/tests/merge.rs" }]],
    helperMoved: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/support/more/mod.rs", status: "renamed", previous_filename: "crates/core/tests/support/mod.rs" }]],
    // The declaration that reached a test file, removed from its parent — a
    // one-line edit to a file that is not a test file.
    orphaned: ["test-removals", "testRemovals", [{ filename: "crates/core/src/lib.rs", status: "modified", patch: "@@ -3,5 +3,3 @@\n pub mod counter;\n-#[cfg(test)]\n-mod counter_tests;\n pub mod merge;" }]],
    // Manifests.
    member: ["test-removals", "testRemovals", [{ filename: "Cargo.toml", status: "modified", patch: '@@ -1,6 +1,5 @@\n [workspace]\n members = [\n     "crates/core",\n-    "crates/client",\n ]' }]],
    doctest: ["test-removals", "testRemovals", [{ filename: "crates/core/Cargo.toml", status: "modified", patch: "@@ -8,2 +8,3 @@\n [lib]\n+doctest = false\n path = \"src/lib.rs\"" }]],
    bench: ["test-removals", "testRemovals", [{ filename: "crates/core/Cargo.toml", status: "modified", patch: "@@ -8,2 +8,3 @@\n [[bench]]\n name = \"merge\"\n+harness = false" }]],
    notAManifest: ["test-removals", "testRemovals", [{ filename: "docs/Cargo.toml.md", status: "modified", patch: "+doctest = false" }]],
    // A gate in a NEW test file counts only when nothing can satisfy it.
    bornGated: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/unix_only.rs", status: "added", patch: "@@ -0,0 +1,3 @@\n+#![cfg(unix)]\n+#[test]\n+fn a() {}" }]],
    bornOff: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/never.rs", status: "added", patch: "@@ -0,0 +1,3 @@\n+#![cfg(any())]\n+#[test]\n+fn a() {}" }]],
    gated: ["test-removals", "testRemovals", [{ filename: "crates/core/tests/merge.rs", status: "modified", patch: '@@ -1,2 +1,3 @@\n+#![cfg(feature = "server")]\n #[test]\n fn a() {}' }]],
    // The JavaScript rules still read a JavaScript test in the same repository.
    js: ["test-removals", "testRemovals", [{ filename: "scripts/agent/a.test.mjs", status: "modified", patch: "-test('a', () => {\n+test.skip('a', () => {" }]],
    // …and a Rust idiom in a JavaScript file is not read with the Rust rules.
    jsNotRust: ["test-removals", "countCases", "-#[test]\n+#[ignore]", { file: "scripts/agent/a.test.mjs" }],
  });
  assert.deepEqual(got.deleted, [{ file: src, deleted: false, removed: 1, added: 0, suitesOff: 0 }]);
  assert.deepEqual(got.ignored, [{ file: src, deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(got.productOnly, []);
  // A reproducer kept behind `#[ignore]`: a case added AND one switched off —
  // the honest record beside a `--skipped` claim, a contradiction beside `--fixed`.
  assert.deepEqual(got.added, [{ file: src, deleted: false, removed: 0, added: 1, suitesOff: 1 }]);
  assert.deepEqual(got.counted, { removed: 0, added: 1, suitesOff: 1 });
  assert.deepEqual(got.fileGone, [{ file: src, deleted: true, removed: 2, added: 0, suitesOff: 0 }]);
  assert.deepEqual(got.plainGone, []);
  assert.deepEqual(got.unread, [{ file: src, deleted: false, removed: 0, added: 0, suitesOff: 0, unreadable: true }]);
  assert.deepEqual(got.targetGone, [{ file: "crates/core/tests/merge.rs", deleted: true, removed: 2, added: 0, suitesOff: 0 }]);
  for (const key of ["targetAway", "targetOff"]) {
    assert.deepEqual(got[key], [{ file: "crates/core/tests/merge.rs", deleted: true, removed: 0, added: 0, suitesOff: 0, unreadable: true }], key);
  }
  assert.deepEqual(got.targetRenamed, [], "still a target under its new name");
  assert.deepEqual(got.helperMoved, [], "a helper was never run by name");
  assert.deepEqual(got.orphaned, [{ file: "crates/core/src/lib.rs", deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(got.member, [{ file: "Cargo.toml", deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(got.doctest, [{ file: "crates/core/Cargo.toml", deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(got.bench, []);
  assert.deepEqual(got.notAManifest, []);
  assert.deepEqual(got.bornGated, []);
  assert.deepEqual(got.bornOff, [{ file: "crates/core/tests/never.rs", deleted: false, removed: 0, added: 1, suitesOff: 1 }]);
  assert.deepEqual(got.gated, [{ file: "crates/core/tests/merge.rs", deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(got.js, [{ file: "scripts/agent/a.test.mjs", deleted: false, removed: 1, added: 0, suitesOff: 0 }]);
  assert.deepEqual(got.jsNotRust, { removed: 0, added: 0, suitesOff: 0 });
});

test("with the cargo rules: switch-offs are netted across a round's commits", (t) => {
  const dir = cargoConfig(t);
  const commit = (...files) => ({ sha: "x", parents: [{}], files });
  const src = "crates/core/src/counter.rs";
  const lib = "crates/core/src/lib.rs";
  const got = probe(dir, {
    // An `#[ignore]` written in one commit and taken out in the next: the
    // fixer got the reproducer to pass. Nothing was switched off.
    ignoreAndBack: ["test-removals", "aggregateCommits", [
      commit({ filename: src, status: "modified", patch: " #[test]\n+#[ignore]\n fn a() {" }),
      commit({ filename: src, status: "modified", patch: " #[test]\n-#[ignore]\n fn a() {" }),
    ]],
    // A declaration removed and put back.
    modAndBack: ["test-removals", "aggregateCommits", [
      commit({ filename: lib, status: "modified", patch: "-#[cfg(test)]\n-mod counter_tests;" }),
      commit({ filename: lib, status: "modified", patch: "+#[cfg(test)]\n+mod counter_tests;" }),
    ]],
    // A test file added in the round and then gated: it was new in the round,
    // so only a gate nothing satisfies counts.
    newThenGated: ["test-removals", "aggregateCommits", [
      commit({ filename: "crates/core/tests/new.rs", status: "added", patch: "+#[test]\n+fn a() {}" }),
      commit({ filename: "crates/core/tests/new.rs", status: "modified", patch: "+#![cfg(unix)]\n #[test]" }),
    ]],
    newThenOff: ["test-removals", "aggregateCommits", [
      commit({ filename: "crates/core/tests/new.rs", status: "added", patch: "+#[test]\n+fn a() {}" }),
      commit({ filename: "crates/core/tests/new.rs", status: "modified", patch: "+#![cfg(any())]\n #[test]" }),
    ]],
    // A test written into a source file and deleted again inside the round —
    // what a compare of the round's two ends cannot see.
    writtenAndDeleted: ["test-removals", "aggregateCommits", [
      commit({ filename: src, status: "modified", patch: "+    #[test]\n+    fn reproducer() {\n+        assert!(false);\n+    }" }),
      commit({ filename: src, status: "modified", patch: "-    #[test]\n-    fn reproducer() {\n-        assert!(false);\n-    }" }),
    ]],
  });
  assert.deepEqual(got.ignoreAndBack, []);
  assert.deepEqual(got.modAndBack, []);
  assert.deepEqual(got.newThenGated, []);
  assert.deepEqual(got.newThenOff, [{ file: "crates/core/tests/new.rs", deleted: false, removed: 0, added: 1, suitesOff: 1 }]);
  // One added, one removed: no NET loss, so not a removal. (The same holds
  // for Go and JavaScript: the record is of what the round took away.)
  assert.deepEqual(got.writtenAndDeleted, []);
});

test("with the cargo rules: a fix round may change the tests inside a Rust file outside the PR's diff", (t) => {
  const dir = cargoConfig(t);
  const original = { $set: ["crates/core/src/merge.rs"] };
  const src = "crates/core/src/counter.rs";
  const lib = "crates/core/src/lib.rs";
  const LIB = "pub mod counter;\npub mod merge;\n";
  const LIB_DECLARES = "pub mod counter;\n#[cfg(test)]\nmod counter_tests;\npub mod merge;\n";
  const got = probe(dir, {
    // By path: the original files, what the CONFIGURATION names as test
    // code, docs, and test data that is not cargo's to build.
    paths: ["fix-guard", "scopeViolations", [
      { filename: "crates/core/src/merge.rs", status: "modified" },
      { filename: "crates/core/tests/merge.rs", status: "modified" },
      { filename: "crates/core/tests/support/mod.rs", status: "added" },
      { filename: "crates/core/tests/fixtures/pack.json", status: "added" },
      { filename: "crates/core/src/sync_scenarios.rs", status: "modified" },
      { filename: "docs/tasks/active/x-lessons.md", status: "modified" },
    ], original],
    // NOT by path, though each has "test" or "docs" in it: what cargo builds
    // into a binary, a library module, a build script or a package.
    notByPath: ["fix-guard", "scopeViolations", [
      "crates/core/src/bin/evil_tests.rs", "crates/core/src/bin/tests/main.rs", "crates/core/src/test/x.rs", "crates/core/src/docs/x.rs",
      "docs/examples/demo.rs", "crates/core/tests/build.rs", "crates/core/tests/nested/Cargo.toml", "docs/Cargo.toml", "Cargo.lock",
      "tests/.cargo/config.toml", "crates/core/examples/merge_tests.rs",
    ].map((filename) => ({ filename, status: "added", declared: true })), original],
    // A unit-test file named by convention is in scope when its parent
    // module declares it under cfg(test) — read, not assumed.
    byNameDeclared: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/counter_tests.rs", status: "added", declared: true }, { filename: "crates/core/src/history/tests.rs", status: "modified", declared: true }], original],
    byNameUndeclared: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/counter_tests.rs", status: "added", declared: false }], original],
    byNameUnread: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/counter_tests.rs", status: "added" }], original],
    byNameRenamed: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/counter_tests.rs", status: "renamed", previous_filename: "crates/core/src/counter.rs", declared: true }], original],
    // The hook's first answer is the same: not by the name.
    hookByName: ["fix-guard", "prepushVerdict", { touched: ["crates/core/src/counter_tests.rs", "crates/core/src/bin/evil_tests.rs", "crates/core/tests/merge.rs"], allowed: original }],
    // THE THREE PLACES A RUST REPRODUCER GOES, none of them in the PR's diff:
    // an existing `mod tests`…
    inline: ["fix-guard", "scopeViolations", [modified(t, src, COUNTER, WITH_REPRODUCER)], original],
    // …a new file, with its one declaration line in the parent module…
    newFile: ["fix-guard", "scopeViolations", [
      modified(t, lib, LIB, LIB_DECLARES),
      { filename: "crates/core/src/counter_tests.rs", status: "added", patch: "@@ -0,0 +1,2 @@\n+#[test]\n+fn a() {}", declared: true },
    ], original],
    // …and a first `mod tests` at the end of a file that had none.
    firstModule: ["fix-guard", "scopeViolations", [modified(t, lib, LIB, `${LIB}\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn a() {}\n}\n`)], original],
    // What is still out of scope.
    product: ["fix-guard", "scopeViolations", [modified(t, src, COUNTER, PRODUCT_CHANGED)], original],
    smuggled: ["fix-guard", "scopeViolations", [modified(t, src, COUNTER, SMUGGLED)], original],
    plainMod: ["fix-guard", "scopeViolations", [modified(t, lib, LIB, "pub mod counter;\nmod counter_tests;\npub mod merge;\n")], original],
    // No contents, no proof: the same test-only edit, unread, pages.
    unread: ["fix-guard", "scopeViolations", [modified(t, src, COUNTER, WITH_REPRODUCER, { contents: false })], original],
    halfRead: ["fix-guard", "scopeViolations", [{ ...modified(t, src, COUNTER, WITH_REPRODUCER), contents: { before: null, after: WITH_REPRODUCER } }], original],
    // A new source file is new product surface, even if all of it is a test module.
    added: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/extra.rs", status: "added", patch: "+#[cfg(test)]\n+mod tests {}", contents: { before: "", after: "#[cfg(test)]\nmod tests {}\n" } }], original],
    renamed: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/tally.rs", status: "renamed", previous_filename: src, contents: { before: COUNTER, after: COUNTER } }], original],
    removed: ["fix-guard", "scopeViolations", [{ filename: src, status: "removed", contents: { before: COUNTER, after: "" } }], original],
    // One commit that kept to the tests and one that did not: the path is out.
    twoCommits: ["fix-guard", "scopeViolations", [modified(t, src, COUNTER, WITH_REPRODUCER), modified(t, src, WITH_REPRODUCER, edit(WITH_REPRODUCER, "self.counts.values().sum()", "0"))], original],
    // A file that is not Rust gets no such reading.
    notRust: ["fix-guard", "scopeViolations", [{ filename: "crates/core/src/counter.go", status: "modified", contents: { before: COUNTER, after: WITH_REPRODUCER } }], original],
    // The whole round, as the report job judges it.
    round: ["fix-guard", "lintRound", {
      files: [
        modified(t, src, COUNTER, WITH_REPRODUCER),
        modified(t, lib, LIB, LIB_DECLARES),
        { filename: "crates/core/src/counter_tests.rs", status: "added", patch: "@@ -0,0 +1,2 @@\n+#[test]\n+fn a() {}", declared: true },
        { filename: "crates/core/src/merge.rs", status: "modified", patch: "@@ -1 +1 @@\n-a\n+b" },
      ],
      original,
    }],
    badRound: ["fix-guard", "lintRound", { files: [modified(t, src, COUNTER, SMUGGLED)], original }],
  });
  assert.deepEqual(got.paths, []);
  assert.deepEqual(got.notByPath, [
    "Cargo.lock", "crates/core/examples/merge_tests.rs", "crates/core/src/bin/evil_tests.rs", "crates/core/src/bin/tests/main.rs", "crates/core/src/docs/x.rs",
    "crates/core/src/test/x.rs", "crates/core/tests/build.rs", "crates/core/tests/nested/Cargo.toml", "docs/Cargo.toml", "docs/examples/demo.rs", "tests/.cargo/config.toml",
  ]);
  assert.deepEqual(got.byNameDeclared, []);
  assert.deepEqual(got.byNameUndeclared, ["crates/core/src/counter_tests.rs"]);
  assert.deepEqual(got.byNameUnread, ["crates/core/src/counter_tests.rs"], "no declaration read, no proof");
  assert.deepEqual(got.byNameRenamed, ["crates/core/src/counter.rs", "crates/core/src/counter_tests.rs"]);
  assert.deepEqual(got.hookByName, { ok: false, violations: ["crates/core/src/bin/evil_tests.rs", "crates/core/src/counter_tests.rs"] });
  assert.deepEqual(got.inline, []);
  assert.deepEqual(got.newFile, []);
  assert.deepEqual(got.firstModule, []);
  assert.deepEqual(got.product, [src]);
  assert.deepEqual(got.smuggled, [src]);
  assert.deepEqual(got.plainMod, [lib], "a module declared without cfg(test) is compiled into the product");
  assert.deepEqual(got.unread, [src]);
  assert.deepEqual(got.halfRead, [src]);
  assert.deepEqual(got.added, ["crates/core/src/extra.rs"]);
  assert.deepEqual(got.renamed, [src, "crates/core/src/tally.rs"]);
  assert.deepEqual(got.removed, [src]);
  assert.deepEqual(got.twoCommits, [src]);
  assert.deepEqual(got.notRust, ["crates/core/src/counter.go"]);
  assert.equal(got.round.blocking, false);
  assert.deepEqual(got.round.violations, []);
  assert.equal(got.badRound.blocking, true);
  assert.deepEqual(got.badRound.violations, [src]);
});

test("with the cargo rules: a file named like a test is one only when its parent module says so", (t) => {
  const dir = cargoConfig(t);
  const file = "crates/core/src/rules/merge_tests.rs";
  const at = (table) => ({ $read: Object.fromEntries(Object.entries(table).map(([k, v]) => [`${k}:undefined`, v])) });
  const declared = "pub mod merge;\n#[cfg(test)]\nmod merge_tests;\n";
  const got = probe(dir, {
    // Each place a parent can be.
    inModRs: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": declared })],
    besideTheDirectory: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules.rs": declared })],
    inLibRs: ["fix-guard", "rustDeclared", "crates/core/src/merge_tests.rs", at({ "crates/core/src/lib.rs": declared })],
    inMainRs: ["fix-guard", "rustDeclared", "src/tests.rs", at({ "src/main.rs": "#[cfg(test)]\nmod tests;\n" })],
    // THE CASE THE NAME GOT WRONG: the parent compiles it into the library.
    plain: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": "pub mod merge;\nmod merge_tests;\n" })],
    // Declared twice, once for real.
    both: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": `${declared}pub mod merge_tests;\n` })],
    widened: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": '#[cfg(any(test, feature = "x"))]\nmod merge_tests;\n' })],
    // A `#[path]` makes the name mean some other file.
    repointed: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": '#[cfg(test)]\n#[path = "elsewhere.rs"]\nmod merge_tests;\n' })],
    anotherName: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": "#[cfg(test)]\nmod other_tests;\n" })],
    nested: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": "mod inner {\n    #[cfg(test)]\n    mod merge_tests;\n}\n" })],
    noParent: ["fix-guard", "rustDeclared", file, at({})],
    unlexable: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/rules/mod.rs": `${declared}"` })],
    // A parent that is not this file's parent does not speak for it.
    wrongParent: ["fix-guard", "rustDeclared", file, at({ "crates/core/src/lib.rs": declared, "crates/core/src/rules/merge.rs": declared })],
    // Only a by-name unit-test file is asked about at all.
    notByName: ["fix-guard", "rustDeclared", "crates/core/src/bin/merge_tests.rs", at({ "crates/core/src/bin/mod.rs": declared })],
    // The report job: one answer per path, at the round's head.
    attached: ["fix-guard", "attachRustDeclarations", [
      { filename: file, status: "added" }, { filename: file, status: "modified" },
      { filename: "crates/core/src/counter_tests.rs", status: "added" },
      { filename: "crates/core/src/counter.rs", status: "modified" },
    ], { read: at({ "crates/core/src/rules/mod.rs": declared, "crates/core/src/lib.rs": "mod counter_tests;\n" }), budget: { left: 10 } }],
    capped: ["fix-guard", "attachRustDeclarations", [{ filename: file, status: "added" }, { filename: "crates/core/src/counter_tests.rs", status: "added" }],
      { read: at({ "crates/core/src/rules/mod.rs": declared }), budget: { left: 1 } }],
  });
  for (const key of ["inModRs", "besideTheDirectory", "inLibRs", "inMainRs"]) assert.equal(got[key], true, key);
  for (const key of ["plain", "both", "widened", "repointed", "anotherName", "nested", "noParent", "unlexable", "wrongParent", "notByName"]) assert.equal(got[key], false, key);
  assert.deepEqual(got.attached.map((f) => f.declared ?? null), [true, true, false, null]);
  assert.deepEqual(got.capped.map((f) => f.declared ?? null), [true, null], "past the budget a file is unread, and so out of scope");
});

test("with the cargo rules: the page and the hook say what a Rust file allows", (t) => {
  const dir = cargoConfig(t);
  const result = { blocking: true, violations: ["crates/core/src/counter.rs"], reviewerDirected: [], weakened: [], crdt: [], scopeKnown: true };
  const got = probe(dir, { page: ["fix-guard", "renderFixGuard", result] });
  assert.match(got.page, /plus tests and docs\. In a Rust file outside that diff, only a top-level `#\[cfg\(test\)\]` module may change/);
  // …and a repository without the cargo rules is told nothing about Rust.
  const plain = probe(FIXTURE_CONFIG_DIR, { page: ["fix-guard", "renderFixGuard", result] });
  assert.match(plain.page, /plus tests and docs\. A finding that needs any other file/);
  assert.doesNotMatch(plain.page, /Rust|cfg\(test\)/);
});

test("with the cargo rules: the assertion lint reads Rust idioms, in test files and in test regions", (t) => {
  const dir = cargoConfig(t);
  const src = "crates/core/src/counter.rs";
  const gutted = edit(edit(COUNTER, "        assert_eq!(a.value(), 4);\n", ""), "        assert!(a.label().starts_with(\"count\"));\n", "        assert!(true);\n");
  const productAssertGone = edit(COUNTER, "        debug_assert!(self.value() >= other.value());\n", "");
  const patch = (...lines) => ["@@ -1,9 +1,9 @@", ...lines].join("\n");
  const got = probe(dir, {
    // A test file by its path: every line is test code.
    file: ["fix-guard", "weakenedAssertions", [{ filename: "crates/core/tests/merge.rs", patch: patch("-    assert_eq!(a.value(), 4);", "-    assert_ne!(a, b);", "-    assert_matches!(r, Ok(_));", "-    debug_assert!(x);", "-    prop_assert!(y);", "-    panic!(\"unreachable state\");", "-    unreachable!();", "+    assert!(a.value() > 0);") }]],
    // Not assertions: how a `Result` test reports, and the JavaScript idioms.
    notAssertions: ["fix-guard", "weakenedAssertions", [{ filename: "crates/core/tests/merge.rs", patch: patch("-    let v = parse(s).expect(\"parses\");", "-    let w = parse(s).unwrap();", "-    check(&v)?;", "-    expect(v).toBe(1);", "-    assert(v);", "-    assertion_count += 1;") }]],
    // A body replaced by `todo!()` shows as the assertions it replaced.
    todo: ["fix-guard", "weakenedAssertions", [{ filename: "crates/core/tests/merge.rs", patch: patch("-    assert_eq!(a.value(), 4);", "-    assert!(ok);", "+    todo!()") }]],
    tautologies: ["fix-guard", "weakenedAssertions", [{
      filename: "crates/core/src/counter_tests.rs",
      patch: patch(
        "+    assert!(true);", "+    assert!(true, \"always\");", "+    assert!(!false);", "+    debug_assert!(true);",
        "+    assert_eq!(1, 1);", "+    assert_eq!(a.b, a.b, \"same\");", "+    assert_eq!(true, true);",
        "+    assert_ne!(true, false);", "+    assert_ne!(false, true);", "+    assert_matches!(r, _);",
        "+    assert_eq!(root.to_json(), root.to_json());", "+    assert_eq!(f(), f());", "+    assert_eq!(a.get(1), a.get(1), \"same\");",
        // Not tautologies.
        "+    assert!(truth);", "+    assert_eq!(a, a.b);", "+    assert_eq!(a.len(), b.len());", "+    assert_eq!(a, b);", "+    assert_ne!(a, false);", "+    assert_matches!(r, Ok(_));", "+    assert_eq!(a.get(1), a.get(2));", "+    assert_eq!(f(), g());",
      ),
    }]],
    // A SOURCE file, with its contents: only what is inside a test region.
    inline: ["fix-guard", "weakenedAssertions", [modified(t, src, COUNTER, gutted)]],
    productAssert: ["fix-guard", "weakenedAssertions", [modified(t, src, COUNTER, productAssertGone)]],
    // …and without them, every line: a warning that names a file it could
    // not read, rather than a weakened test never looked at.
    productAssertUnread: ["fix-guard", "weakenedAssertions", [modified(t, src, COUNTER, productAssertGone, { contents: false })]],
    inlineUnread: ["fix-guard", "weakenedAssertions", [modified(t, src, COUNTER, gutted, { contents: false })]],
    unlexable: ["fix-guard", "weakenedAssertions", [{ filename: src, patch: "@@ -1 +1 @@\n-assert!(x);", contents: { before: "\"", after: "\"" } }]],
    // A test module NESTED in another module is test code to the lint,
    // though the scope rule does not accept it as a region.
    nested: ["fix-guard", "weakenedAssertions", [modified(t, src, NESTED, edit(NESTED, "            assert_eq!(super::two(), 2);\n", ""))]],
    nestedProduct: ["fix-guard", "weakenedAssertions", [modified(t, src, NESTED, edit(NESTED, "        debug_assert!(true);\n", ""))]],
    // The other rule set's files keep their own idioms, and only theirs.
    js: ["fix-guard", "weakenedAssertions", [{ filename: "scripts/agent/a.test.mjs", patch: patch("-  expect(a).toBe(b);", "-  assert_eq!(a, b);", "+  expect(true).toBe(true);") }]],
  });
  assert.deepEqual(got.file, [{ file: "crates/core/tests/merge.rs", removed: 7, added: 1, tautologies: 0 }]);
  assert.deepEqual(got.notAssertions, []);
  assert.deepEqual(got.todo, [{ file: "crates/core/tests/merge.rs", removed: 2, added: 0, tautologies: 0 }]);
  assert.deepEqual(got.tautologies, [{ file: "crates/core/src/counter_tests.rs", removed: 0, added: 21, tautologies: 13 }]);
  assert.deepEqual(got.inline, [{ file: src, removed: 2, added: 1, tautologies: 1 }]);
  assert.deepEqual(got.productAssert, [], "an assertion in product code is not a test's");
  assert.deepEqual(got.nested, [{ file: src, removed: 1, added: 0, tautologies: 0 }]);
  assert.deepEqual(got.nestedProduct, []);
  assert.deepEqual(got.productAssertUnread, [{ file: src, removed: 1, added: 0, tautologies: 0 }]);
  assert.deepEqual(got.inlineUnread, [{ file: src, removed: 2, added: 1, tautologies: 1 }]);
  assert.deepEqual(got.unlexable, [{ file: src, removed: 1, added: 0, tautologies: 0 }]);
  assert.deepEqual(got.js, [{ file: "scripts/agent/a.test.mjs", removed: 1, added: 1, tautologies: 1 }]);
});

test("a rule set with nothing to match matches nothing, not everything", (t) => {
  // THE TRAP: with `cargo` the only rule set and no cargo entry in the
  // assertion table, the pattern was `new RegExp("")`, which matches every
  // line — every removed line a lost assertion, every added one a tautology.
  // Cargo alone now leaves the SHARED pattern with no rule set, and that must
  // read a JavaScript test file as holding no assertions at all.
  const dir = cargoConfig(t, ["cargo"]);
  const got = probe(dir, {
    js: ["fix-guard", "weakenedAssertions", [{ filename: "scripts/agent/a.test.mjs", patch: "-  expect(a).toBe(b);\n-  const x = 1;\n+  const y = 2;" }]],
    rust: ["fix-guard", "weakenedAssertions", [{ filename: "crates/core/tests/merge.rs", patch: "-    assert_eq!(a, b);\n-    let x = 1;\n+    let y = 2;" }]],
    jsCases: ["test-removals", "countCases", "-test('a', () => {", { file: "scripts/agent/a.test.mjs" }],
  });
  assert.deepEqual(got.js, []);
  assert.deepEqual(got.rust, [{ file: "crates/core/tests/merge.rs", removed: 1, added: 0, tautologies: 0 }]);
  assert.deepEqual(got.jsCases, { removed: 0, added: 0, suitesOff: 0 }, "the JavaScript rules are off");
});

test("with the cargo rules: a test-only edit inside a rule file is not a one-sided rule change", (t) => {
  const dir = cargoConfig(t);
  const rule = "crates/core/src/rules/merge.rs";
  const got = probe(dir, {
    testOnly: ["fix-guard", "crdtRuleChanges", [modified(t, rule, COUNTER, WITH_REPRODUCER)]],
    product: ["fix-guard", "crdtRuleChanges", [modified(t, rule, COUNTER, PRODUCT_CHANGED)]],
    // Unread: asked, not assumed.
    unread: ["fix-guard", "crdtRuleChanges", [modified(t, rule, COUNTER, WITH_REPRODUCER, { contents: false })]],
    // The rule directory's test files, by their names.
    testFiles: ["fix-guard", "crdtRuleChanges", [{ filename: "crates/core/src/rules/merge_tests.rs" }, { filename: "crates/core/src/rules/tests.rs" }]],
    mixed: ["fix-guard", "lintRound", { files: [modified(t, rule, COUNTER, WITH_REPRODUCER), modified(t, rule, WITH_REPRODUCER, edit(WITH_REPRODUCER, "self.counts.values().sum()", "0"))], original: { $set: [rule] } }],
    quiet: ["fix-guard", "lintRound", { files: [modified(t, rule, COUNTER, WITH_REPRODUCER)], original: { $set: [rule] } }],
  });
  assert.deepEqual(got.testOnly, []);
  assert.deepEqual(got.product, [rule]);
  assert.deepEqual(got.unread, [rule]);
  assert.deepEqual(got.testFiles, []);
  assert.deepEqual(got.mixed.crdt, [rule]);
  assert.deepEqual(got.quiet.crdt, []);
  assert.equal(got.quiet.blocking, false);
});

test("with the cargo rules: the report job reads both sides of each modified Rust source file", (t) => {
  const dir = cargoConfig(t);
  const files = () => [
    { filename: "crates/core/src/counter.rs", status: "modified" },
    { filename: "crates/core/src/counter_tests.rs", status: "modified" }, // a test file: in scope whole
    { filename: "crates/core/src/new.rs", status: "added" },
    { filename: "crates/core/src/old.rs", status: "removed" },
    { filename: "crates/core/src/moved.rs", status: "renamed", previous_filename: "crates/core/src/was.rs" },
    { filename: "scripts/agent/a.mjs", status: "modified" },
    { filename: "crates/core/src/merge.rs", status: "modified" },
  ];
  const read = { $read: { "parent1:crates/core/src/counter.rs": "A", "commit1:crates/core/src/counter.rs": "B", "parent1:crates/core/src/merge.rs": "C" } };
  const got = probe(dir, {
    all: ["fix-guard", "attachRustContents", files(), { sha: "commit1", parent: { $value: "parent1" }, read, budget: { left: 10 } }],
    // The budget is the round's: past it a file is left unread, and so out of scope.
    capped: ["fix-guard", "attachRustContents", files(), { sha: "commit1", parent: { $value: "parent1" }, read, budget: { left: 1 } }],
    // A parent that could not be read: no "before", so nothing is provable.
    orphan: ["fix-guard", "attachRustContents", files(), { sha: "commit1", parent: { $value: "" }, read, budget: { left: 10 } }],
    cap: ["fix-guard", "RUST_READ_CAP"],
  });
  const withContents = (list) => Object.fromEntries(list.filter((f) => f.contents).map((f) => [f.filename, f.contents]));
  assert.deepEqual(withContents(got.all), {
    "crates/core/src/counter.rs": { before: "A", after: "B" },
    "crates/core/src/merge.rs": { before: "C", after: null },
  });
  assert.deepEqual(Object.keys(withContents(got.capped)), ["crates/core/src/counter.rs"]);
  assert.deepEqual(withContents(got.orphan), {
    "crates/core/src/counter.rs": { before: null, after: "B" },
    "crates/core/src/merge.rs": { before: null, after: null },
  });
  assert.equal(got.cap, 80);
});

test("with the cargo rules: the pre-push hook's exemption, commit by commit", (t) => {
  const dir = cargoConfig(t);
  const src = "crates/core/src/counter.rs";
  const table = (pairs) => ({ $read: Object.fromEntries(pairs.map(([rev, text]) => [`${rev}:${src}`, text])) });
  const got = probe(dir, {
    testOnly: ["fix-guard", "rustExemption", src, ["c1"], table([["c1^", COUNTER], ["c1", WITH_REPRODUCER]])],
    product: ["fix-guard", "rustExemption", src, ["c1"], table([["c1^", COUNTER], ["c1", PRODUCT_CHANGED]])],
    // Every commit, not the net: a product change made and reverted is still
    // a commit that changed the product, as the report job will see it.
    madeAndReverted: ["fix-guard", "rustExemption", src, ["c1", "c2"], table([["c1^", COUNTER], ["c1", PRODUCT_CHANGED], ["c2^", PRODUCT_CHANGED], ["c2", COUNTER]])],
    twoTestOnly: ["fix-guard", "rustExemption", src, ["c1", "c2"], table([["c1^", COUNTER], ["c1", WITH_REPRODUCER], ["c2^", WITH_REPRODUCER], ["c2", COUNTER]])],
    created: ["fix-guard", "rustExemption", src, ["c1"], table([["c1", COUNTER]])],
    noCommits: ["fix-guard", "rustExemption", src, [], table([])],
    notRust: ["fix-guard", "rustExemption", "crates/core/src/counter.go", ["c1"], { $read: { "c1^:crates/core/src/counter.go": COUNTER, "c1:crates/core/src/counter.go": WITH_REPRODUCER } }],
    verdict: ["fix-guard", "prepushVerdict", { touched: [src, "crates/core/src/other.rs", "docs/a.md"], allowed: { $set: ["x"] }, exempt: { $read: {} } }],
  });
  assert.equal(got.testOnly, true);
  assert.equal(got.product, false);
  assert.equal(got.madeAndReverted, false);
  assert.equal(got.twoTestOnly, true);
  assert.equal(got.created, false);
  assert.equal(got.noCommits, false);
  assert.equal(got.notRust, false);
  assert.deepEqual(got.verdict, { ok: false, violations: ["crates/core/src/other.rs", src].sort() });
});

test("prepush CLI with the cargo rules: a reproducer in a file outside the scope is pushed, product code is not", (t) => {
  const config = cargoConfig(t);
  const dir = mkdtempSync(path.join(tmpdir(), "fix-guard-cargo-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const env = { ...fixtureGitEnv(dir), AGENT_HARNESS_CONFIG_DIR: config };
  const git = (...a) => execFileSync("git", ["-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", ...a], { cwd: dir, encoding: "utf8", env }).trim();
  const write = (file, text) => {
    mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
    writeFileSync(path.join(dir, file), text);
  };
  const commit = (message) => {
    git("add", ".");
    git("commit", "-q", "-m", message);
    return git("rev-parse", "HEAD");
  };
  git("init", "-q", "-b", "main");
  write("crates/core/src/counter.rs", COUNTER);
  write("crates/core/src/merge.rs", "pub fn merge() {}\n");
  write("crates/core/src/lib.rs", "pub mod counter;\npub mod merge;\n");
  const remote = commit("base");
  // Outside the work tree, so that `git add .` never commits it.
  const allowed = path.join(config, "allowed.txt");
  writeFileSync(allowed, "crates/core/src/merge.rs\n");
  const push = (local) => spawnSync(process.execPath, [path.join(HERE, "fix-guard.mjs"), "prepush", "--allowed", allowed], {
    cwd: dir, env, encoding: "utf8", input: `refs/heads/agent/x ${local} refs/heads/agent/x ${remote}\n`,
  });

  // The fix in the PR's own file; a reproducer in ANOTHER file's `mod tests`;
  // a new test file and the one line that declares it.
  write("crates/core/src/merge.rs", "pub fn merge() -> u8 { 1 }\n");
  write("crates/core/src/counter.rs", WITH_REPRODUCER);
  commit("fix, with an inline reproducer");
  write("crates/core/src/merge_tests.rs", "#[test]\nfn merges() {}\n");
  write("crates/core/src/lib.rs", "pub mod counter;\npub mod merge;\n#[cfg(test)]\nmod merge_tests;\n");
  const good = commit("a test file of its own");
  const allowedPush = push(good);
  assert.equal(allowedPush.status, 0, allowedPush.stderr);

  // One more commit, which also changes the product in the out-of-scope file.
  write("crates/core/src/counter.rs", edit(WITH_REPRODUCER, "self.counts.values().sum()", "0"));
  const bad = commit("and a product change");
  const refused = push(bad);
  assert.equal(refused.status, 1);
  assert.match(refused.stderr, /PUSH REFUSED[\s\S]*crates\/core\/src\/counter\.rs/);
  assert.doesNotMatch(refused.stderr, /lib\.rs|merge_tests\.rs|merge\.rs/);
  assert.match(refused.stderr, /In a Rust file outside that diff you may change ONLY a top-level `#\[cfg\(test\)\] mod`/);

  // Reverting it in a further commit does not un-refuse the push: the report
  // job reads each commit, and the hook must agree with it.
  write("crates/core/src/counter.rs", WITH_REPRODUCER);
  const reverted = commit("revert the product change");
  assert.equal(push(reverted).status, 1);

  // A test-named file that nothing declares under cfg(test), and one cargo
  // would build as a binary: neither is in scope by its name.
  git("checkout", "-q", "-B", "names", good);
  write("crates/core/src/extra_tests.rs", "pub fn shipped() {}\n");
  const undeclared = push(commit("a test-named file nobody declares"));
  assert.equal(undeclared.status, 1);
  assert.match(undeclared.stderr, /crates\/core\/src\/extra_tests\.rs/);
  git("checkout", "-q", "-B", "bins", good);
  write("crates/core/src/bin/evil_tests.rs", "fn main() {}\n");
  write("crates/core/tests/build.rs", "fn main() {}\n");
  const binary = push(commit("an auto-discovered binary with a test's name"));
  assert.equal(binary.status, 1);
  assert.match(binary.stderr, /crates\/core\/src\/bin\/evil_tests\.rs[\s\S]*crates\/core\/tests\/build\.rs/);
  assert.doesNotMatch(binary.stderr, /merge_tests\.rs/, "the declared one stays in scope");
  // The declaration removed again: the file it reached is out of scope with it.
  git("checkout", "-q", "-B", "undeclare", good);
  write("crates/core/src/lib.rs", "pub mod counter;\npub mod merge;\nmod merge_tests;\n");
  const plainMod = push(commit("declare it without cfg(test)"));
  assert.equal(plainMod.status, 1);
  assert.match(plainMod.stderr, /crates\/core\/src\/lib\.rs[\s\S]*crates\/core\/src\/merge_tests\.rs/);

  // Under the fixture configuration (no cargo rules) the first push is
  // refused as it always was: a Rust file is just a file outside the scope.
  const plain = spawnSync(process.execPath, [path.join(HERE, "fix-guard.mjs"), "prepush", "--allowed", allowed], {
    cwd: dir, env: { ...env, AGENT_HARNESS_CONFIG_DIR: FIXTURE_CONFIG_DIR }, encoding: "utf8", input: `refs/heads/agent/x ${good} refs/heads/agent/x ${remote}\n`,
  });
  assert.equal(plain.status, 1);
  assert.match(plain.stderr, /crates\/core\/src\/counter\.rs[\s\S]*crates\/core\/src\/lib\.rs[\s\S]*crates\/core\/src\/merge_tests\.rs/);
  assert.doesNotMatch(plain.stderr, /Rust/);
});
