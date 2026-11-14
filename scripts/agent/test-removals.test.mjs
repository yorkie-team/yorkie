import "./fixtures/use-fixture-config.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
import {
  isTestFile, countCases, testRemovals, serializeTestRemovals, collectTestRemovals, aggregateCommits, roundCommits,
  renderTestRemovals, TEST_REMOVALS_MARKER, capRoundCommits, describeRemoval, ROUND_COMMIT_CAP,
} from "./test-removals.mjs";

// #1426, e6900da: the fixer wrote a two-replica test for "the remote path does
// not re-point the history", watched it fail, DELETED it, and reported the
// finding "Fixed" with a caveat. Nothing mechanical noticed. These are the
// shapes that must be noticed.

test("isTestFile: the repo's test layouts, and nothing else", () => {
  for (const f of [
    "packages/sdk/test/unit/document/tree_redo_split_style_test.ts",
    "packages/sdk/test/integration/history_tree_split_test.ts",
    "scripts/agent/rounds.test.mjs",
    "packages/react/src/useDoc.spec.tsx",
    "packages/x/__tests__/a.ts",
  ]) assert.equal(isTestFile(f), true, f);
  for (const f of ["packages/sdk/src/document/document.ts", "docs/testing.md", "packages/sdk/src/test-utils.ts"]) {
    assert.equal(isTestFile(f), false, f);
  }
});

test("countCases: active cases removed vs added; skip and todo are not active", () => {
  const patch = [
    "@@ -1,9 +1,7 @@",
    "-  it('re-points the remote history after GC', async () => {",
    "-    test('nested', () => {});",
    "+  it.skip('re-points the remote history after GC', async () => {",
    "+  it.todo('later');",
    "   it('unchanged', () => {});",
    "+  it.fails('still reproduces', () => {});",
    "-  it.each([1, 2])('case %i', () => {});",
  ].join("\n");
  // `.fails` RUNS (it is how a fixer records a reproduction it could not fix),
  // so it counts as active; `.skip` and `.todo` do not run.
  assert.deepEqual(countCases(patch), { removed: 3, added: 1, suitesOff: 0 });
  assert.deepEqual(countCases(""), { removed: 0, added: 0, suitesOff: 0 });
  assert.deepEqual(countCases(undefined), { removed: 0, added: 0, suitesOff: 0 });
});

test("testRemovals: a deleted test file, and a test file that lost cases", () => {
  const files = [
    { filename: "packages/sdk/test/unit/remote_repoint_test.ts", status: "removed", patch: "-it('a', () => {});\n-it('b', () => {});" },
    { filename: "packages/sdk/test/unit/x_test.ts", status: "modified", patch: "-  it('a', () => {});\n+  it.skip('a', () => {});" },
    // A test file that GAINED cases, or moved them around, is not a removal.
    { filename: "packages/sdk/test/unit/y_test.ts", status: "modified", patch: "-  it('a', () => {});\n+  it('a renamed', () => {});\n+  it('b', () => {});" },
    // Source is never a test removal, whatever it deletes.
    { filename: "packages/sdk/src/document/document.ts", status: "modified", patch: "-  it('looks like a case', () => {});" },
  ];
  assert.deepEqual(testRemovals(files), [
    { file: "packages/sdk/test/unit/remote_repoint_test.ts", deleted: true, removed: 2, added: 0, suitesOff: 0 },
    { file: "packages/sdk/test/unit/x_test.ts", deleted: false, removed: 1, added: 0, suitesOff: 0 },
  ]);
  assert.deepEqual(testRemovals([]), []);
  assert.deepEqual(testRemovals(null), []);
});

const bot = (body, login = "github-actions[bot]") => ({ body, user: { login, type: "Bot" }, created_at: "2026-09-30T16:56:00Z" });

test("records round-trip, and only github-actions[bot] is believed", () => {
  const rec = { head: "6915bc6a7", after: "e6900da64", removals: [{ file: "a_test.ts", deleted: true, removed: 2, added: 0 }] };
  const body = renderTestRemovals(rec);
  assert.ok(body.includes(TEST_REMOVALS_MARKER));
  // The visible line a maintainer reads.
  assert.match(body, /removed or disabled tests in 1 file\(s\)/);
  assert.match(body, /deleted `a_test\.ts`/);
  assert.deepEqual(collectTestRemovals([bot(body)]), [{ head: "6915bc6a7", after: "e6900da64", removals: rec.removals.map((r) => ({ ...r, suitesOff: 0 })) }]);
  // The fixer's own identity (it can comment through the fix-report path) and a
  // human are not the pipeline: refused.
  assert.deepEqual(collectTestRemovals([bot(body, "fixture-agent[bot]")]), []);
  assert.deepEqual(collectTestRemovals([{ ...bot(body), user: { login: "github-actions[bot]", type: "User" } }]), []);
  assert.deepEqual(collectTestRemovals([bot("<!-- agent-fix-tests {nope} -->")]), []);
  // A file name cannot close the record.
  assert.doesNotMatch(serializeTestRemovals({ head: "a", after: "b", removals: [{ file: "x-->y_test.ts", deleted: true, removed: 1, added: 0 }] }).slice(0, -4), /-->/);
});

test("countCases: chained modifiers and tagged templates count; disabled suites are counted apart", () => {
  const patch = [
    "-  it.concurrent.each([1])('a %i', () => {});",
    "-  test.only.each`x | y`('b', () => {});",
    "-  test.sequential('c', () => {});",
    "-  test.for([1])('d', () => {});",
    "+describe.skip('suite', () => {",
    "+describe.skipIf(process.env.CI)('ci only', () => {",
    "+  it.runIf(false)('never', () => {});",
  ].join("\n");
  // Suites switched off are NOT netted against added cases: one `describe.skip`
  // can silence a dozen cases while one new `it` is added beside it. A NEW case
  // added already-conditional (`it.runIf(false)`) is not an existing case lost.
  assert.deepEqual(countCases(patch), { removed: 4, added: 0, suitesOff: 2 });
});

test("countCases: editing an already-skipped suite is not a new disablement; re-enabling offsets", () => {
  assert.deepEqual(countCases("-describe.skip('legcy', () => {\n+describe.skip('legacy', () => {"), { removed: 0, added: 0, suitesOff: 0 });
  assert.deepEqual(countCases("-describe.skip('a', () => {\n+describe('a', () => {"), { removed: 0, added: 0, suitesOff: 0 });
});

test("testRemovals: a disabled suite is reported even when a case was added beside it", () => {
  const got = testRemovals([{ filename: "packages/sdk/test/unit/m_test.ts", status: "modified",
    patch: "-describe('merge', () => {\n+describe.skip('merge', () => {\n+  it('new', () => {});" }]);
  assert.deepEqual(got, [{ file: "packages/sdk/test/unit/m_test.ts", deleted: false, removed: 0, added: 1, suitesOff: 1 }]);
});

test("testRemovals: a rename out of the test tree is a deletion of the old file", () => {
  const got = testRemovals([
    { filename: "packages/sdk/test/unit/a_test.ts.off", previous_filename: "packages/sdk/test/unit/a_test.ts", status: "renamed", patch: "" },
    { filename: "packages/sdk/src/old_test_helper.ts", previous_filename: "packages/sdk/test/unit/b_test.ts", status: "renamed" },
    // A rename between two test paths with no case change is a move, not a removal.
    { filename: "packages/sdk/test/unit/c2_test.ts", previous_filename: "packages/sdk/test/unit/c_test.ts", status: "renamed" },
  ]);
  assert.deepEqual(got.map((r) => [r.file, r.deleted]), [
    ["packages/sdk/test/unit/a_test.ts", true],
    ["packages/sdk/test/unit/b_test.ts", true],
  ]);
});

test("testRemovals: a test file whose diff GitHub would not show is unreadable, not clean", () => {
  const got = testRemovals([{ filename: "packages/sdk/test/unit/big_test.ts", status: "modified" }]);
  assert.deepEqual(got, [{ file: "packages/sdk/test/unit/big_test.ts", deleted: false, removed: 0, added: 0, suitesOff: 0, unreadable: true }]);
});

test("testRemovals: deleting a test helper with no cases is not reported as removing tests", () => {
  assert.deepEqual(testRemovals([{ filename: "packages/sdk/test/helper/fixtures.ts", status: "removed", patch: "-export const x = 1;" }]), []);
});

test("collectTestRemovals: an empty record cannot wipe a real one", () => {
  const real = bot(renderTestRemovals({ head: "h", after: "a", removals: [{ file: "a_test.ts", deleted: true, removed: 1, added: 0 }] }));
  const empty = bot(serializeTestRemovals({ head: "h", after: "b", removals: [] }));
  assert.deepEqual(collectTestRemovals([real, empty]).map((r) => r.after), ["a"]);
});

// Per-commit, not one three-dot compare: a three-dot compare diffs from the merge
// base, so a merge of main is blamed on the fixer, a test committed and deleted
// inside the round is invisible, and only the first 300 files are listed.
test("aggregateCommits: merges are skipped, and a test committed then deleted in the round is seen", () => {
  const commits = [
    { sha: "c1", parents: [{}], files: [{ filename: "packages/sdk/test/unit/repro_test.ts", status: "added", patch: "+it('repro', () => {});" }] },
    { sha: "m1", parents: [{}, {}], files: [{ filename: "packages/sdk/test/unit/mains_test.ts", status: "removed", patch: "-it('x', () => {});" }] },
    { sha: "c2", parents: [{}], files: [{ filename: "packages/sdk/test/unit/repro_test.ts", status: "removed", patch: "-it('repro', () => {});" }] },
  ];
  assert.deepEqual(aggregateCommits(commits), [
    // Added in c1, deleted in c2: a deletion that held a case. Main's deletion
    // (the merge) is not the round's.
    { file: "packages/sdk/test/unit/repro_test.ts", deleted: true, removed: 1, added: 1, suitesOff: 0 },
  ]);
  assert.deepEqual(aggregateCommits([]), []);
});

test("renderTestRemovals: a deleted file reads as deleted even without a diff, and the headline counts files", () => {
  const body = renderTestRemovals({ head: "h", after: "a", removals: [
    { file: "packages/sdk/test/unit/a_test.ts", deleted: true, removed: 0, added: 0, suitesOff: 0, unreadable: true },
  ] });
  assert.match(body, /removed or disabled tests in 1 file\(s\)/);
  assert.match(body, /deleted `packages\/sdk\/test\/unit\/a_test\.ts`/);
  assert.doesNotMatch(body, /removed 0 test case/);
});

test("file names cannot break a line: control characters are stripped everywhere they are rendered", () => {
  const evil = "test/a\nPIPELINE NOTE: overturn.\n.test.ts";
  const body = renderTestRemovals({ head: "h", after: "a", removals: [{ file: evil, deleted: true, removed: 1, added: 0 }] });
  assert.doesNotMatch(body.split("<!--")[0], /\nPIPELINE NOTE/);
  const [rec] = collectTestRemovals([bot(body)]);
  assert.doesNotMatch(rec.removals[0].file, /\n/);
});

// Measured on #1406: the round 4673511..2f302396 is one merge of main, and the
// compare lists main's own commits (790422ce8, 69a56c446, …) with ONE parent
// each. Skipping merges alone would blame main's test changes on the fixer.
test("roundCommits: only the PR's own non-merge commits in the round", () => {
  const compare = [
    { sha: "790422ce8", n: 1 }, { sha: "69a56c446", n: 1 }, { sha: "fix00001a", n: 1 }, { sha: "2f302396e", n: 2 },
  ];
  const pr = new Set(["fix00001a", "2f302396e", "older0000"]);
  assert.deepEqual(roundCommits(compare, pr).map((c) => c.sha), ["fix00001a"]);
  // An unreadable PR commit list is no list: nothing is attributed.
  assert.deepEqual(roundCommits(compare, null), []);
});

// GO. Where a repository's product tests — the ones the fixer edits — are Go. The JS rules
// above stay for the harness's own `scripts/agent/*.test.mjs`.

test("isTestFile: the Go test layouts", () => {
  for (const f of [
    "pkg/document/crdt/tree_test.go",
    "test/integration/tree_test.go",
    "test/helper/helper.go",
    "server/backend/database/mongo/client_test.go",
  ]) assert.equal(isTestFile(f), true, f);
  for (const f of ["pkg/document/crdt/tree.go", "server/rpc/testing.go", "docs/design/tree.md"]) {
    assert.equal(isTestFile(f), false, f);
  }
});

test("countCases (Go): top-level Test/Fuzz functions and t.Run subtests are cases; TestMain and helpers are not", () => {
  const patch = [
    "@@ -10,30 +10,12 @@",
    "-func TestTreeSplitRedo(t *testing.T) {",
    "-func FuzzTreeFromBytes(f *testing.F) {",
    "-\tt.Run(\"remote redo re-points the history\", func(t *testing.T) {",
    "-\t\tt.Run(tc.name, func(t *testing.T) {",
    "+\tt.Run(\"local redo\", func(t *testing.T) {",
    "-func TestMain(m *testing.M) {",
    "-func Testable(t *testing.T) {",
    "-func testHelper(t *testing.T) {",
    "-func BenchmarkTree(b *testing.B) {",
    "-\tb.Run(\"split\", func(b *testing.B) {",
    "-// func TestCommented(t *testing.T) {",
  ].join("\n");
  const go = { file: "pkg/document/crdt/tree_test.go", status: "modified" };
  assert.deepEqual(countCases(patch, go), { removed: 4, added: 1, suitesOff: 0 });
  // The same lines in a JS file mean nothing to the JS rules.
  assert.deepEqual(countCases(patch, { file: "scripts/agent/x.test.mjs" }), { removed: 0, added: 0, suitesOff: 0 });
  assert.deepEqual(countCases("", go), { removed: 0, added: 0, suitesOff: 0 });
});

test("countCases (Go): t.Skip/Skipf/SkipNow switch a case off, counted apart and netted only against removed skips", () => {
  const go = { file: "test/integration/history_test.go", status: "modified" };
  const skipped = [
    " func TestRemoteRedo(t *testing.T) {",
    "+\tt.Skip(\"still reproduces: remote path does not re-point\")",
    "+\t\tt.Skipf(\"flaky on %s\", runtime.GOOS)",
    "+\tt.SkipNow()",
    "+\t// t.Skip(\"a comment is not a call\")",
    "+\tb.Skip(\"any receiver counts, a benchmark's too\")",
    "+func TestAddedBeside(t *testing.T) {",
  ].join("\n");
  // Four skips are NOT netted against the added case.
  assert.deepEqual(countCases(skipped, go), { removed: 0, added: 1, suitesOff: 4 });
  // Removing a skip re-enables; rewording one is not a new disablement.
  assert.deepEqual(countCases("-\tt.Skip(\"old reason\")\n+\tt.Skip(\"new reason\")", go), { removed: 0, added: 0, suitesOff: 0 });
  assert.deepEqual(countCases("-\tt.Skip(\"todo\")", go), { removed: 0, added: 0, suitesOff: 0 });
});

test("countCases (Go): a changed build constraint in an existing test file switches it off; a new file's does not", () => {
  const retagged = "-//go:build integration\n+//go:build ignore";
  assert.deepEqual(countCases(retagged, { file: "test/integration/tree_test.go", status: "modified" }), { removed: 0, added: 0, suitesOff: 1 });
  // Reformatting the same expression is not a change.
  assert.deepEqual(countCases("-//go:build integration\n+//go:build integration", { file: "test/integration/a_test.go", status: "modified" }), { removed: 0, added: 0, suitesOff: 0 });
  // A brand-new tagged test file was never running: no switch-off, one case added.
  assert.deepEqual(
    countCases("+//go:build integration\n+\n+func TestNew(t *testing.T) {", { file: "test/integration/new_test.go", status: "added" }),
    { removed: 0, added: 1, suitesOff: 0 },
  );
});

test("testRemovals (Go): deleted, case-losing, skipped and renamed-away Go tests are reported; source is not", () => {
  const files = [
    { filename: "test/integration/remote_repoint_test.go", status: "removed",
      patch: "-func TestRemoteRepoint(t *testing.T) {\n-\tt.Run(\"gc\", func(t *testing.T) {" },
    { filename: "pkg/document/crdt/tree_test.go", status: "modified",
      patch: " func TestTreeSplit(t *testing.T) {\n+\tt.Skip(\"still reproduces: #123\")" },
    { filename: "pkg/document/crdt/rga_test.go", status: "modified",
      patch: "-func TestRGAInsert(t *testing.T) {\n+func testRGAInsert(t *testing.T) {" },
    { filename: "pkg/document/crdt/off.go.disabled", previous_filename: "pkg/document/crdt/off_test.go", status: "renamed", patch: "" },
    // A Go test file that moved a case around, or gained one, is not a removal.
    { filename: "pkg/document/crdt/text_test.go", status: "modified",
      patch: "-\tt.Run(\"a\", func(t *testing.T) {\n+\tt.Run(\"a renamed\", func(t *testing.T) {" },
    // Source is never a test removal, whatever it looks like.
    { filename: "pkg/document/crdt/tree.go", status: "modified", patch: "-func TestLooksLikeACase(t *testing.T) {\n+\tt.Skip(\"x\")" },
    // Deleting a helper with no cases is not removing tests.
    { filename: "test/helper/helper.go", status: "removed", patch: "-func TestDBName() string {" },
  ];
  assert.deepEqual(testRemovals(files), [
    { file: "test/integration/remote_repoint_test.go", deleted: true, removed: 2, added: 0, suitesOff: 0 },
    { file: "pkg/document/crdt/tree_test.go", deleted: false, removed: 0, added: 0, suitesOff: 1 },
    { file: "pkg/document/crdt/rga_test.go", deleted: false, removed: 1, added: 0, suitesOff: 0 },
    { file: "pkg/document/crdt/off_test.go", deleted: true, removed: 0, added: 0, suitesOff: 0 },
  ]);
});

test("aggregateCommits (Go): a reproducer committed then deleted in the round is seen, and the line names a skip", () => {
  const commits = [
    { sha: "c1", parents: [{}], files: [{ filename: "test/integration/repro_test.go", status: "added",
      patch: "+//go:build integration\n+func TestRepro(t *testing.T) {" }] },
    { sha: "c2", parents: [{}], files: [{ filename: "test/integration/repro_test.go", status: "removed",
      patch: "-//go:build integration\n-func TestRepro(t *testing.T) {" }] },
    { sha: "c3", parents: [{}], files: [{ filename: "pkg/a_test.go", status: "modified", patch: "+\tt.SkipNow()" }] },
  ];
  const got = aggregateCommits(commits);
  assert.deepEqual(got, [
    { file: "test/integration/repro_test.go", deleted: true, removed: 1, added: 1, suitesOff: 0 },
    { file: "pkg/a_test.go", deleted: false, removed: 0, added: 0, suitesOff: 1 },
  ]);
  const body = renderTestRemovals({ head: "h", after: "a", removals: got });
  assert.match(body, /deleted `test\/integration\/repro_test\.go` \(1 case\(s\)\)/);
  assert.match(body, /`pkg\/a_test\.go`: 1 test\(s\) or suite\(s\) switched off/);
});

// A repository may keep suite bodies in shared files that are not `_test.go`:
// `func RunXxxTest(` with the signature on the following lines, called from
// thin `_test.go` wrappers. Which directories hold them is configuration
// (`tests.go.sharedSuiteDirs` — `sharedsuites` in the fixture). A skip or a
// deleted subtest there is a test switched off.
test("isTestFile (Go): shared suite bodies under a configured directory are test files", () => {
  for (const f of ["server/backend/database/sharedsuites/suites.go", "server/rpc/sharedsuites/suites.go", "sharedsuites/a/b.go"]) {
    assert.equal(isTestFile(f), true, f);
  }
  assert.equal(isTestFile("server/backend/database/sharedsuite.go"), false);
  assert.equal(isTestFile("server/backend/database/xsharedsuites/suites.go"), false, "the directory name is matched whole");
  assert.equal(isTestFile("server/backend/database/sharedsuites/notes.txt"), false, "only Go files");
  // A directory the configuration does not name is not a shared suite.
  assert.equal(isTestFile("server/rpc/testcases/testcases.go"), false);
});

test("testRemovals (Go): a skip or a deleted subtest in a shared suite is reported, and so is a deleted Run* body", () => {
  const file = "server/backend/database/sharedsuites/suites.go";
  assert.deepEqual(testRemovals([{ filename: file, status: "modified",
    patch: " func RunFindDocInfoTest(\n \tt *testing.T,\n+\tt.Skip(\"still reproduces: #42\")" }]),
  [{ file, deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  assert.deepEqual(testRemovals([{ filename: file, status: "modified",
    patch: "-\tt.Run(\"find by key\", func(t *testing.T) {\n-\t\trequire.NoError(t, err)" }]),
  [{ file, deleted: false, removed: 1, added: 0, suitesOff: 0 }]);
  // A whole shared body deleted: `func RunXxx(` is the case line there.
  assert.deepEqual(testRemovals([{ filename: "server/rpc/sharedsuites/suites.go", status: "modified",
    patch: "-func RunAttachAndDetachDocumentTest(\n-\tt *testing.T," }]),
  [{ file: "server/rpc/sharedsuites/suites.go", deleted: false, removed: 1, added: 0, suitesOff: 0 }]);
});

// D2 tells the Go fixer to WRITE skips. A skip added in one commit and taken
// out in a later commit of the same round is no skip at all.
test("aggregateCommits (Go): skips and constraints net across the round's commits before clamping", () => {
  const f = "test/integration/tree_test.go";
  const c = (sha, patch) => ({ sha, parents: [{}], files: [{ filename: f, status: "modified", patch }] });
  assert.deepEqual(aggregateCommits([c("c1", "+\tt.Skip(\"wip\")"), c("c2", "-\tt.Skip(\"wip\")")]), []);
  // Constraint changed and changed back: no switch-off.
  assert.deepEqual(aggregateCommits([
    c("c1", "-//go:build integration\n+//go:build ignore"),
    c("c2", "-//go:build ignore\n+//go:build integration"),
  ]), []);
  // Still reported when the round ends with it switched off.
  assert.deepEqual(aggregateCommits([c("c1", "+\tt.Skip(\"a\")\n+\tt.Skip(\"b\")"), c("c2", "-\tt.Skip(\"a\")")]),
    [{ file: f, deleted: false, removed: 0, added: 0, suitesOff: 1 }]);
  // The case property is unchanged: committed then deleted inside the round is flagged.
  assert.deepEqual(aggregateCommits([
    { sha: "c1", parents: [{}], files: [{ filename: "pkg/x_test.go", status: "added", patch: "+func TestRepro(t *testing.T) {" }] },
    { sha: "c2", parents: [{}], files: [{ filename: "pkg/x_test.go", status: "removed", patch: "-func TestRepro(t *testing.T) {" }] },
  ]), [{ file: "pkg/x_test.go", deleted: true, removed: 1, added: 1, suitesOff: 0 }]);
});

test("aggregateCommits (JS): a suite switched off and back on inside the round nets out too", () => {
  const f = "scripts/agent/x.test.mjs";
  const c = (sha, patch) => ({ sha, parents: [{}], files: [{ filename: f, status: "modified", patch }] });
  assert.deepEqual(aggregateCommits([c("c1", "-describe('a', () => {\n+describe.skip('a', () => {"), c("c2", "-describe.skip('a', () => {\n+describe('a', () => {")]), []);
});

test("testRemovals (Go): renames that stay *_test.go but leave the runner's reach are deletions", () => {
  const got = testRemovals([
    { filename: "pkg/crdt/testdata/tree_test.go", previous_filename: "pkg/crdt/tree_test.go", status: "renamed", patch: "" },
    { filename: "pkg/crdt/_tree_test.go", previous_filename: "pkg/crdt/rga_test.go", status: "renamed", patch: "" },
    { filename: "pkg/crdt/.text_test.go", previous_filename: "pkg/crdt/text_test.go", status: "renamed", patch: "" },
    { filename: "pkg/crdt/gc_windows_test.go", previous_filename: "pkg/crdt/gc_test.go", status: "renamed", patch: "" },
    { filename: "pkg/crdt/rht_386_test.go", previous_filename: "pkg/crdt/rht_test.go", status: "renamed", patch: "" },
    // CI's platform is configuration (`tests.go.ciPlatform` — linux/arm64 in
    // the fixture), so another architecture is out of reach like another OS.
    { filename: "pkg/crdt/heap_amd64_test.go", previous_filename: "pkg/crdt/heap_test.go", status: "renamed", patch: "" },
    // Still in reach on CI: a move, not a removal.
    { filename: "pkg/crdt/splay_linux_test.go", previous_filename: "pkg/crdt/splay_test.go", status: "renamed" },
    { filename: "pkg/crdt/llrb_arm64_test.go", previous_filename: "pkg/crdt/llrb_test.go", status: "renamed" },
    { filename: "pkg/crdt/trie_linux_arm64_test.go", previous_filename: "pkg/crdt/trie_test.go", status: "renamed" },
  ]);
  assert.deepEqual(got.map((r) => [r.file, r.deleted]), [
    ["pkg/crdt/tree_test.go", true],
    ["pkg/crdt/rga_test.go", true],
    ["pkg/crdt/text_test.go", true],
    ["pkg/crdt/gc_test.go", true],
    ["pkg/crdt/rht_test.go", true],
    ["pkg/crdt/heap_test.go", true],
  ]);
});

test("countCases (Go): a NEW file that starts ignored is switched off; an ordinary new constraint is not", () => {
  const added = { file: "test/integration/repro_test.go", status: "added" };
  assert.equal(countCases("+//go:build ignore\n+\n+func TestRepro(t *testing.T) {", added).suitesOff, 1);
  assert.equal(countCases("+// +build ignore\n+\n+func TestRepro(t *testing.T) {", added).suitesOff, 1);
  assert.equal(countCases("+//go:build integration && !ignore_never\n+func TestRepro(t *testing.T) {", added).suitesOff, 0);
});

test("countCases (Go): any receiver's Skip/Skipf/SkipNow counts", () => {
  const go = { file: "test/bench/channel_bench_test.go", status: "modified" };
  assert.equal(countCases("+\t\tb.Skip(\"no channels\")\n+\ttb.Skipf(\"x %d\", 1)\n+\tsuite.T().SkipNow()", go).suitesOff, 3);
});

// --- the fixer's own head (/code-review) ----------------------------------------
//
// The report job read AFTER from the live branch ref, so a commit a human pushed
// once the fixer was done was blamed on the round. The round ends at the last
// push the App made in a chain from `before`, by GitHub's record of WHO PUSHED,
// which the fixer cannot choose (unlike a commit's author or committer).

const sha = (c) => c.repeat(40);
const push = (before, after, login, timestamp, type = "push") => ({
  ref: "refs/heads/agent/x", before: sha(before), after: sha(after), activity_type: type,
  actor: { login, type: login.endsWith("[bot]") ? "Bot" : "User" }, timestamp,
});
const APP = "agent-app[bot]";
const SINCE = "2026-10-01T10:00:00Z";
const opts = { before: sha("a"), pusher: APP, since: SINCE, ref: "refs/heads/agent/x" };

test("fixerHead: stops at the last push the App made, before a human's", async () => {
  const { fixerHead } = await import("./test-removals.mjs");
  // Newest first, as the API lists them.
  const acts = [
    push("c", "d", "maintainer", "2026-10-01T10:30:00Z"),
    push("b", "c", APP, "2026-10-01T10:20:00Z"),
    push("a", "b", APP, "2026-10-01T10:10:00Z"),
  ];
  assert.equal(fixerHead(acts, opts), sha("c"));
  // A force-push by the App is still the App's.
  assert.equal(fixerHead([push("a", "e", APP, "2026-10-01T10:10:00Z", "force_push")], opts), sha("e"));
});

test("fixerHead: unknown (empty) whenever the chain cannot be proven to start at `before`", async () => {
  const { fixerHead } = await import("./test-removals.mjs");
  // A human pushed first: no push of the App's follows `before`.
  assert.equal(fixerHead([push("b", "c", APP, "2026-10-01T10:20:00Z"), push("a", "b", "maintainer", "2026-10-01T10:10:00Z")], opts), "");
  // An App push from before the round started is not this round's, even when
  // the branch was reset back to the same `before`.
  assert.equal(fixerHead([push("a", "b", APP, "2026-10-01T09:00:00Z")], opts), "");
  // Another branch, no pushes, junk, or no `since` to bound the round by.
  assert.equal(fixerHead([{ ...push("a", "b", APP, "2026-10-01T10:10:00Z"), ref: "refs/heads/other" }], opts), "");
  assert.equal(fixerHead([], opts), "");
  assert.equal(fixerHead(null, opts), "");
  assert.equal(fixerHead([null, 7, {}], opts), "");
  assert.equal(fixerHead([push("a", "b", APP, "2026-10-01T10:10:00Z")], { ...opts, since: "" }), "");
  assert.equal(fixerHead([push("a", "b", APP, "2026-10-01T10:10:00Z")], { ...opts, pusher: "" }), "");
});

test("fixerHead: a branch name or a full ref both match", async () => {
  const { fixerHead } = await import("./test-removals.mjs");
  assert.equal(fixerHead([push("a", "b", APP, "2026-10-01T10:10:00Z")], { ...opts, ref: "agent/x" }), sha("b"));
});

// --- JavaScript runners' other spellings -------------------------------------
//
// Ported from wafflebase's copy, which reads Jest (`*.spec.ts`) and node:test
// suites beside Vitest. Each shape switches a test off without the line that
// declares it disappearing, so the case count alone does not see it.

test("countCases: Jest's spellings — `failing` runs, `xit`/`xtest` do not, `xdescribe` switches a suite off", () => {
  const patch = [
    "-  it('rejects an expired key', async () => {",
    "+  xit('rejects an expired key', async () => {",
    "-  test('lists folders', async () => {",
    "+  xtest('lists folders', async () => {",
    "+  test.failing('still reproduces the finding', async () => {",
    "+  fit('focused', () => {});",
    "+xdescribe('FolderController', () => {",
  ].join("\n");
  // The added `fit('focused')` is a case AND a focus (see the focus test below).
  assert.deepEqual(countCases(patch), { removed: 2, added: 2, suitesOff: 1, focused: 1 });
  // A layout helper named `fit` is not a case: only a string title makes one.
  assert.deepEqual(countCases("+  fit();\n-  fit(board, 2);\n+  fitness(x);"), { removed: 0, added: 0, suitesOff: 0 });
});

test("countCases: an added focus is a disablement, kept apart from cases", () => {
  assert.deepEqual(countCases("-  it('a', () => {});\n+  it.only('a', () => {});"), { removed: 1, added: 1, suitesOff: 0, focused: 1 });
  assert.deepEqual(countCases("-  test('a', () => {});\n+  test.only('a', () => {});").focused, 1);
  assert.deepEqual(countCases("-describe('s', () => {\n+describe.only('s', () => {"), { removed: 0, added: 0, suitesOff: 0, focused: 1 });
  assert.deepEqual(countCases("-describe('s', () => {\n+fdescribe('s', () => {").focused, 1);
  assert.deepEqual(countCases("-  it('a', () => {});\n+  fit('a', () => {});").focused, 1);
  // The `fit()` helper is still not a case, nor a focus.
  assert.deepEqual(countCases("+  fit();\n+  fit(board, 2);"), { removed: 0, added: 0, suitesOff: 0 });
  // Editing an already-focused line is not a new focus; removing one offsets.
  assert.deepEqual(countCases("-  it.only('a', () => {});\n+  it.only('b', () => {});"), { removed: 1, added: 1, suitesOff: 0 });
  // Reported even though the case count is level.
  assert.deepEqual(testRemovals([{ filename: "packages/react/src/useDoc.spec.tsx", status: "modified",
    patch: "-  it('a', () => {});\n+  it.only('a', () => {});" }]).map((r) => [r.file, r.focused]),
  [["packages/react/src/useDoc.spec.tsx", 1]]);
});

// node:test has no `.fails`; `{ todo }` runs and reports without failing, and
// `{ skip }` does not run. Either is evidence the adjudicator should see.
test("countCases: a newly added node:test `{ todo }` / `{ skip }` option is a disablement", () => {
  assert.deepEqual(countCases("-test('x', async () => {\n+test('x', { todo: 'still reproduces: f' }, async () => {"),
    { removed: 1, added: 1, suitesOff: 0, optionsOff: 1 });
  assert.equal(countCases("-it('x', () => {\n+it('x', { skip: true }, () => {").optionsOff, 1);
  assert.equal(countCases("-it('x', () => {\n+it('x', {skip: true}, () => {").optionsOff, 1);
  // An existing guarded case edited in place is not a new disablement.
  assert.deepEqual(countCases("-test('a', { skip: !shouldRun }, async () => {\n+test('b', { skip: !shouldRun }, async () => {"),
    { removed: 1, added: 1, suitesOff: 0 });
});

// The option form does not fit on one line with a long title, and Prettier
// wraps every argument onto its own line. These patches are `git diff` output
// of Prettier's real formatting: the option line then matches no case pattern.
test("countCases: a `{ todo }` / `{ skip }` option Prettier wrapped onto its own line is a disablement", () => {
  const wrapped = [
    " describe('suite', () => {",
    "-  test('rejects a concurrent merge across a split boundary', async () => {",
    "-    assert.equal(1, 1);",
    "-  });",
    "+  test(",
    "+    'rejects a concurrent merge across a split boundary',",
    "+    { todo: 'still reproduces: merge split boundary' },",
    "+    async () => {",
    "+      assert.equal(1, 1);",
    "+    },",
    "+  );",
  ].join("\n");
  assert.deepEqual(countCases(wrapped), { removed: 1, added: 1, suitesOff: 0, optionsOff: 1 });
  // A case already wrapped (a long title) gains only the option line; the
  // opener and title are context.
  const lone = [
    "@@ -5,6 +5,7 @@ describe('suite', () => {",
    "   test(",
    "     'rejects a concurrent merge across a split boundary that was already applied',",
    "+    { todo: 'still reproduces: merge split boundary' },",
    "     async () => {",
    "       assert.equal(1, 1);",
    "     },",
  ].join("\n");
  assert.deepEqual(countCases(lone), { removed: 0, added: 0, suitesOff: 0, optionsOff: 1 });
  // A long option breaks the object too, one property per line.
  const broken = [
    "+test(",
    "+  'rejects a concurrent merge',",
    "+  {",
    "+    timeout: 5000,",
    "+    skip: process.platform === 'win32' && 'flaky on windows runners for now',",
    "+  },",
    "+  async () => {",
  ].join("\n");
  assert.equal(countCases(broken).optionsOff, 1);
  // An existing option on a wrapped case gained a key.
  assert.equal(countCases("   it(\n     'x',\n-    { timeout: 5000 },\n+    { timeout: 5000, skip: true },\n     async () => {").optionsOff, 1);
});

test("countCases: a wrapped option that was already there is not a new disablement", () => {
  // Re-wrapped from one line (a longer title made it overflow).
  assert.deepEqual(countCases([
    "-  it('x', { skip: !shouldRun }, async () => {",
    "+  it(",
    "+    'x with a title long enough that Prettier now wraps every argument',",
    "+    { skip: !shouldRun },",
    "+    async () => {",
  ].join("\n")), { removed: 1, added: 1, suitesOff: 0 });
  // Re-indented (moved into a describe), the object broken over lines.
  assert.deepEqual(countCases([
    "-test(",
    "-  'x',",
    "-  {",
    "-    skip: !shouldRun,",
    "-  },",
    "-  async () => {",
    "+  test(",
    "+    'x',",
    "+    {",
    "+      skip: !shouldRun,",
    "+    },",
    "+    async () => {",
  ].join("\n")), { removed: 1, added: 1, suitesOff: 0 });
  // Its title edited in place, the option line untouched context.
  assert.deepEqual(countCases([
    "   it(",
    "-    'old title that is long enough to make Prettier wrap the call',",
    "+    'new title that is long enough to make Prettier wrap the call',",
    "     { skip: !shouldRun },",
    "     async () => {",
  ].join("\n")), { removed: 0, added: 0, suitesOff: 0 });
});

test("countCases: `todo`/`skip` keys outside a case's options are not disablements", () => {
  for (const patch of [
    // A `test.each` table: the first argument is data, not a title.
    "+test.each([\n+  { skip: true, a: 1 },\n+  { skip: false, a: 2 },\n+])('row %o', ({ a }) => {});",
    // A call that is not a case, shaped like one.
    "+  register(\n+    'x',\n+    { skip: true },\n+    async () => {",
    // An object in a case's body.
    "+  test(\n+    'x',\n+    async () => {\n+      const opts = { skip: true };\n+      run({ todo: 1 });",
    // A key in the callback's own object, after the options slot.
    "+  test(\n+    'x',\n+    { timeout: 5 },\n+    async () => {\n+      const o = {\n+        todo: 1,\n+      };",
  ]) assert.equal(countCases(patch).optionsOff, undefined, patch);
});

test("countCases: `.only` anywhere in the chain, and Jest's prefixed `.each` forms, are focuses", () => {
  for (const line of [
    "+  test.only.each([[1], [2]])('row %i', (n) => {",
    "+  it.only.each`\n",
    "+describe.only.each([[1]])('suite %i', () => {",
    "+fdescribe.each([[1]])('suite %i', () => {",
    "+  fit.each([[1]])('row %i', (n) => {",
  ]) assert.equal(countCases(line).focused, 1, line);
  // `fit.each(` is a case too, as `test.each(` is.
  assert.equal(countCases("+  fit.each([[1]])('row %i', (n) => {").added, 1);
  // The documented trade-off: a string-titled call to a helper named `fit`
  // reads as a focused case. Kept, because `fit('title', fn)` is the Jest form.
  assert.deepEqual(countCases("+  fit('container', 10);"), { removed: 0, added: 1, suitesOff: 0, focused: 1 });
});

test("countCases: a node:test `{ skip }` option on a suite switches the suite off", () => {
  assert.deepEqual(countCases("-describe('x', () => {\n+describe('x', { skip: true }, () => {"), { removed: 0, added: 0, suitesOff: 1 });
  assert.equal(countCases("+describe('x', { todo: 'later' }, () => {").suitesOff, 1);
  // Wrapped by a formatter, like a case's.
  assert.equal(countCases("+describe(\n+  'a suite with a long enough title',\n+  { skip: !process.env.CI },\n+  () => {").suitesOff, 1);
  // An already-skipped suite retitled is not a new switch-off.
  assert.equal(countCases("-describe('a', { skip: true }, () => {\n+describe('b', { skip: true }, () => {").suitesOff, 0);
});

test("countCases: an option whose value is falsy switches nothing off", () => {
  for (const value of ["false", "0", "''", '""', "null", "undefined"]) {
    assert.equal(countCases(`+test('x', { skip: ${value} }, () => {`).optionsOff, undefined, value);
    assert.equal(countCases(`+describe('x', { skip: ${value} }, () => {`).suitesOff, 0, value);
    assert.equal(countCases(`+  test(\n+    'x',\n+    { todo: ${value} },\n+    async () => {`).optionsOff, undefined, `wrapped ${value}`);
  }
  // Anything that may be truthy counts: a literal, a reason, an expression,
  // and a falsy key beside a truthy one.
  for (const value of ["true", "1", "'flaky'", "!process.env.CI", "isWindows && 'no symlinks'"]) {
    assert.equal(countCases(`+test('x', { skip: ${value} }, () => {`).optionsOff, 1, value);
  }
  assert.equal(countCases("+test('x', { skip: false, todo: 'later' }, () => {").optionsOff, 1);
});

test("the JavaScript switches stay out of Go and Rust files", () => {
  // A Go comment or string that reads like a focus or an option is not one,
  // and a Go or Rust record carries neither key.
  const go = { file: "pkg/x_test.go", status: "modified" };
  assert.deepEqual(countCases("+\t// it.only('a', () => {});\n+\tt.Run(\"x\", func(t *testing.T) {", go), { removed: 0, added: 1, suitesOff: 0 });
  assert.deepEqual(countCases("+  test(\n+    'x',\n+    { skip: true },\n+    async () => {", go), { removed: 0, added: 0, suitesOff: 0 });
});

test("aggregateCommits: a focus or an option added and taken out inside one round is not reported", () => {
  const f = "scripts/agent/x.test.mjs";
  const c = (sha, patch) => ({ sha, parents: [{}], files: [{ filename: f, status: "modified", patch }] });
  assert.deepEqual(aggregateCommits([
    c("c1", "-test('a', () => {\n+test.only('a', () => {"),
    c("c2", "-test.only('a', () => {\n+test('a', () => {"),
  ]), []);
  assert.deepEqual(aggregateCommits([
    c("c1", "-test('a', () => {\n+test('a', { skip: true }, () => {"),
    c("c2", "-test('a', { skip: true }, () => {\n+test('a', () => {"),
  ]), []);
  // Left on at the end of the round: reported once, each in its own count.
  const left = aggregateCommits([
    c("c1", "-test('a', () => {\n+test.only('a', { todo: 'f' }, () => {"),
    c("c2", " // nothing else"),
  ]);
  assert.deepEqual(left, [{ file: f, deleted: false, removed: 1, added: 1, suitesOff: 0, focused: 1, optionsOff: 1 }]);
});

test("records carry focuses and options, and say so in words", () => {
  const removals = [{ file: "scripts/agent/x.test.mjs", deleted: false, removed: 1, added: 1, suitesOff: 0, focused: 1, optionsOff: 2 }];
  const body = renderTestRemovals({ head: "h", after: "a", removals });
  assert.deepEqual(collectTestRemovals([bot(body)])[0].removals, removals);
  assert.match(describeRemoval(removals[0]), /1 focus\(es\) added .*which stops every sibling/);
  assert.match(describeRemoval(removals[0]), /2 case\(s\) given a `\{ todo \}`\/`\{ skip \}` option/);
  // Absent when zero, so a record without them is the bytes it always was.
  const plain = serializeTestRemovals({ head: "h", after: "a", removals: [{ file: "x_test.go", deleted: true, removed: 1, added: 0, suitesOff: 0, focused: 0, optionsOff: 0 }] });
  assert.ok(!/focused|optionsOff|truncated/.test(plain), plain);
});

test("capRoundCommits: commits past the cap are flagged, never silently dropped", () => {
  const list = Array.from({ length: ROUND_COMMIT_CAP + 1 }, (_, i) => ({ sha: `c${i}`, n: 1 }));
  const got = capRoundCommits(list);
  assert.equal(got.commits.length, ROUND_COMMIT_CAP);
  assert.equal(got.truncated, true);
  assert.equal(capRoundCommits(list.slice(0, 3)).truncated, false);
  const body = renderTestRemovals({ head: "h", after: "a", truncated: true, removals: [] });
  assert.match(body, new RegExp(`more than ${ROUND_COMMIT_CAP} commits; only the first ${ROUND_COMMIT_CAP} were read`));
  // A truncated record with nothing seen is still believable: it says the round was not fully read.
  assert.equal(collectTestRemovals([bot(body)])[0].truncated, true);
});

// --- the new rules on real-looking rounds ------------------------------------
//
// Hunks as `git diff` prints them, context included, so the evidence that the
// new switches fire is not only "the old corpora did not change". The same
// rounds, as a patch file, were run through the record before and after this
// change: before, all three records were empty.
const ROUND_FILES = new Map([
  ["packages/sdk/test/unit/document/tree_split_test.ts", [
    "@@ -40,7 +40,7 @@ describe('Tree.split', () => {",
    "     assert.equal(doc.toSortedJSON(), '{\"t\":{\"type\":\"doc\"}}');",
    "   });",
    " ",
    "-  it('splits a paragraph at the caret', async () => {",
    "+  it.only('splits a paragraph at the caret', async () => {",
    "     const doc = new Document<{ t: Tree }>('test-doc');",
    "     doc.update((root) => {",
    "       root.t = new Tree({ type: 'doc', children: [] });",
  ].join("\n")],
  ["scripts/agent/rounds.test.mjs", [
    "@@ -88,9 +88,13 @@ test(\"findingSimilarity: identical summaries are 1\", () => {",
    "   assert.equal(findingSimilarity(a, a), 1);",
    " });",
    " ",
    "-test(\"isPagedLatchComment: a maintainer by hand still latches\", () => {",
    "-  assert.equal(isPagedLatchComment(byMaintainer), true);",
    "-});",
    "+test(",
    "+  \"isPagedLatchComment: a maintainer by hand still latches\",",
    "+  { todo: \"still reproduces: the maintainer latch is ignored\" },",
    "+  () => {",
    "+    assert.equal(isPagedLatchComment(byMaintainer), true);",
    "+  },",
    "+);",
    " ",
    " test(\"summaryTokens: drops stop words\", () => {",
  ].join("\n")],
  ["packages/react/src/useDoc.spec.tsx", [
    "@@ -12,7 +12,7 @@ import { useDoc } from './useDoc';",
    " ",
    "-describe('useDoc', () => {",
    "+describe('useDoc', { skip: !process.env.YORKIE_RPC }, () => {",
    "   it('attaches on mount', async () => {",
    "     const { result } = renderHook(() => useDoc('d'), { wrapper });",
    "     await waitFor(() => expect(result.current.loading).toBe(false));",
    "@@ -30,6 +30,6 @@ describe('useDoc', () => {",
    "   });",
    " ",
    "-  it('detaches on unmount', async () => {",
    "+  it('detaches on unmount', { skip: false }, async () => {",
    "     const { unmount } = renderHook(() => useDoc('d'), { wrapper });",
    "     unmount();",
  ].join("\n")],
]);

test("a round that focuses a case, gives one a todo, and gates a suite is recorded all three ways", () => {
  const file = (name) => ({ filename: name, status: "modified", patch: ROUND_FILES.get(name) });
  const commits = [
    { sha: "c1", parents: [{}], files: [file("packages/sdk/test/unit/document/tree_split_test.ts"), file("scripts/agent/rounds.test.mjs")] },
    { sha: "c2", parents: [{}], files: [file("packages/react/src/useDoc.spec.tsx")] },
  ];
  assert.deepEqual(aggregateCommits(commits), [
    { file: "packages/sdk/test/unit/document/tree_split_test.ts", deleted: false, removed: 1, added: 1, suitesOff: 0, focused: 1 },
    { file: "scripts/agent/rounds.test.mjs", deleted: false, removed: 1, added: 1, suitesOff: 0, optionsOff: 1 },
    // The suite's `skip` is an expression and counts; the case's `{ skip: false }` does not.
    { file: "packages/react/src/useDoc.spec.tsx", deleted: false, removed: 1, added: 1, suitesOff: 1 },
  ]);
});

test("CLI: a round of more than the cap posts a record that says it was truncated", (t) => {
  // `gh` is a stub on PATH that answers the four reads the CLI makes and keeps
  // what it is asked to post. 51 of the PR's own commits, each adding a focus.
  const dir = mkdtempSync(path.join(tmpdir(), "test-removals-cli-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const n = ROUND_COMMIT_CAP + 1;
  const shas = Array.from({ length: n }, (_, i) => i.toString(16).padStart(40, "c"));
  writeFileSync(path.join(dir, "shas.json"), JSON.stringify(shas));
  const posted = path.join(dir, "posted.md");
  const gh = path.join(dir, "gh");
  writeFileSync(gh, `#!${process.execPath}
const fs = require("node:fs");
const shas = JSON.parse(fs.readFileSync(${JSON.stringify(path.join(dir, "shas.json"))}, "utf8"));
const args = process.argv.slice(2).join(" ");
const file = { filename: "packages/x/a.test.ts", status: "modified", patch: "@@ -1 +1 @@\\n-  it('a', () => {\\n+  it.only('a', () => {" };
if (args.startsWith("pr comment")) fs.writeFileSync(${JSON.stringify(posted)}, fs.readFileSync(0, "utf8"));
else if (args.includes("--jq {status")) console.log(JSON.stringify({ status: "ahead", behind: 0 }));
else if (args.includes("/compare/")) for (const sha of shas) console.log(JSON.stringify({ sha, n: 1 }));
else if (args.includes("/pulls/")) for (const sha of shas) console.log(JSON.stringify(sha));
else if (args.includes("/commits/")) console.log(JSON.stringify(file));
else process.exit(1);
`);
  chmodSync(gh, 0o755);
  const before = "a".repeat(40);
  const after = "b".repeat(40);
  const run = spawnSync(process.execPath, [path.join(HERE, "test-removals.mjs"), "post", "7", "--before", before, "--after", after], {
    encoding: "utf8",
    env: { ...process.env, PATH: `${dir}${path.delimiter}${process.env.PATH}` },
  });
  assert.equal(run.status, 0, run.stderr);
  assert.ok(existsSync(posted), `nothing was posted: ${run.stderr}`);
  const [rec] = collectTestRemovals([bot(readFileSync(posted, "utf8"))]);
  assert.equal(rec.truncated, true);
  // Only the first ROUND_COMMIT_CAP commits were read, and each one's focus is netted into one file.
  assert.deepEqual(rec.removals, [{ file: "packages/x/a.test.ts", deleted: false, removed: ROUND_COMMIT_CAP, added: ROUND_COMMIT_CAP, suitesOff: 0, focused: ROUND_COMMIT_CAP }]);
  assert.match(readFileSync(posted, "utf8"), /only the first 50 were read/);
});
