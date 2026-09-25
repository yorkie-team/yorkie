// Tests for the renderer that turns the lane reports into the block the
// CI-fix agent reads.
//
// PLANTED REPORTS UNDER THE OS TEMP DIRECTORY, never this repository's own
// `.ci-reports/`: the interesting cases are a MISSING directory and a
// TRUNCATED report, and neither can be arranged in a tree something else is
// writing.
//
// The property under test throughout is the contract in the module header —
// say nothing, or say something useful. Every "renders nothing" case below is
// a case where `agent-iterate-ci.yml` must fall back to the log tail, and a
// renderer that emitted a table of skipped lanes instead would take that
// fallback away while looking like it had worked.

import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  loadReports,
  MAX_BLOCK_CHARS,
  NOTHING_TO_RENDER,
  renderDiagnosis,
} from '../ci/summarize-ci.mjs';

const SCRIPT = fileURLToPath(new URL('../ci/summarize-ci.mjs', import.meta.url));

const SUMMARY = {
  schema: 1,
  status: 'fail',
  counts: { pass: 2, fail: 1, skip: 1, filtered: 1 },
  lanes: [
    { lane: 'lint', title: 'golangci-lint', status: 'pass', durationMs: 42_000, summary: null },
    { lane: 'license', title: 'Apache 2.0 headers', status: 'pass', durationMs: 900, summary: null },
    {
      lane: 'test',
      title: 'Integration tests, with the race detector',
      status: 'fail',
      durationMs: 610_000,
      summary: 'TestTreeEdit/split failed in github.com/yorkie-team/yorkie/pkg/document/crdt',
    },
    { lane: 'build', title: 'make build', status: 'skip', durationMs: null, summary: 'an earlier lane failed, so this one never ran' },
    { lane: 'proto-breaking', title: 'buf breaking', status: 'filtered', durationMs: null, summary: 'no pull-request base commit on this event' },
  ],
};

const TEST_REPORT = {
  schema: 1,
  lane: 'test',
  title: 'Integration tests, with the race detector',
  kind: 'go-test',
  command: 'go test -tags integration -race -v ./...',
  status: 'fail',
  exitCode: 1,
  durationMs: 610_000,
  outputBytes: 41_000_000,
  notable: ['    --- FAIL: TestTreeEdit/split (0.01s)', 'FAIL\tgithub.com/yorkie-team/yorkie/pkg/document/crdt\t0.3s'],
  notableDropped: 4,
  tail: 'ok  \tgithub.com/yorkie-team/yorkie/pkg/units\t0.004s\nFAIL\n',
  tailTruncated: true,
};

function withReports(files, body) {
  const dir = mkdtempSync(path.join(tmpdir(), 'summarize-ci-'));
  try {
    for (const [name, value] of Object.entries(files)) {
      writeFileSync(path.join(dir, name), typeof value === 'string' ? value : JSON.stringify(value));
    }
    return body(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

const planted = (extra = {}) =>
  withReports({ 'summary.json': SUMMARY, 'lane-test.json': TEST_REPORT, ...extra }, (dir) =>
    renderDiagnosis(loadReports(dir)),
  );

test('the block names the failing lane and what failed, near the top', () => {
  const block = planted();
  assert.match(block, /Failed lane: `test`/);
  assert.match(block, /TestTreeEdit\/split failed in github\.com\/yorkie-team\/yorkie\/pkg\/document\/crdt/);
  // The conclusion has to survive being read alone: an agent that stops at the
  // first screen must already know which lane and which test.
  assert.ok(block.indexOf('TestTreeEdit/split failed') < 1200, 'the conclusion is buried');
});

test('every lane is stated, including the ones that never ran', () => {
  const block = planted();
  for (const lane of SUMMARY.lanes) assert.match(block, new RegExp(`\`${lane.lane}\``));
  // `skip` and `filtered` are rendered as the different things they are.
  assert.match(block, /skipped \(an earlier lane failed\)/);
  assert.match(block, /filtered \(not applicable to this run\)/);
});

test('the reproduction command is quoted verbatim', () => {
  assert.match(planted(), /go test -tags integration -race -v \.\/\.\.\./);
});

test('the notable lines are rendered, with a count of the ones dropped', () => {
  const block = planted();
  assert.match(block, /--- FAIL: TestTreeEdit\/split/);
  assert.match(block, /\(4 more matched\)/);
});

test('the size of the output the tail came from is stated', () => {
  // 41 MB is the number that explains why the old 40 KB log tail was not a
  // diagnosis; a reader who does not see it will not know the tail is a tail.
  assert.match(planted(), /41000000 bytes of output/);
});

test('nothing is rendered when no lane failed', () => {
  // The contract the consumer's fallback depends on. A green summary means the
  // red run died somewhere these lanes do not cover, and the log tail is then
  // strictly the better diagnosis.
  const green = { ...SUMMARY, status: 'pass', counts: { pass: 5, fail: 0, skip: 0, filtered: 0 }, lanes: SUMMARY.lanes.map((l) => ({ ...l, status: 'pass' })) };
  withReports({ 'summary.json': green }, (dir) => {
    assert.equal(renderDiagnosis(loadReports(dir)), '');
  });
});

test('nothing is rendered when the reports are absent', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'summarize-ci-'));
  rmSync(dir, { recursive: true, force: true });
  assert.equal(renderDiagnosis(loadReports(dir)), '');
});

test('nothing is rendered when every lane skipped', () => {
  // A job that died before the first lane — `make tools`, a full disk. There
  // is a real failure and these reports do not contain it.
  const nothing = {
    ...SUMMARY,
    status: 'incomplete',
    counts: { pass: 0, fail: 0, skip: 5, filtered: 0 },
    lanes: SUMMARY.lanes.map((l) => ({ ...l, status: 'skip' })),
  };
  withReports({ 'summary.json': nothing }, (dir) => {
    assert.equal(renderDiagnosis(loadReports(dir)), '');
  });
});

test('a truncated lane report still leaves the lane in the table', () => {
  // A partial artifact download loses the detail, not the fact that the lane
  // failed — which is in summary.json and is the more important half.
  const block = withReports({ 'summary.json': SUMMARY, 'lane-test.json': '{ truncated' }, (dir) =>
    renderDiagnosis(loadReports(dir)),
  );
  assert.match(block, /Failed lane: `test`/);
  assert.match(block, /TestTreeEdit\/split failed/);
  assert.doesNotMatch(block, /go test -tags integration/);
});

test('a truncated summary.json renders nothing rather than half a claim', () => {
  withReports({ 'summary.json': '{ truncated', 'lane-test.json': TEST_REPORT }, (dir) => {
    assert.equal(renderDiagnosis(loadReports(dir)), '');
  });
});

test('the block is bounded, and the bound cuts context before conclusions', () => {
  const loud = { ...TEST_REPORT, tail: 'x'.repeat(500_000) };
  const block = withReports({ 'summary.json': SUMMARY, 'lane-test.json': loud }, (dir) =>
    renderDiagnosis(loadReports(dir)),
  );
  assert.ok(block.length <= MAX_BLOCK_CHARS + 80, `block was ${block.length}`);
  assert.match(block, /TestTreeEdit\/split failed/);
});

test('output that contains a code fence cannot close the block early', () => {
  // Go test output quotes markdown routinely. A three-backtick fence around it
  // closes early and spills the rest of the diagnosis into the prompt as
  // prose — and, in a prompt, prose is instructions.
  const quoting = { ...TEST_REPORT, tail: 'got:\n```\nnot a fence\n```\n' };
  const block = withReports({ 'summary.json': SUMMARY, 'lane-test.json': quoting }, (dir) =>
    renderDiagnosis(loadReports(dir)),
  );
  assert.match(block, /````/, 'the fence was not widened past the one in the output');
});

test('a pipe in a summary cannot break out of the table', () => {
  const piped = {
    ...SUMMARY,
    lanes: [{ lane: 'lint', title: 'golangci-lint', status: 'fail', durationMs: 1, summary: 'a | b | c' }],
  };
  const block = withReports({ 'summary.json': piped }, (dir) => renderDiagnosis(loadReports(dir)));
  assert.match(block, /a \\\| b \\\| c/);
});

test('the CLI prints the block on stdout and exits 0', () => {
  withReports({ 'summary.json': SUMMARY, 'lane-test.json': TEST_REPORT }, (dir) => {
    const r = spawnSync(process.execPath, [SCRIPT, '--dir', dir], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /Failed lane: `test`/);
  });
});

test('the CLI writes NOTHING to stdout when there is nothing to render', () => {
  // `DIAG="$(node … || true)"` in the consumer keys its fallback off an empty
  // capture, so a note on stdout here would read as a diagnosis.
  withReports({}, (dir) => {
    const r = spawnSync(process.execPath, [SCRIPT, '--dir', dir], { encoding: 'utf8' });
    assert.equal(r.status, NOTHING_TO_RENDER);
    assert.equal(r.stdout, '');
    assert.match(r.stderr, /nothing to render/);
  });
});

test('the CLI accepts an absolute directory outside the repository', () => {
  // Which is where the consumer puts it: the artifact is unpacked into
  // `$RUNNER_TEMP`, never into the untrusted branch checkout.
  withReports({ 'summary.json': SUMMARY }, (dir) => {
    assert.ok(path.isAbsolute(dir));
    const r = spawnSync(process.execPath, [SCRIPT, `--dir=${dir}`], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /CI lane report/);
  });
});
