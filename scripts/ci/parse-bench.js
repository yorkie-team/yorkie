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

// parse-bench.js — parses Go benchmark output and computes percentage changes.
//
// Input (env vars):
//   PREV_BENCH_RESULT  JSON-encoded string of previous Go benchmark output
//   CURR_BENCH_RESULT  JSON-encoded string of current Go benchmark output
//
// Output (stdout):
//   JSON: { groupedTable, significantChangesTable, hasVersionVector, grouped }

const BENCH_LINE_RE =
  /^(?<name>Benchmark[\w/()$%^&*\-=|,[\]{}\"#]*?)(?<procs>-\d+)?\s+(?<times>\d+)\s+(?<remainder>.+)$/;

// Unit classification
const TIME_UNITS = new Set(['ns/op', '(ns)', '(ms)', '(s)']);
const MEMORY_UNITS = new Set(['B/op', '(bytes)']);
const ALLOC_UNITS = new Set(['allocs/op', '(allocs)']);

// Significant change thresholds
const SIG_PCT = 20; // percent
const SIG_TIME_NS = 1000; // 1 µs
const SIG_BYTES = 8;
const SIG_ALLOCS = 1;

// Convert raw value to nanoseconds for time comparison
function toNs(value, unit) {
  switch (unit) {
    case 'ns/op':
    case '(ns)':
      return value;
    case '(ms)':
      return value * 1e6;
    case '(s)':
      return value * 1e9;
    default:
      return value;
  }
}

// Format nanoseconds to human-readable string
function formatTime(ns) {
  if (ns >= 1e9) return (ns / 1e9).toFixed(3) + ' s';
  if (ns >= 1e6) return (ns / 1e6).toFixed(3) + ' ms';
  if (ns >= 1e3) return (ns / 1e3).toFixed(3) + ' µs';
  return ns.toFixed(3) + ' ns';
}

// Format bytes to human-readable string
function formatBytes(b) {
  if (b >= 1024 ** 3) return (b / 1024 ** 3).toFixed(2) + ' GB';
  if (b >= 1024 ** 2) return (b / 1024 ** 2).toFixed(2) + ' MB';
  if (b >= 1024) return (b / 1024).toFixed(2) + ' KB';
  return b.toFixed(0) + ' B';
}

// Format allocation count
function formatAllocs(n) {
  return n.toFixed(0);
}

// Format a metric value given its unit
function formatValue(value, unit) {
  if (TIME_UNITS.has(unit)) return formatTime(toNs(value, unit));
  if (MEMORY_UNITS.has(unit)) return formatBytes(value);
  if (ALLOC_UNITS.has(unit)) return formatAllocs(value);
  return value.toString();
}

// Determine unit category label for table headers
function unitLabel(unit) {
  if (TIME_UNITS.has(unit)) return 'Time';
  if (MEMORY_UNITS.has(unit)) return 'Memory';
  if (ALLOC_UNITS.has(unit)) return 'Allocs';
  return unit;
}

// Parse a single Go benchmark output string into a map:
//   { benchmarkName => { metricUnit => value } }
function parseBenchOutput(text) {
  const results = {};
  for (const line of text.split(/\r?\n/)) {
    const m = BENCH_LINE_RE.exec(line.trim());
    if (!m) continue;

    const { name, remainder } = m.groups;
    const parts = remainder.trim().split(/\s+/);
    const metrics = {};

    for (let i = 0; i + 1 < parts.length; i += 2) {
      const val = parseFloat(parts[i]);
      const unit = parts[i + 1];
      if (!isNaN(val)) {
        metrics[unit] = val;
      }
    }

    if (Object.keys(metrics).length > 0) {
      results[name] = metrics;
    }
  }
  return results;
}

// Extract group prefix from a benchmark name.
// e.g. BenchmarkDocument/constructor => BenchmarkDocument
function groupPrefix(name) {
  const slash = name.indexOf('/');
  return slash !== -1 ? name.slice(0, slash) : name;
}

// Compute percentage change: (curr - prev) / prev * 100
function pctChange(prev, curr) {
  if (prev === 0) return null;
  return ((curr - prev) / prev) * 100;
}

// Indicator emoji
function indicator(pct) {
  if (pct === null) return '⚪';
  // For time and memory: negative pct = improvement (green)
  // For allocs: negative pct = improvement (green)
  if (Math.abs(pct) < 0.5) return '⚪';
  return pct < 0 ? '🟢' : '🔴';
}

// Determine if a change is significant
function isSignificant(pct, absChange, unit) {
  if (pct === null || Math.abs(pct) < SIG_PCT) return false;
  if (TIME_UNITS.has(unit)) return absChange >= SIG_TIME_NS;
  if (MEMORY_UNITS.has(unit)) return absChange >= SIG_BYTES;
  if (ALLOC_UNITS.has(unit)) return absChange >= SIG_ALLOCS;
  return true;
}

// Build comparison rows for a set of benchmark names.
// Returns { rows: [{ name, metrics: [{ unit, prevVal, currVal, pct, absChange, significant }] }],
//           sortedUnits: string[] }
function buildRows(names, prevData, currData) {
  // Collect all units seen across these benchmarks
  const allUnits = new Set();
  for (const name of names) {
    for (const unit of Object.keys(currData[name] || {})) allUnits.add(unit);
    for (const unit of Object.keys(prevData[name] || {})) allUnits.add(unit);
  }

  // Sort units: time first, then memory, then allocs, then others
  const unitOrder = (u) => {
    if (TIME_UNITS.has(u)) return 0;
    if (MEMORY_UNITS.has(u)) return 1;
    if (ALLOC_UNITS.has(u)) return 2;
    return 3;
  };
  const sortedUnits = [...allUnits].sort((a, b) => unitOrder(a) - unitOrder(b));

  const rows = [];
  for (const name of names) {
    const prev = prevData[name] || {};
    const curr = currData[name] || {};
    const metrics = [];

    for (const unit of sortedUnits) {
      const prevVal = prev[unit] ?? null;
      const currVal = curr[unit] ?? null;

      let pct = null;
      let absChange = null;

      if (prevVal !== null && currVal !== null) {
        pct = pctChange(
          TIME_UNITS.has(unit) ? toNs(prevVal, unit) : prevVal,
          TIME_UNITS.has(unit) ? toNs(currVal, unit) : currVal,
        );
        absChange = Math.abs(
          (TIME_UNITS.has(unit) ? toNs(currVal, unit) : currVal) -
            (TIME_UNITS.has(unit) ? toNs(prevVal, unit) : prevVal),
        );
      }

      metrics.push({
        unit,
        prevVal,
        currVal,
        pct,
        absChange,
        significant: isSignificant(pct, absChange, unit),
      });
    }

    rows.push({ name, metrics });
  }

  return { rows, sortedUnits };
}

// Render a short display name: strip the group prefix
function shortName(name) {
  const slash = name.indexOf('/');
  return slash !== -1 ? name.slice(slash + 1) : name;
}

// Build markdown table for a group of benchmarks
function buildGroupTable(groupName, names, prevData, currData) {
  const { rows, sortedUnits } = buildRows(names, prevData, currData);
  if (rows.length === 0) return '';

  // Build header columns
  const metricCols = sortedUnits.map((u) => unitLabel(u));
  const header = ['Benchmark', ...metricCols.flatMap((l) => [`Prev ${l}`, `Curr ${l}`, 'Change'])];
  const sep = header.map(() => '---');

  const lines = [`| ${header.join(' | ')} |`, `| ${sep.join(' | ')} |`];

  for (const { name, metrics } of rows) {
    const cells = [shortName(name)];
    for (const { unit, prevVal, currVal, pct, significant } of metrics) {
      const prevStr = prevVal !== null ? formatValue(prevVal, unit) : 'N/A';
      const currStr = currVal !== null ? formatValue(currVal, unit) : 'N/A';
      let changeStr = '⚪ N/A';
      if (pct !== null) {
        const icon = indicator(pct);
        const sign = pct > 0 ? '+' : '';
        changeStr = `${icon} ${sign}${pct.toFixed(1)}%`;
      }
      cells.push(prevStr, currStr, changeStr);
    }
    lines.push(`| ${cells.join(' | ')} |`);
  }

  return lines.join('\n');
}

// Build significant changes table across all groups.
// Returns { table, hasVersionVector }.
function buildSignificantTable(grouped, prevData, currData) {
  const sigRows = [];
  let hasVV = false;

  for (const [groupName, names] of Object.entries(grouped)) {
    const { rows } = buildRows(names, prevData, currData);
    for (const { name, metrics } of rows) {
      for (const { unit, prevVal, currVal, pct, significant } of metrics) {
        if (!significant) continue;
        sigRows.push({ name, unit, prevVal, currVal, pct });
        if (name.includes('VersionVector')) hasVV = true;
      }
    }
  }

  if (sigRows.length === 0) return { table: '', hasVersionVector: hasVV };

  const header = ['Benchmark', 'Metric', 'Prev', 'Curr', 'Change'];
  const sep = header.map(() => '---');
  const lines = [`| ${header.join(' | ')} |`, `| ${sep.join(' | ')} |`];

  for (const { name, unit, prevVal, currVal, pct } of sigRows) {
    const prevStr = prevVal !== null ? formatValue(prevVal, unit) : 'N/A';
    const currStr = currVal !== null ? formatValue(currVal, unit) : 'N/A';
    const icon = indicator(pct);
    const sign = pct > 0 ? '+' : '';
    const changeStr = `${icon} ${sign}${pct.toFixed(1)}%`;
    lines.push(`| ${name} | ${unitLabel(unit)} | ${prevStr} | ${currStr} | ${changeStr} |`);
  }

  return { table: lines.join('\n'), hasVersionVector: hasVV };
}

const EMPTY_RESULT = JSON.stringify({
  groupedTable: '',
  significantChangesTable: '',
  hasVersionVector: false,
  grouped: {},
});

function main() {
  const prevEnv = process.env.PREV_BENCH_RESULT;
  const currEnv = process.env.CURR_BENCH_RESULT;

  // If either is missing or null, output empty result and exit
  if (!prevEnv || !currEnv) {
    process.stdout.write(EMPTY_RESULT);
    process.exit(0);
  }

  let prevText, currText;
  try {
    prevText = JSON.parse(prevEnv);
    currText = JSON.parse(currEnv);
  } catch (e) {
    process.stderr.write('Failed to parse input JSON: ' + e.message + '\n');
    process.exit(1);
  }

  if (!prevText || prevText === 'null' || !currText || currText === 'null') {
    process.stdout.write(EMPTY_RESULT);
    process.exit(0);
  }

  const prevData = parseBenchOutput(prevText);
  const currData = parseBenchOutput(currText);

  // Group benchmark names by prefix
  const grouped = {};
  const allNames = new Set([...Object.keys(prevData), ...Object.keys(currData)]);

  for (const name of allNames) {
    const prefix = groupPrefix(name);
    if (!grouped[prefix]) grouped[prefix] = [];
    if (!grouped[prefix].includes(name)) grouped[prefix].push(name);
  }

  // Sort names within each group
  for (const names of Object.values(grouped)) {
    names.sort();
  }

  // Build grouped table with <details> sections
  const groupSections = [];
  for (const [groupName, names] of Object.entries(grouped)) {
    const table = buildGroupTable(groupName, names, prevData, currData);
    if (!table) continue;
    groupSections.push(
      `<details>\n<summary>${groupName}</summary>\n\n${table}\n\n</details>`,
    );
  }
  const groupedTable = groupSections.join('\n\n');

  // Build significant changes table and check for VersionVector
  const { table: significantChangesTable, hasVersionVector } =
    buildSignificantTable(grouped, prevData, currData);

  process.stdout.write(
    JSON.stringify({
      groupedTable,
      significantChangesTable,
      hasVersionVector,
      grouped,
    }),
  );
}

main();
