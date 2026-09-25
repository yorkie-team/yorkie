// Tests for the per-lane failure summariser.
//
// PURE INPUT, PURE OUTPUT. Nothing here spawns a process or touches the
// filesystem; the fixtures are real output shapes copied from this
// repository's own lanes. That is the point of `lane-failure.mjs` being a
// module of its own — the parsing is what has to be exhaustive, and it can be
// without a Go toolchain anywhere near the suite.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  isNotableLine,
  LANE_KINDS,
  MAX_SUMMARY_CHARS,
  summarizeFailure,
} from '../ci/lane-failure.mjs';

/** Split a fixture into the shape the runner hands the summariser. */
function evidence(kind, output) {
  const lines = output.split('\n');
  return {
    kind,
    notable: lines.filter((l) => isNotableLine(kind, l)),
    tail: output,
    exitCode: 1,
  };
}

const GO_TEST_FAIL = `=== RUN   TestTreeEdit
=== RUN   TestTreeEdit/split_at_a_boundary
    tree_test.go:214: expected <p>ab</p>, got <p>a</p><p>b</p>
    --- FAIL: TestTreeEdit/split_at_a_boundary (0.01s)
--- FAIL: TestTreeEdit (0.02s)
FAIL
FAIL	github.com/yorkie-team/yorkie/pkg/document/crdt	0.312s
ok  	github.com/yorkie-team/yorkie/pkg/units	0.004s
FAIL`;

test('a failing Go test is named, not the first line with the word error', () => {
  const summary = summarizeFailure(evidence('go-test', GO_TEST_FAIL));
  assert.match(summary, /TestTreeEdit\/split_at_a_boundary failed/);
  assert.match(summary, /github\.com\/yorkie-team\/yorkie\/pkg\/document\/crdt/);
  // The subtest, not its parent: `go test -v` prints the deepest one first,
  // and it is the name that can be handed to `go test -run`.
  assert.doesNotMatch(summary, /^TestTreeEdit failed/);
});

test('the assertion line rides along when there is one', () => {
  assert.match(summarizeFailure(evidence('go-test', GO_TEST_FAIL)), /expected <p>ab<\/p>/);
});

test('the passing package is not mistaken for the failing one', () => {
  assert.doesNotMatch(summarizeFailure(evidence('go-test', GO_TEST_FAIL)), /pkg\/units/);
});

test('a data race outranks the test failure it causes', () => {
  // `-race` is the reason the integration lane is expensive; a report that
  // named only the `--- FAIL:` would hide why it failed.
  const out = `==================
WARNING: DATA RACE
Write at 0x00c0001b4010 by goroutine 42:
  github.com/yorkie-team/yorkie/server/backend.(*Backend).Close()
==================
--- FAIL: TestBackendClose (0.11s)
FAIL	github.com/yorkie-team/yorkie/server/backend	1.2s`;
  const summary = summarizeFailure(evidence('go-test', out));
  assert.match(summary, /data race detected in TestBackendClose/);
  assert.match(summary, /server\/backend/);
});

test('a compile failure outranks everything: there is no test to name', () => {
  const out = `# github.com/yorkie-team/yorkie/pkg/document
pkg/document/document.go:118:9: undefined: crdt.NewTreeNodeX
FAIL	github.com/yorkie-team/yorkie/pkg/document [build failed]`;
  const summary = summarizeFailure(evidence('go-test', out));
  assert.match(summary, /failed to build/);
  assert.match(summary, /document\.go:118: undefined: crdt\.NewTreeNodeX/);
});

test('a panic is reported as a panic, with the test it aborted', () => {
  const out = `--- FAIL: TestSplitText (0.00s)
panic: duplicate TreeNodeID 5:0:1 [recovered]
	goroutine 19 [running]:
FAIL	github.com/yorkie-team/yorkie/pkg/document/crdt	0.4s`;
  const summary = summarizeFailure(evidence('go-test', out));
  assert.match(summary, /^panic: duplicate TreeNodeID/);
  assert.match(summary, /TestSplitText/);
});

test('golangci-lint names the first issue and counts the rest', () => {
  const out = `server/rpc/yorkie_server.go:88:2: cyclomatic complexity 31 of func \`x\` is high (gocyclo)
server/rpc/yorkie_server.go:140:1: line is 130 characters (lll)
2 issues:`;
  const summary = summarizeFailure(evidence('golangci', out));
  assert.match(summary, /yorkie_server\.go:88:2: cyclomatic complexity/);
  assert.match(summary, /\(gocyclo\)/);
  assert.match(summary, /\+1 more/);
});

test('buf names the proto and the position', () => {
  const out = `api/yorkie/v1/resources.proto:412:3:Field "1" with name "id" changed type from "string" to "bytes".`;
  assert.match(summarizeFailure(evidence('buf', out)), /resources\.proto:412:3/);
});

test('the licence gate names the file, not its own success line', () => {
  const out = `[verify:license]   pkg/document/crdt/tree.go has no Apache 2.0 header
[verify:license]   server/rpc/auth.go has no Apache 2.0 header
[verify:license] 2 file(s) missing the header.`;
  const summary = summarizeFailure(evidence('license', out));
  assert.match(summary, /pkg\/document\/crdt\/tree\.go has no Apache 2\.0 header/);
  assert.match(summary, /\+1 more/);
});

test('a `::error::` annotation is read when nothing else parses', () => {
  const out = `buf generate
::error::Generated code is stale. Run 'make tools && buf generate' and commit the result.
 M api/yorkie/v1/resources.pb.go`;
  assert.match(summarizeFailure(evidence('generic', out)), /^Generated code is stale/);
});

test('an unparseable failure still says something true', () => {
  // A lane that failed must never produce a blank cell: blank reads as
  // "nothing failed" in the table the fixing agent is handed.
  assert.equal(
    summarizeFailure({ kind: 'go-test', notable: [], tail: '', exitCode: 2 }),
    'exited 2 with no output',
  );
  assert.equal(
    summarizeFailure({ kind: 'generic', notable: [], tail: '', exitCode: null, signal: 'SIGKILL' }),
    'killed by SIGKILL, with no output',
  );
});

test('a missing tool is reported as one', () => {
  const out = 'bash: line 1: golangci-lint: command not found';
  assert.match(summarizeFailure(evidence('golangci', out)), /command not found/);
});

test('an unknown kind falls back to the generic reading rather than throwing', () => {
  assert.equal(
    summarizeFailure({ kind: 'not-a-kind', tail: 'something broke\n', exitCode: 1 }),
    'something broke',
  );
});

test('the summary is one line and bounded', () => {
  const noisy = `--- FAIL: ${'T'.repeat(400)} (0.0s)\nFAIL\tgithub.com/yorkie-team/yorkie/x\t0.1s`;
  const summary = summarizeFailure(evidence('go-test', noisy));
  assert.ok(summary.length <= MAX_SUMMARY_CHARS, `too long: ${summary.length}`);
  assert.doesNotMatch(summary, /\n/);
});

test('every kind the lanes declare has a parser path', () => {
  // A lane declaring a kind nothing handles would silently fall through to the
  // generic reading, which is the behaviour this list exists to make explicit.
  assert.deepEqual(
    [...LANE_KINDS].sort(),
    ['buf', 'generic', 'go-build', 'go-test', 'golangci', 'license'],
  );
});

test('notable lines are picked by shape, never by the word "error"', () => {
  // The regression this guards is the whole reason for the module: a passing
  // test that logs the word must not be retained, and a `--- FAIL:` that does
  // not contain it must be.
  assert.equal(isNotableLine('go-test', '    t.Log("no error here, all good")'), false);
  assert.equal(isNotableLine('go-test', '--- FAIL: TestX (0.0s)'), true);
  assert.equal(isNotableLine('go-test', 'WARNING: DATA RACE'), true);
  assert.equal(isNotableLine('go-test', '=== RUN   TestX'), false);
});
