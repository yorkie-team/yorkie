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

// Render the lane reports into the block `agent-iterate-ci.yml` puts in front
// of the fixing agent.
//
// THE CONTRACT IS "SAY NOTHING OR SAY SOMETHING USEFUL". This exits 3 and
// prints nothing on stdout whenever no lane FAILED — including when the
// reports are missing, unreadable, or describe a job that died before any
// lane ran (a `make tools` failure, a runner that ran out of disk). The
// consumer keys its fallback off exactly that: empty output means `gh run view
// --log-failed` is still the better diagnosis, and a half-wired chain that
// hands an agent a table of eight skipped lanes and no cause is worse than an
// unwired one. `agent-iterate-ci.yml`'s diagnosis step is where the fallback
// lives; nothing here should ever emit a block it cannot stand behind.
//
// BOUNDED, AND THE BOUND IS NOT A TAIL. The old diagnosis was 40 KB of
// whatever finished last. This is ~8 KB of: the status of every lane, the
// failing lane's command, one line naming what failed, the lines the runner
// retained as it streamed (which do not depend on where in the output the
// failure was), and only then the tail. Each section is cut before the next
// one starts, so the strongest evidence is never the part that gets dropped.

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { isDirectRun } from '../direct-run.mjs';

const PREFIX = '[ci:summarize]';

/** Exit code for "there is nothing here worth rendering". */
export const NOTHING_TO_RENDER = 3;

/** How large the rendered block may get, in characters. */
export const MAX_BLOCK_CHARS = 8000;

/** How much of a failing lane's retained tail is rendered. */
export const MAX_TAIL_CHARS = 3000;

/** How many of the runner's retained notable lines are rendered. */
export const MAX_NOTABLE_RENDERED = 25;

/**
 * Read `summary.json` and every lane report in `dir`.
 *
 * A MISSING OR UNPARSEABLE FILE IS NOT AN ERROR HERE. The artifact can be
 * absent for perfectly ordinary reasons — the producing job was cancelled,
 * the branch predates this subsystem — and the caller's answer to all of them
 * is the same fallback. Returning an empty reading lets `renderDiagnosis`
 * make that call in one place.
 */
export function loadReports(dir) {
  let summary = null;
  try {
    summary = JSON.parse(readFileSync(path.join(dir, 'summary.json'), 'utf8'));
  } catch {
    summary = null;
  }
  const reports = new Map();
  let names = [];
  try {
    names = readdirSync(dir);
  } catch {
    names = [];
  }
  for (const name of names) {
    const m = /^lane-(.+)\.json$/.exec(name);
    if (!m) continue;
    try {
      reports.set(m[1], JSON.parse(readFileSync(path.join(dir, name), 'utf8')));
    } catch {
      // A truncated upload is a report that does not exist. The lane still
      // appears in the table from summary.json.
    }
  }
  return { summary, reports };
}

const ICON = { pass: '✅', fail: '❌', skip: '⏭️', filtered: '⚪' };

function seconds(ms) {
  if (ms == null) return '';
  return `${(ms / 1000).toFixed(1)}s`;
}

/** Escape the pipes that would otherwise break out of a markdown table cell. */
function cell(text) {
  return String(text ?? '').replace(/\|/g, '\\|').replace(/\n/g, ' ');
}

function fence(body, language = '') {
  // A fence long enough to survive a body that contains one. Test output
  // routinely quotes markdown, and a three-backtick fence around it closes
  // early and spills the rest of the block into the prompt as prose.
  let ticks = '```';
  while (body.includes(ticks)) ticks += '`';
  return `${ticks}${language}\n${body}\n${ticks}`;
}

function clip(text, max) {
  const s = String(text ?? '');
  if (s.length <= max) return s;
  // Keep the END of raw output: whatever the runner retained is already the
  // tail of the stream, and its last lines are the closest to the failure.
  return `…(${s.length - max} characters omitted)…\n${s.slice(-max)}`;
}

/**
 * The block, or an empty string when there is nothing worth rendering.
 *
 * @param {{summary: object|null, reports: Map<string, object>}} reading
 */
export function renderDiagnosis(reading, { maxChars = MAX_BLOCK_CHARS } = {}) {
  const { summary, reports } = reading;
  const rows = summary?.lanes ?? [];
  const failed = rows.filter((r) => r.status === 'fail');
  // NO FAILING LANE, NO BLOCK. See this file's header: the caller's fallback
  // is strictly better than anything that could be said here.
  if (failed.length === 0) return '';

  const counts = summary.counts ?? {};
  const out = [];
  out.push('## CI lane report');
  out.push('');
  out.push(
    `${counts.fail ?? failed.length} failed, ${counts.pass ?? 0} passed, ` +
      `${counts.skip ?? 0} skipped (an earlier lane failed), ` +
      `${counts.filtered ?? 0} filtered (not applicable to this run).`,
  );
  out.push('');
  out.push('| | lane | status | time | detail |');
  out.push('| --- | --- | --- | --- | --- |');
  for (const row of rows) {
    out.push(
      `| ${ICON[row.status] ?? ''} | \`${cell(row.lane)}\` | ${cell(row.status)} | ` +
        `${cell(seconds(row.durationMs))} | ${cell(row.summary ?? '')} |`,
    );
  }

  for (const row of failed) {
    const report = reports.get(row.lane);
    out.push('');
    out.push(`### Failed lane: \`${row.lane}\` — ${row.title ?? ''}`.trimEnd());
    out.push('');
    out.push(`What failed: **${row.summary ?? 'unknown'}**`);
    out.push('');
    if (report?.command) {
      out.push(`Reproduce it with (exit ${report.exitCode ?? '?'}):`);
      out.push(fence(report.command, 'sh'));
      out.push('');
    }
    const notable = (report?.notable ?? []).slice(0, MAX_NOTABLE_RENDERED);
    if (notable.length > 0) {
      const dropped =
        (report.notable.length - notable.length) + (report.notableDropped ?? 0);
      out.push(`Lines the runner kept${dropped > 0 ? ` (${dropped} more matched)` : ''}:`);
      out.push(fence(notable.join('\n')));
      out.push('');
    }
    if (report?.tail) {
      const size = report.tailTruncated
        ? `the last ${MAX_TAIL_CHARS} characters of ${report.outputBytes} bytes of output`
        : 'the lane’s full output';
      out.push(`For context, ${size}:`);
      out.push(fence(clip(report.tail, MAX_TAIL_CHARS)));
    }
  }

  const block = out.join('\n');
  if (block.length <= maxChars) return block;
  // The table and the "what failed" lines are at the top by construction, so
  // cutting from the end drops context before it drops a conclusion.
  return `${block.slice(0, maxChars)}\n…(diagnosis truncated at ${maxChars} characters)…`;
}

if (isDirectRun(import.meta.url)) {
  const args = process.argv.slice(2);
  let dir = '.ci-reports';
  let maxChars = MAX_BLOCK_CHARS;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--dir') dir = args[++i];
    else if (args[i].startsWith('--dir=')) dir = args[i].slice('--dir='.length);
    else if (args[i] === '--max-chars') maxChars = Number(args[++i]);
    else {
      console.error(`${PREFIX} unknown option ${args[i]}`);
      console.error(`${PREFIX} usage: summarize-ci.mjs [--dir <path>] [--max-chars <n>]`);
      process.exitCode = 2;
      dir = null;
      break;
    }
  }
  if (dir !== null) {
    const resolved = path.resolve(
      path.dirname(fileURLToPath(import.meta.url)),
      '..',
      '..',
      dir,
    );
    const block = renderDiagnosis(loadReports(resolved), { maxChars });
    if (block) {
      process.stdout.write(`${block}\n`);
    } else {
      // STDERR, so the caller's `$(…)` capture stays empty and its fallback
      // fires. A note on stdout would be indistinguishable from a diagnosis.
      console.error(`${PREFIX} no failing lane in ${resolved}; nothing to render.`);
      process.exitCode = NOTHING_TO_RENDER;
    }
  }
}
