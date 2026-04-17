#!/usr/bin/env node
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

'use strict';

// parse-load.js — parses k6 load test output and summary JSON, computes
// metric diffs, and generates a markdown comparison table.
//
// Input (env vars):
//   CURR_LOAD_RESULT    JSON-encoded string of k6 console output
//   CURR_LOAD_SUMMARY   JSON-encoded k6 summary object
//   PREV_LOAD_SUMMARY   JSON-encoded previous k6 summary object
//
// Output (stdout):
//   JSON: { result, table }
//     result  filtered k6 output (null if no input)
//     table   markdown comparison table (null if no prev/curr summaries)

// Regexes for filtering k6 progress noise from console output
const NOISE_RES = [
  /^\s*script:\s*(.+)$/m,
  /^\s*output:\s*(.+)$/m,
  /^\s*execution:\s*(.+)$/m,
  /running \(.*\), \d+\/\d+ VUs, \d+ complete and \d+ interrupted iterations/,
  /\[\s*(\d+)%\s*\]\s*\d+(\/\d+)? VUs/,
  /^(?:\|| |\n|\/|\u203e|\(|\)|_|\\|Grafana)+$/,
];

// Target metrics to extract from the k6 summary JSON
// Format: [dotPath, displayLabel, isCount]
const TARGET_METRICS = [
  ['transaction_time.avg', 'Transaction Time (avg)', false],
  ['http_req_duration.avg', 'HTTP Req Duration (avg)', false],
  ['http_req_duration.p(95)', 'HTTP Req Duration (p95)', false],
  ['iteration_duration.avg', 'Iteration Duration (avg)', false],
  ['data_received.count', 'Data Received', true],
  ['data_sent.count', 'Data Sent', true],
];

// Filter noise lines from k6 output
function filterOutput(text) {
  const lines = text.split(/\r?\n/);
  const kept = lines.filter((line) => !NOISE_RES.some((re) => re.test(line)));
  // Trim leading/trailing blank lines
  let start = 0;
  let end = kept.length - 1;
  while (start <= end && kept[start].trim() === '') start++;
  while (end >= start && kept[end].trim() === '') end--;
  return kept.slice(start, end + 1).join('\n');
}

// Resolve a dot-path like "http_req_duration.p(95)" from the k6 metrics object.
// k6 summary structure: { metrics: { <name>: { values: { avg, "p(95)", ... } } } }
function resolveMetric(summary, path) {
  if (!summary || !summary.metrics) return null;
  const dot = path.indexOf('.');
  if (dot === -1) return null;

  const metricName = path.slice(0, dot);
  const valuePath = path.slice(dot + 1);

  const metric = summary.metrics[metricName];
  if (!metric) return null;
  const val = metric[valuePath];
  return val !== undefined ? val : null;
}

// Format a numeric value: integers with locale formatting, floats with 2 dp
function formatNum(value, isCount) {
  if (value === null || value === undefined) return '–';
  if (isCount) {
    // Byte counts — show as locale integer
    return Number(value).toLocaleString('en-US');
  }
  // Time values (ms) — float with 2 dp
  return Number(value).toFixed(2);
}

// Compute percentage change: (curr - prev) / prev * 100
function pctChange(prev, curr) {
  if (prev === 0 || prev === null || curr === null) return null;
  return ((curr - prev) / prev) * 100;
}

// Change indicator emoji.
// For load metrics: lower is generally better (time, bytes transferred).
// Negative pct (curr < prev) = improvement = 🟢
// Positive pct (curr > prev) = regression = 🔴
function indicator(pct) {
  if (pct === null) return '⚪';
  if (Math.abs(pct) < 0.5) return '⚪';
  return pct < 0 ? '🟢' : '🔴';
}

// Build the markdown comparison table
function buildTable(prevSummary, currSummary) {
  const header = ['Metric', 'Previous', 'Current', 'Change'];
  const sep = header.map(() => '---');
  const lines = [`| ${header.join(' | ')} |`, `| ${sep.join(' | ')} |`];

  for (const [path, label, isCount] of TARGET_METRICS) {
    const prevVal = resolveMetric(prevSummary, path);
    const currVal = resolveMetric(currSummary, path);

    const prevStr = formatNum(prevVal, isCount);
    const currStr = formatNum(currVal, isCount);

    const pct = pctChange(prevVal, currVal);
    let changeStr;
    if (pct === null) {
      changeStr = '⚪ –';
    } else {
      const icon = indicator(pct);
      const sign = pct > 0 ? '+' : '';
      changeStr = `${icon} ${sign}${pct.toFixed(1)}%`;
    }

    lines.push(`| ${label} | ${prevStr} | ${currStr} | ${changeStr} |`);
  }

  return lines.join('\n');
}

function main() {
  const currResultEnv = process.env.CURR_LOAD_RESULT;
  const currSummaryEnv = process.env.CURR_LOAD_SUMMARY;
  const prevSummaryEnv = process.env.PREV_LOAD_SUMMARY;

  // Filter k6 output
  let result = null;
  if (currResultEnv) {
    let text;
    try {
      text = JSON.parse(currResultEnv);
    } catch (e) {
      process.stderr.write('Failed to parse CURR_LOAD_RESULT JSON: ' + e.message + '\n');
      process.exit(1);
    }
    if (text && text !== 'null') {
      result = filterOutput(text);
    }
  }

  // Build comparison table if both summaries are available
  let table = null;
  if (currSummaryEnv && prevSummaryEnv) {
    let currSummary, prevSummary;
    try {
      currSummary = JSON.parse(currSummaryEnv);
      prevSummary = JSON.parse(prevSummaryEnv);
    } catch (e) {
      process.stderr.write('Failed to parse summary JSON: ' + e.message + '\n');
      process.exit(1);
    }

    if (currSummary && currSummary !== 'null' && prevSummary && prevSummary !== 'null') {
      table = buildTable(prevSummary, currSummary);
    }
  }

  process.stdout.write(JSON.stringify({ result, table }));
}

main();
