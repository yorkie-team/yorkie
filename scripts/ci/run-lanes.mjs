// Copyright 2026 The Yorkie Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Run `ci.yml`'s `build` job as NAMED LANES, and write a machine-readable
// report for each one.
//
// WHY THIS EXISTS. `agent-iterate-ci.yml` dispatches a fixing agent when CI
// reds on an agent-managed branch, and its whole diagnosis was
// `gh run view --log-failed | tail -c 40000`. For the lane that fails most
// expensively — `go test -tags integration -race -v ./...`, which prints tens
// of megabytes — the last 40 KB is whichever packages finished last, and the
// `--- FAIL:` line may be megabytes earlier. The fixer was asked to converge
// in one round from output that need not contain the failure. These reports
// are what `summarize-ci.mjs` renders instead.
//
// A WRAPPER, NOT A REPLACEMENT. Every `run:` below is the literal command the
// `ci.yml` step it replaced ran (`modernize`, added later, replaced no step and
// runs the same `make verify-modernize` that `make verify` does). That is
// deliberate and load-bearing twice over: `review-panel.mjs`'s
// `MECHANICAL_COVERAGE_NOTE` and `docs/design/agent-command-verbs.md` §4b are
// an inventory of what CI proves, and both stay true only while this file
// changes how a lane is REPORTED and never what it RUNS. A lane that adds a
// check updates both inventories in the same change.
//
// ONE LANE PER INVOCATION IN CI, which is why `ci.yml` still has a step per
// lane. The alternative — one invocation running the whole manifest — would
// have collapsed nine named steps into one, so a human reading a red run
// would see "Run the CI lanes" fail rather than "Lint", and the two lanes with
// a condition of their own would have had to move their `if:` into JavaScript.
// Run with no arguments it does execute the whole manifest in order, stopping
// at the first failure, which is what makes it useful locally.
//
// FOUR STATES, AND `skip` IS NOT `filtered`:
//   pass      the lane ran and exited 0
//   fail      the lane ran and did not
//   skip      the lane never ran because an earlier one failed
//   filtered  the lane never ran because a precondition did not hold
// Conflating the last two makes the summary state something untrue. Today the
// only precondition is `proto-breaking`'s base commit, which exists on a pull
// request and not on a push to `main`: reporting that as `skip` would tell a
// fixing agent that a lane was cut short by an earlier failure when in fact
// the lane cannot run on this event at all. This is NOT path filtering — see
// docs/design/ci-lane-reports.md for why that is `ci.yml`'s job and not this
// file's.

import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { isDirectRun } from '../direct-run.mjs';
import {
  CONTEXT_LOOKBEHIND,
  isContextLine,
  isNeutralLine,
  isNotableLine,
  MAX_NOTABLE_LINES,
  summarizeFailure,
} from './lane-failure.mjs';

const PREFIX = '[ci:lanes]';

/** Where the reports go, relative to the repository root. */
export const REPORT_DIR = '.ci-reports';

/**
 * How much of each lane's raw output a report keeps.
 *
 * 16 KB, against the 40 KB the whole diagnosis used to be. The tail is the
 * WEAKEST evidence in a report — the notable lines are retained as they
 * stream past and do not depend on where in the output the failure was — so
 * it is sized for "enough context around the end" rather than for "enough to
 * find the failure in".
 */
export const TAIL_BYTES = 16 * 1024;

/**
 * The lanes, in the order `ci.yml`'s `build` job runs them.
 *
 * `run` is a shell script, executed with `bash -e -c`, because that is what a
 * `run:` step is: `codegen-fresh` is multi-command, and re-expressing it as an
 * argv array would change it while porting it.
 *
 * `-e` IS NOT OPTIONAL. GitHub's default `run:` shell is `bash -e {0}`, and
 * no step in `ci.yml` overrides it. Without the flag a multi-command lane
 * keeps going after a failure and reports the LAST command's status:
 * `codegen-fresh` would run `buf generate`, ignore it failing, find `api/`
 * clean because nothing regenerated, and report **pass** — removing a CI gate
 * while the run stays green, which is the failure this subsystem exists to
 * make impossible.
 *
 * `-o pipefail` is deliberately NOT added even though it is the more careful
 * flag. GitHub applies it only when a step sets `shell: bash` explicitly, and
 * none here does; adding it would make a lane stricter than the step it
 * replaced. Same fidelity rule, other direction.
 * Every string here is repository-controlled; nothing from a pull request
 * reaches it.
 *
 * `kind` picks the parser in lane-failure.mjs. `precondition` names an
 * environment variable that must be non-empty for the lane to be applicable
 * at all, and the reason, which is what the `filtered` state reports.
 */
export const LANES = Object.freeze([
  {
    name: 'lint',
    title: 'golangci-lint',
    kind: 'golangci',
    run: 'make lint',
  },
  {
    name: 'license',
    title: 'Apache 2.0 headers',
    kind: 'license',
    // `node …`, not `make verify-license`: that target announces SKIPPED when
    // Node is absent, and a CI gate must not fail open. The Makefile's own
    // comment says the same thing from the other side.
    run: 'node scripts/verify-license.mjs',
  },
  {
    name: 'proto-lint',
    title: 'buf lint',
    kind: 'buf',
    run: 'buf lint',
  },
  {
    name: 'proto-breaking',
    title: 'buf breaking, against this PR’s base commit',
    kind: 'buf',
    precondition: {
      env: 'LANE_BASE_SHA',
      why: 'no pull-request base commit on this event',
    },
    run: 'buf breaking --against "https://github.com/yorkie-team/yorkie.git#ref=$LANE_BASE_SHA"',
  },
  {
    name: 'codegen-fresh',
    title: 'Generated code is up to date',
    kind: 'generic',
    run: [
      'buf generate',
      'if [ -n "$(git status --porcelain --untracked-files=all -- api/)" ]; then',
      "  echo \"::error::Generated code is stale. Run 'make tools && buf generate' and commit the result.\"",
      '  git status --short -- api/',
      '  git diff -- api/',
      '  exit 1',
      'fi',
    ].join('\n'),
  },
  {
    name: 'build',
    title: 'make build',
    kind: 'go-build',
    run: 'make build',
  },
  {
    name: 'vet-tagged',
    title: 'go vet over the tag-gated reproductions',
    kind: 'go-build',
    run: 'go vet -tags rgafuzz ./...',
  },
  {
    name: 'modernize',
    title: 'go fix has nothing to rewrite, under every build tag',
    kind: 'generic',
    run: 'make verify-modernize',
  },
  {
    name: 'test',
    title: 'Integration tests, with the race detector',
    kind: 'go-test',
    run: 'go test -tags integration -race -coverpkg=./... -coverprofile=coverage.txt -covermode=atomic -v ./...',
  },
]);

/** The lane with this name, or undefined. */
export function laneByName(name, lanes = LANES) {
  return lanes.find((l) => l.name === name);
}

/**
 * Why this lane cannot apply to this run, or null when it can.
 *
 * A lane with no `precondition` is always applicable — the fail-safe
 * direction, since a lane wrongly reported as `filtered` is one a fixing
 * agent is told it need not think about.
 */
export function filteredReason(lane, env = process.env) {
  const pre = lane.precondition;
  if (!pre) return null;
  return String(env[pre.env] ?? '').trim() === '' ? pre.why : null;
}

/** Where a lane's report is written. */
export function reportPath(dir, name) {
  return path.join(dir, `lane-${name}.json`);
}

function writeJson(file, value) {
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

/**
 * A bounded view of a stream: the last `TAIL_BYTES`, plus the notable lines
 * as they go past.
 *
 * NEITHER IS THE OTHER'S FALLBACK. The tail alone loses a race report that
 * fired early in a forty-megabyte run; the notable lines alone lose the
 * ordinary context around a failure that matched no pattern. Keeping both,
 * each bounded, is what makes a report a fixed size regardless of how loud
 * the lane was.
 */
export function createCapture(
  kind,
  { tailBytes = TAIL_BYTES, maxNotable = MAX_NOTABLE_LINES, lookbehind = CONTEXT_LOOKBEHIND } = {},
) {
  let tail = '';
  let bytes = 0;
  let pending = '';
  const notable = [];
  let notableDropped = 0;
  // The last few context-shaped lines, held until a failure-shaped line
  // arrives. `go test` prints an assertion's output directly above the
  // `--- FAIL:` it belongs to, so this is where the useful half of a Go
  // failure lives — and it is also the single most common line in a PASSING
  // verbose run, which is why it cannot simply be matched and kept.
  let recent = [];

  const keep = (line) => {
    if (notable.length < maxNotable) notable.push(line);
    else notableDropped += 1;
  };

  const takeLine = (line) => {
    if (isNotableLine(kind, line)) {
      for (const held of recent) keep(held);
      recent = [];
      keep(line);
      return;
    }
    if (isContextLine(kind, line)) {
      recent.push(line);
      if (recent.length > lookbehind) recent.shift();
      return;
    }
    // `go test`'s own bookkeeping is NEITHER, and must not clear the
    // lookbehind. Whenever a failing subtest is not the last under its parent,
    // `=== RUN Parent/next_case` is printed between the assertion and the
    // `--- FAIL:` that makes it notable — so treating it as "something else"
    // discards the one line naming what went wrong. A small lane survives that
    // because the tail rescues it; the 40 MB `-race` lane this exists for does
    // not.
    if (isNeutralLine(kind, line)) return;
    // Anything else ends the run of context: the lines held above belong to
    // whatever was printing then, and a failure further down has its own.
    if (line.trim()) recent = [];
  };

  return {
    push(chunk) {
      const text = String(chunk);
      bytes += Buffer.byteLength(text);
      tail = (tail + text).slice(-tailBytes);
      pending += text;
      const lines = pending.split('\n');
      // The last element is a partial line; hold it for the next chunk so a
      // pattern is never missed because a write landed mid-line.
      pending = lines.pop() ?? '';
      for (const line of lines) takeLine(line.replace(/\r$/, ''));
    },
    finish() {
      if (pending) {
        takeLine(pending);
        pending = '';
      }
      return { tail, bytes, notable, notableDropped };
    },
  };
}

/**
 * Run one lane and return its report. Does not write anything.
 *
 * `onOutput` receives every chunk so the caller can forward it to the job
 * log: the report is a summary, and the full output still belongs in the run
 * where a human can read it.
 */
export function runLane(lane, { cwd = process.cwd(), env = process.env, onOutput } = {}) {
  return new Promise((resolve) => {
    const started = Date.now();
    const capture = createCapture(lane.kind);
    const child = spawn('bash', ['-e', '-c', lane.run], {
      cwd,
      env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    // Both streams into one capture, in arrival order: a Go compile error is
    // on stderr and the test output around it is on stdout, and splitting
    // them makes a report in which neither explains the other.
    for (const stream of [child.stdout, child.stderr]) {
      stream.setEncoding('utf8');
      stream.on('data', (chunk) => {
        capture.push(chunk);
        if (onOutput) onOutput(chunk);
      });
    }
    const done = (exitCode, signal, spawnError) => {
      const { tail, bytes, notable, notableDropped } = capture.finish();
      const ok = spawnError == null && exitCode === 0 && signal == null;
      const report = {
        schema: 1,
        lane: lane.name,
        title: lane.title,
        kind: lane.kind,
        command: lane.run,
        status: ok ? 'pass' : 'fail',
        exitCode: exitCode ?? null,
        signal: signal ?? null,
        durationMs: Date.now() - started,
        outputBytes: bytes,
        notable,
        notableDropped,
        tail,
        tailTruncated: bytes > Buffer.byteLength(tail),
      };
      if (!ok) {
        report.summary = spawnError
          ? `could not start the lane: ${spawnError.message}`
          : summarizeFailure({ kind: lane.kind, notable, tail, exitCode, signal });
      }
      resolve(report);
    };
    child.on('error', (err) => done(null, null, err));
    child.on('close', (code, signal) => done(code, signal, null));
  });
}

/**
 * Read whatever reports are on disk, then state every lane in the manifest.
 *
 * WRITTEN FROM THE MANIFEST, NOT FROM THE DIRECTORY, which is the whole point
 * of a separate finish step. A lane that never ran leaves no file, and a
 * summary assembled from the files present would simply not mention it — so a
 * run that died at `lint` would report one failure and eight lanes that, as
 * far as any reader could tell, did not exist. Naming them as `skip` (or
 * `filtered`) is what makes the summary a statement about the whole job.
 */
export function collectSummary({ dir = REPORT_DIR, lanes = LANES, env = process.env } = {}) {
  const rows = [];
  for (const lane of lanes) {
    let report = null;
    try {
      report = JSON.parse(readFileSync(reportPath(dir, lane.name), 'utf8'));
    } catch {
      report = null;
    }
    if (report) {
      rows.push({
        lane: lane.name,
        title: lane.title,
        status: report.status,
        summary: report.summary ?? null,
        durationMs: report.durationMs ?? null,
        exitCode: report.exitCode ?? null,
      });
      continue;
    }
    const filtered = filteredReason(lane, env);
    rows.push({
      lane: lane.name,
      title: lane.title,
      status: filtered ? 'filtered' : 'skip',
      summary: filtered ?? 'did not run — an earlier lane failed, or the job ended first',
      durationMs: null,
      exitCode: null,
    });
  }
  const counts = { pass: 0, fail: 0, skip: 0, filtered: 0 };
  for (const row of rows) counts[row.status] = (counts[row.status] ?? 0) + 1;
  return {
    schema: 1,
    generatedAt: new Date().toISOString(),
    // `fail` whenever any lane failed. ANY skip makes the job `incomplete`,
    // not `pass`: a lane is only skipped here because it wrote no report, and
    // when no lane failed that means something ended the job instead — the
    // runner was killed, the job cancelled, a non-lane step failed before the
    // lane ran. Reporting `pass` for six-of-eight would be a machine-readable
    // statement that the job succeeded, about a job that did not.
    status:
      counts.fail > 0
        ? 'fail'
        : counts.skip > 0
          ? 'incomplete'
          : counts.pass > 0
            ? 'pass'
            : 'incomplete',
    counts,
    lanes: rows,
    repository: env.GITHUB_REPOSITORY ?? null,
    runId: env.GITHUB_RUN_ID ?? null,
    sha: env.GITHUB_SHA ?? null,
    ref: env.GITHUB_REF_NAME ?? null,
  };
}

/** Write `summary.json` over the whole manifest and return it. */
export function writeSummary(options = {}) {
  const dir = options.dir ?? REPORT_DIR;
  const summary = collectSummary(options);
  writeJson(path.join(dir, 'summary.json'), summary);
  return summary;
}

/**
 * Run the named lanes in manifest order, writing a report for each, and
 * rewrite the summary afterwards.
 *
 * STOPS AT THE FIRST FAILURE, which is what `ci.yml` does with its steps and
 * therefore what the reports have to describe. Running on would change what
 * CI costs on a red branch, which is a decision about the pipeline and not
 * one this file gets to make on the way past.
 */
export async function runLanes({
  dir = REPORT_DIR,
  lanes = LANES,
  select = null,
  cwd = process.cwd(),
  env = process.env,
  onOutput,
  onLane,
} = {}) {
  const chosen = select ? lanes.filter((l) => select.includes(l.name)) : lanes;
  const reports = [];
  for (const lane of chosen) {
    const filtered = filteredReason(lane, env);
    if (filtered) {
      if (onLane) onLane({ lane: lane.name, status: 'filtered', summary: filtered });
      continue;
    }
    const report = await runLane(lane, { cwd, env, onOutput });
    writeJson(reportPath(dir, lane.name), report);
    reports.push(report);
    if (onLane) onLane(report);
    if (report.status === 'fail') break;
  }
  const summary = writeSummary({ dir, lanes, env });
  return { reports, summary };
}

function parseArgs(argv) {
  const opts = { dir: null, finish: false, list: false, select: [] };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--finish') opts.finish = true;
    else if (arg === '--list') opts.list = true;
    else if (arg === '--dir') opts.dir = argv[++i];
    else if (arg.startsWith('--dir=')) opts.dir = arg.slice('--dir='.length);
    else if (arg.startsWith('-')) throw new Error(`unknown option ${arg}`);
    else opts.select.push(arg);
  }
  return opts;
}

if (isDirectRun(import.meta.url)) {
  const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
  let opts = null;
  try {
    opts = parseArgs(process.argv.slice(2));
  } catch (err) {
    console.error(`${PREFIX} ${err.message}`);
    console.error(`${PREFIX} usage: run-lanes.mjs [--dir <path>] [--finish] [--list] [lane...]`);
    process.exitCode = 2;
  }
  const dir = opts ? path.resolve(repoRoot, opts.dir ?? REPORT_DIR) : null;

  // AN UNKNOWN LANE NAME IS FATAL. `ci.yml` names its lanes as literals, so a
  // typo there would otherwise be a step that ran nothing, exited 0, and left
  // the lane reported as `skip` — a gate silently removed from CI, which is
  // the one failure mode this whole subsystem exists to make impossible.
  const unknown = (opts?.select ?? []).filter((name) => !laneByName(name));

  // `process.exitCode`, never `process.exit()`, on every path that has
  // written output: this process forwards a lane's whole stdout, and
  // `process.exit` discards whatever of a pipe's buffer has not drained. A
  // truncated job log is the failure this subsystem exists to end.
  if (!opts) {
    // The usage message is already out; nothing below may run.
  } else if (opts.list) {
    for (const lane of LANES) console.log(lane.name);
  } else if (unknown.length > 0) {
    console.error(`${PREFIX} no such lane: ${unknown.join(', ')} (have: ${LANES.map((l) => l.name).join(', ')})`);
    process.exitCode = 2;
  } else if (opts.finish) {
    const summary = writeSummary({ dir, env: process.env });
    const { pass, fail, skip, filtered } = summary.counts;
    console.log(
      `${PREFIX} ${fail} failed, ${pass} passed, ${skip} skipped, ${filtered} filtered → ${path.join(dir, 'summary.json')}`,
    );
    // EXIT 0 EVEN WHEN A LANE FAILED. This step runs `if: always()` after the
    // lane that already reported the failure; failing here a second time
    // would add a red step that names nothing.
  } else {
    const { reports } = await runLanes({
      dir,
      select: opts.select.length ? opts.select : null,
      cwd: repoRoot,
      env: process.env,
      onOutput: (chunk) => process.stdout.write(chunk),
      onLane: (report) => {
        if (report.status === 'fail') console.log(`${PREFIX} ${report.lane}: FAIL — ${report.summary}`);
        else if (report.status === 'filtered') console.log(`${PREFIX} ${report.lane}: filtered (${report.summary})`);
        else console.log(`${PREFIX} ${report.lane}: pass (${Math.round((report.durationMs ?? 0) / 1000)}s)`);
      },
    });
    if (reports.some((r) => r.status === 'fail')) process.exitCode = 1;
  }
}
