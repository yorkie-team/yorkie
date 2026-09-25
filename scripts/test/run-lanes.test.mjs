// Tests for the lane runner.
//
// EVERYTHING THAT EXECUTES RUNS AGAINST A PLANTED MANIFEST IN A TEMP
// DIRECTORY. The real `LANES` manifest runs `make lint`, `buf generate` and a
// `-race` integration suite; a suite that invoked it would take twenty
// minutes, need MongoDB, and write `coverage.txt` into whatever tree it was
// launched from. `runLanes` takes the manifest as an argument precisely so
// this file never has to.
//
// The last section is the exception, and it is the same exception
// `harness-hooks.test.mjs` makes for itself: three guards read THIS tree's
// `ci.yml`, because the drift they exist to catch — a lane in the manifest
// that no step invokes, which the finish step then reports as `skip` forever —
// only exists between the two files. A planted copy of that pair would assert
// only that the test agrees with itself. They read; they never write.

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  collectSummary,
  createCapture,
  filteredReason,
  LANES,
  laneByName,
  REPORT_DIR,
  reportPath,
  runLane,
  runLanes,
  writeSummary,
} from '../ci/run-lanes.mjs';

const SCRIPT = fileURLToPath(new URL('../ci/run-lanes.mjs', import.meta.url));
const REPO = fileURLToPath(new URL('../..', import.meta.url));

/** A manifest of lanes that are instant and need no toolchain. */
const FAKE = [
  { name: 'alpha', title: 'Alpha', kind: 'generic', run: 'echo alpha-ok' },
  { name: 'beta', title: 'Beta', kind: 'go-test', run: 'echo "--- FAIL: TestBeta (0.0s)"; echo "FAIL\texample.com/pkg\t0.1s"; exit 1' },
  { name: 'gamma', title: 'Gamma', kind: 'generic', run: 'echo gamma-ok' },
  {
    name: 'delta',
    title: 'Delta',
    kind: 'generic',
    precondition: { env: 'LANE_FAKE_INPUT', why: 'no fake input on this event' },
    run: 'echo delta-ok',
  },
];

/**
 * Run `body` against a fresh temp directory and remove it afterwards.
 *
 * AWAITS AN ASYNC BODY. A plain `try/finally` around `return body(dir)`
 * deletes the directory the instant the promise is RETURNED, so a lane
 * spawned with `cwd` set to it starts in a directory that no longer exists —
 * which surfaces as `getcwd: cannot access parent directories` inside the
 * assertion, several layers from the cause.
 */
function withDir(body) {
  const dir = mkdtempSync(path.join(tmpdir(), 'run-lanes-'));
  const clean = () => rmSync(dir, { recursive: true, force: true });
  let result;
  try {
    result = body(dir);
  } catch (err) {
    clean();
    throw err;
  }
  if (result && typeof result.then === 'function') return result.finally(clean);
  clean();
  return result;
}

const readReport = (dir, name) => JSON.parse(readFileSync(reportPath(dir, name), 'utf8'));
const readSummary = (dir) => JSON.parse(readFileSync(path.join(dir, 'summary.json'), 'utf8'));

test('a passing lane is reported as pass, with its output measured', async () => {
  await withDir(async (dir) => {
    const report = await runLane(FAKE[0], { cwd: dir, env: {} });
    assert.equal(report.status, 'pass');
    assert.equal(report.exitCode, 0);
    assert.equal(report.summary, undefined, 'a passing lane needs no failure summary');
    assert.ok(report.outputBytes > 0);
    assert.match(report.tail, /alpha-ok/);
  });
});

test('a failing lane carries the one-line summary the summariser produced', async () => {
  await withDir(async (dir) => {
    const report = await runLane(FAKE[1], { cwd: dir, env: {} });
    assert.equal(report.status, 'fail');
    assert.equal(report.exitCode, 1);
    assert.match(report.summary, /TestBeta failed in example\.com\/pkg/);
    assert.ok(report.notable.some((l) => l.includes('--- FAIL: TestBeta')));
  });
});

test('a lane that cannot be started is reported, not thrown', async () => {
  // `bash -c` exists everywhere this runs, so the reachable shape of this is a
  // command inside it that does not — which is still a lane that failed, with
  // a summary that says which tool is missing.
  await withDir(async (dir) => {
    const report = await runLane(
      { name: 'x', title: 'X', kind: 'golangci', run: 'definitely-not-a-real-binary --now' },
      { cwd: dir, env: {} },
    );
    assert.equal(report.status, 'fail');
    assert.match(report.summary, /not found/);
  });
});

test('stdout and stderr are captured in arrival order, into one stream', async () => {
  // A Go compile error is on stderr and the test output around it is on
  // stdout; splitting them makes a report in which neither explains the other.
  await withDir(async (dir) => {
    const report = await runLane(
      { name: 'x', title: 'X', kind: 'generic', run: 'echo out; echo err >&2; exit 1' },
      { cwd: dir, env: {} },
    );
    assert.match(report.tail, /out/);
    assert.match(report.tail, /err/);
  });
});

test('the run stops at the first failing lane and says so in the summary', async () => {
  await withDir(async (dir) => {
    const { reports, summary } = await runLanes({ dir, lanes: FAKE, env: {}, cwd: dir });
    assert.deepEqual(reports.map((r) => `${r.lane}:${r.status}`), ['alpha:pass', 'beta:fail']);
    assert.equal(summary.status, 'fail');
    assert.deepEqual(
      summary.lanes.map((l) => `${l.lane}:${l.status}`),
      ['alpha:pass', 'beta:fail', 'gamma:skip', 'delta:filtered'],
    );
    assert.equal(summary.counts.fail, 1);
    assert.equal(summary.counts.skip, 1);
    assert.equal(summary.counts.filtered, 1);
  });
});

test('skip and filtered are different states, with different reasons', () => {
  // Conflating them makes the summary state something untrue: `gamma` was cut
  // short by an earlier failure and would run on a re-run, while `delta`
  // cannot run on this event at all.
  withDir((dir) => {
    const summary = collectSummary({ dir, lanes: FAKE, env: {} });
    const by = Object.fromEntries(summary.lanes.map((l) => [l.lane, l]));
    assert.equal(by.gamma.status, 'skip');
    assert.match(by.gamma.summary, /an earlier lane failed/);
    assert.equal(by.delta.status, 'filtered');
    assert.equal(by.delta.summary, 'no fake input on this event');
  });
});

test('a satisfied precondition makes the lane applicable again', async () => {
  await withDir(async (dir) => {
    const env = { LANE_FAKE_INPUT: 'abc123' };
    assert.equal(filteredReason(FAKE[3], env), null);
    const { summary } = await runLanes({ dir, lanes: [FAKE[3]], env, cwd: dir });
    assert.equal(summary.lanes[0].status, 'pass');
  });
});

test('a whitespace-only precondition value is still unset', () => {
  // GitHub expands an absent context to the empty string, and a `env: BASE: `
  // line with nothing after it yields a space. Both mean "not a pull request".
  assert.ok(filteredReason(FAKE[3], { LANE_FAKE_INPUT: '   ' }));
});

test('a lane with no precondition is never filtered', () => {
  // The fail-safe direction: a lane wrongly reported as filtered is one the
  // fixing agent is told it need not think about.
  for (const lane of LANES.filter((l) => !l.precondition)) {
    assert.equal(filteredReason(lane, {}), null, lane.name);
  }
});

test('the summary is written from the MANIFEST, not from the files present', () => {
  // The regression: a job that died at the first lane leaves one report, and a
  // summary assembled from the directory would not mention the other seven at
  // all — a run that reported one failure and, as far as any reader could
  // tell, seven lanes that do not exist.
  withDir((dir) => {
    writeFileSync(
      reportPath(dir, 'alpha'),
      JSON.stringify({ lane: 'alpha', status: 'fail', summary: 'boom', durationMs: 5 }),
    );
    const summary = writeSummary({ dir, lanes: FAKE, env: {} });
    assert.equal(summary.lanes.length, FAKE.length);
    assert.equal(readSummary(dir).lanes.length, FAKE.length);
  });
});

test('a summary in which nothing ran is incomplete, never pass', () => {
  withDir((dir) => {
    const summary = collectSummary({ dir, lanes: FAKE, env: {} });
    assert.equal(summary.status, 'incomplete');
  });
});

test('an unreadable report leaves the lane stated, not dropped', () => {
  withDir((dir) => {
    writeFileSync(reportPath(dir, 'alpha'), '{ truncated');
    const summary = collectSummary({ dir, lanes: FAKE, env: {} });
    assert.equal(summary.lanes[0].status, 'skip');
  });
});

test('the capture bounds the tail and keeps notable lines from anywhere', () => {
  // The failure this subsystem exists to fix: a race report megabytes before
  // the end of a `-race` run. The tail cannot hold it; the notable lines must.
  const capture = createCapture('go-test', { tailBytes: 256, maxNotable: 5 });
  capture.push('WARNING: DATA RACE\n');
  capture.push(`${'filler line that is not notable at all\n'.repeat(500)}`);
  capture.push('--- FAIL: TestLate (0.0s)\n');
  const { tail, notable, bytes } = capture.finish();
  assert.ok(bytes > 10_000);
  assert.ok(tail.length <= 256, `tail was ${tail.length}`);
  assert.ok(notable.includes('WARNING: DATA RACE'), 'the early race report was dropped');
  assert.ok(notable.includes('--- FAIL: TestLate (0.0s)'));
});

test('a passing verbose run does not evict the failure that follows it', () => {
  // THE BUG A REAL RUN FOUND. The patterns used to match `ok  <pkg>` and every
  // `<file>_test.go:NN:` line; against `go test -race -v ./...` those are most
  // of the output, so the budget filled with a hundred PASSING packages before
  // the stream reached the failing one. The `--- FAIL:` never made it in and
  // the summary came out as the word `FAIL`.
  const capture = createCapture('go-test', { tailBytes: 128, maxNotable: 60 });
  for (let i = 0; i < 200; i++) {
    capture.push(`=== RUN   TestPassing${i}\n`);
    capture.push(`    thing_test.go:${i}: some passing test logging\n`);
    capture.push(`--- PASS: TestPassing${i} (0.00s)\n`);
    capture.push(`ok  \tgithub.com/yorkie-team/yorkie/pkg/p${i}\t0.01s\n`);
  }
  capture.push('=== RUN   TestTreeEdit\n');
  capture.push('    tree_test.go:214: expected <p>ab</p>, got <p>a</p><p>b</p>\n');
  capture.push('--- FAIL: TestTreeEdit (0.02s)\n');
  capture.push('FAIL\tgithub.com/yorkie-team/yorkie/pkg/document/crdt\t0.3s\n');
  const { notable } = capture.finish();

  assert.ok(notable.some((l) => l.includes('--- FAIL: TestTreeEdit')), `evicted: ${notable.length} kept`);
  assert.ok(!notable.some((l) => /^ok\s/.test(l)), 'a passing package is not evidence');
  assert.ok(!notable.some((l) => l.includes('some passing test logging')));
  // The assertion output IS kept, by lookbehind — `go test` prints it directly
  // above the `--- FAIL:` it belongs to, which is the only place it is
  // distinguishable from the identical lines a passing test prints.
  assert.ok(
    notable.some((l) => l.includes('expected <p>ab</p>')),
    'the assertion above the failure was dropped',
  );
});

test('held context lines are released only by a failure, never on their own', () => {
  const capture = createCapture('go-test', { lookbehind: 4 });
  capture.push('    thing_test.go:1: passing chatter\n');
  capture.push('--- PASS: TestX (0.00s)\n');
  capture.push('    other_test.go:9: the assertion that matters\n');
  capture.push('--- FAIL: TestY (0.00s)\n');
  const { notable } = capture.finish();
  assert.deepEqual(notable, [
    '    other_test.go:9: the assertion that matters',
    '--- FAIL: TestY (0.00s)',
  ]);
});

test('a line split across two writes is still matched', () => {
  const capture = createCapture('go-test');
  capture.push('--- FAIL: Test');
  capture.push('Split (0.0s)\n');
  assert.deepEqual(capture.finish().notable, ['--- FAIL: TestSplit (0.0s)']);
});

test('the notable buffer is bounded, and says how many it dropped', () => {
  const capture = createCapture('go-test', { maxNotable: 2 });
  for (let i = 0; i < 10; i++) capture.push(`--- FAIL: Test${i} (0.0s)\n`);
  const { notable, notableDropped } = capture.finish();
  // The FIRST ones: the first failing test in a run is usually the cause and
  // the rest are collateral.
  assert.deepEqual(notable, ['--- FAIL: Test0 (0.0s)', '--- FAIL: Test1 (0.0s)']);
  assert.equal(notableDropped, 8);
});

test('the CLI lists exactly the manifest, in order', () => {
  // Every real lane needs the Go toolchain, so the CLI is exercised through
  // `--list` and `--finish` — the two paths `ci.yml` depends on that do not
  // execute a lane. The lane paths are covered above through `runLanes`.
  const r = spawnSync(process.execPath, [SCRIPT, '--list'], { encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(r.stdout.trim().split('\n'), LANES.map((l) => l.name));
});

test('the CLI refuses a lane name that is not in the manifest', () => {
  // A typo in `ci.yml` would otherwise be a step that ran nothing, exited 0,
  // and left the lane reported as `skip` — a gate silently removed from CI.
  const r = spawnSync(process.execPath, [SCRIPT, 'lnit'], { encoding: 'utf8' });
  assert.equal(r.status, 2, r.stdout);
  assert.match(r.stderr, /no such lane: lnit/);
});

test('the CLI --finish exits 0 even when a lane failed', () => {
  // It runs `if: always()` after the lane that already reported the failure;
  // failing here a second time adds a red step that names nothing.
  withDir((dir) => {
    writeFileSync(
      reportPath(dir, 'lint'),
      JSON.stringify({ lane: 'lint', status: 'fail', summary: 'boom' }),
    );
    const r = spawnSync(process.execPath, [SCRIPT, '--finish', '--dir', dir], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /1 failed/);
    const summary = readSummary(dir);
    assert.equal(summary.status, 'fail');
    assert.equal(summary.lanes.length, LANES.length);
  });
});

test('a report lands at one predictable path per lane', () => {
  withDir((dir) => {
    assert.equal(reportPath(dir, 'test'), path.join(dir, 'lane-test.json'));
  });
});

// ---------------------------------------------------------------------------
// Against THIS tree. See the header for why these three are not planted.
// ---------------------------------------------------------------------------

const ciWorkflow = () => readFileSync(path.join(REPO, '.github', 'workflows', 'ci.yml'), 'utf8');

test('every lane in the manifest is invoked by a step in ci.yml', () => {
  // The silent failure: add a lane here, forget the step, and the finish step
  // reports it as `skip` on every green run forever — a gate that exists in
  // the manifest and runs nowhere.
  const wf = ciWorkflow();
  const invoked = new Set(
    [...wf.matchAll(/run-lanes\.mjs ([a-z-]+)/g)].map((m) => m[1]).filter((n) => n !== '--finish'),
  );
  assert.deepEqual([...invoked].sort(), LANES.map((l) => l.name).sort());
});

test('ci.yml still runs the licence gate, where the CI-fix loop can see it', () => {
  // Moved here from harness-hooks.test.mjs when the step became a lane. The
  // fact being pinned is unchanged and the reason is unchanged: `make verify`
  // needs Node locally, and CI is the only workflow `agent-iterate-ci.yml`
  // subscribes to — a gate that reds anywhere else stops an agent-managed PR
  // with nothing watching it. What changed is where the command is spelled,
  // so the assertion is now made of two halves that must BOTH hold.
  assert.match(ciWorkflow(), /run-lanes\.mjs license\b/);
  assert.equal(laneByName('license').run, 'node scripts/verify-license.mjs');
});

test('ci.yml finishes the summary unconditionally and uploads the reports', () => {
  // Without `--finish` under `if: always()` there is no summary.json on a red
  // run, which is every run the consumer cares about.
  const wf = ciWorkflow();
  const at = wf.indexOf('run-lanes.mjs --finish');
  assert.ok(at > 0, 'no finish step in ci.yml');
  assert.match(wf.slice(Math.max(0, at - 400), at), /if:\s*always\(\)/);
  assert.match(wf, new RegExp(`path:\\s*${REPORT_DIR.replace('.', '\\.')}/`));
  assert.match(wf, /include-hidden-files:\s*true/);
});

test('the reports directory is gitignored, and is not the scratch one', () => {
  // `.harness-reports/` was already taken: `scripts/agent/novelty.test.mjs`
  // creates throwaway git repositories under it. Sharing the name would put a
  // nested `.git` inside the artifact upload's search root.
  const ignore = readFileSync(path.join(REPO, '.gitignore'), 'utf8');
  assert.match(ignore, new RegExp(`^${REPORT_DIR.replace('.', '\\.')}/$`, 'm'));
  assert.notEqual(REPORT_DIR, '.harness-reports');
  assert.match(ignore, /^\.harness-reports\/$/m, 'the scratch directory must stay ignored too');
});
