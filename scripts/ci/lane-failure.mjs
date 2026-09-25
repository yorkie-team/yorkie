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

// Turn a failed lane's output into ONE line naming what failed.
//
// WHY THIS IS A MODULE OF ITS OWN, and not a function inside run-lanes.mjs:
// the runner spawns processes, and the only thing that can be tested cheaply
// and exhaustively here is the parsing. A suite that wants to assert "this
// `go test` output names TestTreeEdit" must be able to import the parser
// without importing anything that could run a build. Nothing in this file
// touches the filesystem, the network or a child process.
//
// WHY IT IS NOT A GREP FOR "error". That is what a log tail already gives the
// fixing agent, and it is wrong in both directions on this repository's
// output: `go test -v` prints the word in passing tests' own logging, while
// the line that actually names the failure — `--- FAIL: TestX/sub` — does not
// contain it at all. A race report contains neither. So the parsing is per
// KIND of output, and each kind has an explicit precedence order, because a
// single run can carry several failure shapes at once and only one of them is
// the root cause:
//
//   a package that did not COMPILE cannot have a failing test, so the compile
//   error wins; a DATA RACE is what the `-race` lane exists to find, so it
//   wins over the `--- FAIL:` it causes; a panic aborts the binary, so the
//   tests listed after it are collateral.
//
// WHAT "ONE LINE" IS FOR. The line lands in a table row, in a step summary,
// and inside the fixer's prompt. It has to survive being read without the
// surrounding output, so it names the test, the package and the kind of
// failure and nothing else; the evidence for it travels separately, as the
// notable lines the runner retained.

/** Every output kind a lane may declare. `generic` is the fail-safe. */
export const LANE_KINDS = Object.freeze([
  'go-test',
  'go-build',
  'golangci',
  'buf',
  'license',
  'generic',
]);

/**
 * The longest a summary line may be. Long enough for a package path plus a
 * test name — the two together are routinely 90 characters here — and short
 * enough that a table row holding eight of them stays readable.
 */
export const MAX_SUMMARY_CHARS = 200;

/**
 * Lines worth keeping out of an arbitrarily large stream, per kind.
 *
 * THE RUNNER APPLIES THESE WHILE IT STREAMS, which is the whole reason they
 * live here rather than in a regex the runner owns. `go test -race -v ./...`
 * can emit tens of megabytes: keeping a tail alone loses a race report that
 * happened early, and keeping everything is not an option. Retaining the
 * matched lines as they go past is what makes the summary independent of
 * WHERE in the stream the failure occurred.
 *
 * FAILURE-SHAPED ONLY, and that is a correction rather than a preference.
 * This list once matched `ok  <pkg>` and every `<file>_test.go:NN:` line.
 * Against a real `-v ./...` run those are the overwhelming majority of the
 * output — the budget filled with a hundred passing packages long before the
 * stream reached the failing one, the `--- FAIL:` never made it in, and the
 * summary came out as the word `FAIL`. A pattern that matches a PASSING run's
 * output does not select evidence, it evicts it.
 */
const NOTABLE = {
  common: [
    /^::(error|warning)::/,
    /^make(\[\d+\])?:\s.*\bError\b/,
    /command not found/,
    /^bash:\s/,
  ],
  'go-test': [
    /^\s*--- FAIL: /,
    /^FAIL\b/,
    /^\s*panic: /,
    /^\s*fatal error: /,
    /WARNING: DATA RACE/,
    /^\S+\.go:\d+:\d+: /,
    /^#\s\S+/,
    /\[build failed\]/,
  ],
  'go-build': [/^\S+\.go:\d+:(\d+:)? /, /^#\s\S+/, /^\s*vet: /],
  golangci: [/^\S+\.go:\d+:\d+: /, /^level=(error|fatal)/, /^\s*ERRO\s/],
  buf: [/^\S+\.proto:\d+:\d+:/, /^Failure: /],
  license: [/^\[verify:license\]/],
  generic: [/^\s*Error:\s/],
};

/**
 * Lines that are evidence only when something near them failed.
 *
 * `    tree_test.go:214: expected <p>ab</p>, got …` is the most useful line in
 * a Go failure and the most common line in a passing verbose run. It is kept
 * by LOOKBEHIND instead of by pattern: the runner holds the last few of these
 * and flushes them only when a failure-shaped line arrives, which is exactly
 * where `go test` prints them — immediately before `--- FAIL:`.
 */
const CONTEXT = {
  'go-test': [
    /^\s+\S+_test\.go:\d+: /,
    /^\s+Error Trace:/,
    /^\s+Error:\s/,
    /^\s+Test:\s/,
    /^\s+Messages:\s/,
  ],
  'go-build': [],
  golangci: [],
  buf: [],
  license: [],
  generic: [],
};

/**
 * How many notable lines a report keeps. The FIRST ones, deliberately: the
 * first compile error explains the twenty after it, and the first failing
 * test in a `-race` run is usually the one that raced. A tail of notable
 * lines would keep the collateral and drop the cause.
 */
export const MAX_NOTABLE_LINES = 60;

/**
 * How many context lines are held for lookbehind, and how many of them a
 * single failure may flush. Small: `go test` prints an assertion's output
 * directly above its `--- FAIL:`, so anything older belongs to a test that
 * passed.
 */
export const CONTEXT_LOOKBEHIND = 8;

/** True iff `line` is failure-shaped for a lane of this `kind`. */
export function isNotableLine(kind, line) {
  if (!line) return false;
  const patterns = NOTABLE[kind] ?? NOTABLE.generic;
  for (const re of NOTABLE.common) if (re.test(line)) return true;
  for (const re of patterns) if (re.test(line)) return true;
  return false;
}

/** True iff `line` is worth keeping only when a failure follows it. */
export function isContextLine(kind, line) {
  if (!line) return false;
  if (isNotableLine(kind, line)) return false;
  for (const re of CONTEXT[kind] ?? CONTEXT.generic) if (re.test(line)) return true;
  return false;
}

/**
 * Lines a runner prints about its own progress, which are NEITHER evidence nor
 * the end of it.
 *
 * The distinction exists because of one shape. `go test` prints an assertion's
 * output, then — when the failing subtest is not the last under its parent —
 * `=== RUN Parent/next_case`, and only then the `--- FAIL:` that makes the
 * assertion worth keeping. Treating that `=== RUN` as "something else" clears
 * the lookbehind and throws away the only line naming what went wrong. It is
 * not context either: holding it would push real context out of a small
 * window.
 *
 * `--- PASS` and `--- SKIP` are deliberately NOT here. They mean the output
 * above them belonged to a test that did not fail, so clearing the window is
 * the right answer — that is the precision `CONTEXT_LOOKBEHIND` is documented
 * for, and a suite case pins it. Only the announcements are neutral.
 */
const NEUTRAL = [/^\s*=== (RUN|PAUSE|CONT|NAME)\b/];

/** True iff `line` should leave the lookbehind exactly as it is. */
export function isNeutralLine(kind, line) {
  if (!line) return false;
  if (isNotableLine(kind, line)) return false;
  return NEUTRAL.some((re) => re.test(line));
}

/** Collapse to a single line and bound it. */
function oneLine(text) {
  const flat = String(text ?? '')
    .replace(/\s+/g, ' ')
    .trim();
  if (flat.length <= MAX_SUMMARY_CHARS) return flat;
  return `${flat.slice(0, MAX_SUMMARY_CHARS - 1)}…`;
}

/**
 * Every line of the evidence, notable lines first, then the retained tail.
 *
 * DEDUPED, and it has to be. The notable lines are a SUBSET of the stream and
 * the tail is the END of that same stream, so on any lane whose output fits
 * in the tail every notable line appears twice. Left as is, "3 issues" is
 * reported as six, and a count a reader cannot trust is worse than no count.
 */
function evidenceLines({ notable = [], tail = '' }) {
  const strip = (l) => l.replace(/\r$/, '');
  const kept = (notable ?? []).map(strip);
  const seen = new Set(kept);
  const lines = [...kept];
  for (const line of String(tail ?? '').split('\n').map(strip)) {
    if (seen.has(line)) continue;
    // Only lines that could be counted are deduped against; blank lines and
    // ordinary output repeat legitimately and carry no count.
    if (line.trim()) seen.add(line);
    lines.push(line);
  }
  return lines;
}

/** The last line that carries anything at all. */
function lastMeaningful(lines) {
  for (let i = lines.length - 1; i >= 0; i--) {
    const t = lines[i].trim();
    // A bare rule or a progress bar is not a diagnosis.
    if (t && !/^[-=_*\s]+$/.test(t)) return t;
  }
  return '';
}

/**
 * The failing package, as `go test` reports it on its own summary line.
 *
 * `FAIL\tgithub.com/...\t0.4s` and `FAIL\tgithub.com/... [build failed]` are
 * both matched; `ok` lines are not, so a run with one failing package among
 * fifty passing ones still names the right one.
 */
function failingPackage(lines) {
  for (const line of lines) {
    const m = /^FAIL\s+(\S+)/.exec(line);
    if (m && m[1] !== '') return m[1];
  }
  return null;
}

/**
 * The most specific failing test.
 *
 * THE ORDER IS NOT WHAT IT LOOKS LIKE. `go test -v` prints the PARENT's
 * `--- FAIL:` first and indents its failing subtests underneath:
 *
 *     --- FAIL: TestTreeEdit (0.02s)
 *         --- FAIL: TestTreeEdit/split_at_a_boundary (0.01s)
 *
 * so taking the first line in stream order names `TestTreeEdit` — true, but
 * one level too coarse to hand to `go test -run`. Taking the last is wrong
 * too: a run with several failing tests ends on whichever failed last.
 *
 * The rule that holds either way is DESCENDANT OF THE FIRST: the first
 * `--- FAIL:` establishes the root, and the deepest later name below it is
 * the specific one. A second unrelated failing test is not a descendant and
 * cannot displace it.
 */
function failingTest(lines) {
  let root = null;
  let best = null;
  for (const line of lines) {
    const m = /^\s*--- FAIL:\s+(\S+)/.exec(line);
    if (!m) continue;
    const name = m[1];
    if (root === null) {
      root = name;
      best = name;
      continue;
    }
    if (name.startsWith(`${root}/`) && name.length > best.length) best = name;
  }
  return best;
}

/** `# pkg` followed by `file.go:1:2: message` — the shape of a compile error. */
function compileError(lines) {
  let pkg = null;
  for (const line of lines) {
    const head = /^#\s+(\S+)/.exec(line);
    if (head) {
      pkg = head[1];
      continue;
    }
    const m = /^(\S+\.go):(\d+):(?:(\d+):)?\s*(.+)$/.exec(line);
    if (m) return { pkg, file: m[1], line: m[2], message: m[4].trim() };
  }
  return null;
}

function summarizeGoTest(lines) {
  const pkg = failingPackage(lines);
  const where = pkg ? ` in ${pkg}` : '';

  // 1. It did not build. Everything else in the output is downstream of this.
  const built = lines.some((l) => /\[build failed\]/.test(l));
  const compile = compileError(lines);
  if (built || (compile && !failingTest(lines))) {
    if (compile) {
      return oneLine(
        `${compile.pkg ?? pkg ?? 'a package'} failed to build: ${compile.file}:${compile.line}: ${compile.message}`,
      );
    }
    return oneLine(`${pkg ?? 'a package'} failed to build`);
  }

  const test = failingTest(lines);

  // 2. A data race is what the `-race` lane exists to find, and it is the
  //    cause of the `--- FAIL:` below it rather than a second finding.
  if (lines.some((l) => /WARNING: DATA RACE/.test(l))) {
    return oneLine(`data race detected${test ? ` in ${test}` : ''}${where}`);
  }

  // 3. A panic aborts the test binary: every test after it is collateral.
  const panicAt = lines.findIndex((l) => /^\s*(panic|fatal error): /.test(l));
  if (panicAt >= 0) {
    const message = lines[panicAt].trim().replace(/^\s*/, '');
    return oneLine(`${message}${test ? ` (in ${test})` : ''}${where}`);
  }

  // 4. The ordinary case: a test asserted and lost.
  if (test) {
    const detail = lines.find((l) => /^\s*\S+_test\.go:\d+: /.test(l));
    const because = detail ? ` — ${detail.trim()}` : '';
    return oneLine(`${test} failed${where}${because}`);
  }

  if (pkg) return oneLine(`${pkg} failed`);
  return null;
}

function summarizeCompiler(lines) {
  const compile = compileError(lines);
  if (compile) {
    return oneLine(
      `${compile.file}:${compile.line}: ${compile.message}${compile.pkg ? ` (${compile.pkg})` : ''}`,
    );
  }
  const vet = lines.find((l) => /^\s*vet: /.test(l));
  return vet ? oneLine(vet) : null;
}

function summarizeGolangci(lines) {
  const issues = lines.filter((l) => /^\S+\.go:\d+:\d+: /.test(l));
  if (issues.length === 0) {
    const level = lines.find((l) => /^level=(error|fatal)/.test(l));
    return level ? oneLine(level) : null;
  }
  const more = issues.length > 1 ? ` (+${issues.length - 1} more)` : '';
  return oneLine(`${issues[0].trim()}${more}`);
}

function summarizeBuf(lines) {
  const issues = lines.filter((l) => /^\S+\.proto:\d+:\d+:/.test(l));
  if (issues.length === 0) return null;
  const more = issues.length > 1 ? ` (+${issues.length - 1} more)` : '';
  return oneLine(`${issues[0].trim()}${more}`);
}

/**
 * The three finding shapes `verify-license.mjs` prints, matched by shape
 * rather than by its "   " indentation.
 *
 * Its trailing count line — `2 file(s) missing the header.` — also carries
 * the word "header", so a looser pattern counts the tally as a finding and
 * reports one more missing file than there are.
 */
const LICENSE_FINDING =
  /^\[verify:license\]\s+(\S+) (?:has no Apache 2\.0 header|could not be read|could not be listed)/;

function summarizeLicense(lines) {
  const missing = lines.filter((l) => LICENSE_FINDING.test(l));
  if (missing.length === 0) return null;
  const first = missing[0].replace(/^\[verify:license\]\s*/, '').trim();
  const more = missing.length > 1 ? ` (+${missing.length - 1} more)` : '';
  return oneLine(`${first}${more}`);
}

/**
 * ONE line naming what failed.
 *
 * Returns a non-empty string always: a lane that failed with output this
 * cannot parse still has to say something, and "exited 2 with no output" is a
 * true and useful statement. An empty string here would surface as a blank
 * table cell, which reads as "nothing failed".
 *
 * @param {{kind?: string, notable?: string[], tail?: string, exitCode?: number|null, signal?: string|null}} input
 */
export function summarizeFailure(input = {}) {
  const kind = LANE_KINDS.includes(input.kind) ? input.kind : 'generic';
  const lines = evidenceLines(input);
  const exitCode = input.exitCode ?? null;

  let summary = null;
  if (kind === 'go-test') summary = summarizeGoTest(lines);
  else if (kind === 'go-build') summary = summarizeCompiler(lines);
  else if (kind === 'golangci') summary = summarizeGolangci(lines);
  else if (kind === 'buf') summary = summarizeBuf(lines);
  else if (kind === 'license') summary = summarizeLicense(lines);

  // A `go-build`-shaped or `go-test`-shaped lane whose failure was something
  // else entirely — a missing tool, a `make` error — falls through to the
  // generic reading rather than reporting nothing.
  if (!summary) {
    const annotated = lines.find((l) => /^::error::/.test(l));
    if (annotated) summary = oneLine(annotated.replace(/^::error::(\S*::)?/, ''));
  }
  // THE LAST NOTABLE LINE FIRST, and only then the last line of any kind.
  //
  // `evidenceLines` emits the notable lines ahead of the tail and drops tail
  // lines already among them, so when the genuinely last line of output IS
  // notable — `make: *** [Makefile:40: build] Error 2` is exactly that — it
  // has been moved out of last position, and `lastMeaningful` returns whatever
  // preceded it. On a build lane that is the echoed command, so the summary
  // read `go build ./...`: the thing that ran, not the thing that failed.
  if (!summary) {
    const notables = lines.filter((l) => isNotableLine(kind, l));
    const lastNotable = notables.length ? notables[notables.length - 1] : '';
    if (lastNotable) summary = oneLine(lastNotable);
  }
  if (!summary) {
    const last = lastMeaningful(lines);
    if (last) summary = oneLine(last);
  }
  if (!summary) {
    if (input.signal) return `killed by ${input.signal}, with no output`;
    return `exited ${exitCode ?? '?'} with no output`;
  }

  // The exit code is NOT appended to a parsed summary. It is the weakest fact
  // in the report, it is already a field of its own, and stapling it to a line
  // that names a test and a package only crowds out the strong facts.
  return summary;
}
