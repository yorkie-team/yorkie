// Which tests did a fix round take away?
//
// WHY THIS EXISTS. On #1426 (e6900da) the fixer wrote a two-replica test for a
// finding, watched it fail, DELETED it, and reported the finding "Fixed" with a
// caveat in prose. The next round's adjudicator was told "a claim of having
// fixed something is not evidence that it was fixed" — and was given nothing
// else to go on. Weakened tests were left entirely to the test-adequacy lens
// (checks.mjs says as much), which reads the cumulative diff and cannot see a
// test that was written and removed between two of its rounds.
//
// This is the mechanical half. The trusted `fix-report` job reads the round's
// commits through the API — never a checkout the agent touched — and when a
// test file was deleted, renamed out of the runner's reach, lost active cases,
// or had a suite switched off, posts a `<!-- agent-fix-tests -->` record as
// `github-actions[bot]`. The next round puts it in front of the adjudicator,
// before the author's fence, for every claim and dispute it adjudicates
// (fix-report.mjs, rebuttal.mjs). It decides nothing itself: a removed test can
// be a legitimate cleanup, so it is EVIDENCE for a component that already
// re-reads the code, not a gate.
//
// PER COMMIT, NOT ONE COMPARE. A three-dot compare diffs from the merge base:
// a merge of main would be blamed on the fixer, a test committed and deleted
// inside the round would not show at all, and only 300 files are listed. So the
// round's OWN commits are taken (`roundCommits`: in the compare, in the PR's
// commit list, not merges) and each commit's files are summed per path.
//
// WHAT IT CANNOT SEE, stated because #1426 is the case that motivated it and is
// one it would NOT have caught: there the test was written and deleted in the
// working tree and never committed, so no commit ever held it. Only COMMITTED
// tests are visible. The fixer prompt's rule — keep a reproducing test behind
// a `t.Skip` that names the finding, never delete it — is the only guard for
// the uncommitted case. A
// force-push that dropped commits is flagged (`rewritten`) rather than hidden:
// the dropped commits are gone from the API too.
//
// TWO LANGUAGES. yorkie's product tests are Go — `_test.go` files, top-level
// `func TestXxx(t *testing.T)` / `func FuzzXxx(f *testing.F)` cases, `t.Run`
// subtests, and `t.Skip`/`t.Skipf`/`t.SkipNow` as the way a case is switched
// off — and the fixer edits Go. The harness's own tests are JavaScript
// (`scripts/agent/*.test.mjs`, `it(`/`test(`). `countCases` picks the rules by
// the file's extension. Go has no `it.fails`: a reproducer the fixer could not
// fix is kept behind a `t.Skip`, so on the Go side a skip IS recorded — beside
// a `--skipped` claim that is the honest record, beside a `--fixed` one it is
// the contradiction the adjudicator is shown it to catch.
//
// Usage:
//   node test-removals.mjs post <pr> --before <sha> --after <sha> [--head <sha>]
// `--head` is the sha the fix report names (the round's reviewed head), which
// the record is joined on; it defaults to `--before`. Posts only when something
// was removed or history was rewritten. Always exits 0: an unread round is "no
// evidence", which is exactly the behaviour before this existed.

import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const TEST_REMOVALS_MARKER = "<!-- agent-fix-tests ";
export const TEST_REMOVALS_VERSION = 1;
/** The one identity whose records are believed: the trusted job's GITHUB_TOKEN. */
export const TEST_REMOVALS_AUTHOR_LOGIN = "github-actions[bot]";

const str = (v) => (typeof v === "string" ? v : "");
const int = (v) => (Number.isInteger(v) && v > 0 ? v : 0);

/**
 * A path as it may be RENDERED: control characters (newlines included — git
 * allows them in paths) become spaces, backticks become quotes. File names come
 * from the branch, and they are printed in a block labelled "the author did not
 * write this"; a newline would let a path write its own line there.
 */
export function safePath(file) {
  return str(file).replace(/[\u0000-\u001f\u007f]/g, " ").replace(/`/g, "'").slice(0, 300);
}

/** This repo's test layouts: `test/` trees, `__tests__/`, `*_test.go`, and `*.test.*` / `*.spec.*` / `*_test.*` JS files. */
export function isTestFile(file) {
  const f = str(file);
  return /(^|\/)(test|tests|__tests__)\//.test(f) || isRunnableTest(f);
}

/** A file the runners pick up by NAME (`*_test.go`; `*.test.*`, `*.spec.*`, `*_test.*` in JS), wherever it lives. */
export function isRunnableTest(file) {
  const f = str(file);
  return /_test\.go$/.test(f) || /[._](test|spec)\.[cm]?[jt]sx?$/.test(f);
}

const isGo = (file) => /\.go$/.test(str(file));

// JavaScript (the harness's own tests). An ACTIVE case declaration at the start
// of a diff line: `it`/`test` with any
// chain of the modifiers that still RUN (`only`, `each`, `concurrent`, `fails`,
// `sequential`, `for`), called or tagged (`test.each\`…\``). `.fails` runs and
// asserts the failure — how a fixer should record a reproduction it could not
// fix — so turning a case into `.fails` is not a removal. `.skip`, `.todo`,
// `.skipIf(…)` and `.runIf(…)` may not run: a case rewritten to one stops
// matching, which counts it removed.
const CASE = /^[-+]\s*(?:it|test)(?:\.(?:only|each|concurrent|fails|sequential|for))*\s*[(`]/;
// A suite that may not run. Counted apart from cases and never netted against
// added ones: one `describe.skip` silences every case under it without those
// lines changing, so "one suite off, one case added" must still be reported.
const SUITE_OFF = /^[-+]\s*describe(?:\.\w+)*\.(?:skip|todo|skipIf|runIf)\s*[(`]/;

// Go. A top-level case is a `Test`/`Fuzz` function whose name continues with
// anything but a lower-case letter (the `go test` rule) and whose parameter is
// `*testing.T`/`*testing.F` — so `TestMain(m *testing.M)` and helpers are not
// cases. gofmt puts top-level declarations at column 0. A subtest is a
// `<x>.Run(…, func(<t> *testing.T)` call. Benchmarks are left out: `go test`
// does not run them, so they cannot show that a finding reproduces. Rows of a
// table-driven test are not seen — a known gap, stated rather than guessed at.
const GO_CASE = /^[-+]func\s+(?:Test|Fuzz)(?![a-z])\w*\s*\(\s*\w+\s+\*testing\.[TF]\s*\)/;
const GO_SUBTEST = /^[-+]\s*[\w.]+\.Run\(.*func\s*\(\s*\w+\s+\*testing\.T\s*\)/;
// A case switched off in place. Unlike `it.skip`, `t.Skip` is a new line in an
// unchanged function, so it is counted apart from cases, like a disabled suite,
// and netted only against skips the same patch removes. Comment lines are not
// calls.
const GO_SKIP = /^[-+](?!\s*\/\/).*\b[tf]\.Skip(?:f|Now)?\(/;
// A build constraint. In an existing test file, a new or changed `//go:build`
// expression can take the whole file out of every lane that runs it
// (`//go:build integration` → `//go:build ignore`), the Go form of a disabled
// suite. A new file's constraint is not: it was never running.
const GO_BUILD = /^([-+])\/\/go:build\s+(.*?)\s*$/;

/** The Go half of `countCases`; see the rules above. */
function countGoCases(patch, status) {
  let removed = 0, added = 0, skipAdded = 0, skipRemoved = 0;
  const builds = { "+": new Set(), "-": new Set() };
  for (const line of str(patch).split("\n")) {
    if (line.startsWith("---") || line.startsWith("+++")) continue;
    const b = GO_BUILD.exec(line);
    if (b) {
      builds[b[1]].add(b[2]);
      continue;
    }
    if (GO_SKIP.test(line)) {
      if (line[0] === "+") skipAdded++;
      else skipRemoved++;
      continue;
    }
    if (!GO_CASE.test(line) && !GO_SUBTEST.test(line)) continue;
    if (line[0] === "-") removed++;
    else added++;
  }
  const constraintsOff = status === "added" ? 0 : [...builds["+"]].filter((e) => !builds["-"].has(e)).length;
  return { removed, added, suitesOff: Math.max(0, skipAdded - skipRemoved) + constraintsOff };
}

/**
 * Active cases a unified diff removes and adds, and how many suites it newly
 * switches off. Suite lines are netted against THEMSELVES only, so editing an
 * already-skipped suite's title, or re-enabling one, is not a disablement.
 * `file` picks the language (a `.go` file gets the Go rules, anything else the
 * JS ones); `status` is the file's GitHub status, which only the Go
 * build-constraint rule reads.
 */
export function countCases(patch, { file = "", status = "" } = {}) {
  if (isGo(file)) return countGoCases(patch, status);
  let removed = 0, added = 0, offAdded = 0, offRemoved = 0;
  for (const line of str(patch).split("\n")) {
    if (line.startsWith("---") || line.startsWith("+++")) continue;
    if (SUITE_OFF.test(line)) {
      if (line[0] === "+") offAdded++;
      else offRemoved++;
      continue;
    }
    if (!CASE.test(line)) continue;
    if (line[0] === "-") removed++;
    else added++;
  }
  return { removed, added, suitesOff: Math.max(0, offAdded - offRemoved) };
}

/** One commit's (or one compare's) files → per-path entries. Not yet filtered. */
function entries(files) {
  const out = [];
  for (const f of Array.isArray(files) ? files : []) {
    const now = str(f?.filename);
    const was = f?.status === "renamed" ? str(f?.previous_filename) : now;
    // A test RENAMED so the runner no longer picks it up (`a_test.ts` →
    // `a_test.ts.off`, or `_test` dropped from the name) no longer runs: that is
    // the old file deleted, wherever the new name lives.
    const renamedAway = f?.status === "renamed" && isRunnableTest(was) && !isRunnableTest(now);
    if (!isTestFile(now) && !renamedAway) continue;
    const deleted = f.status === "removed" || renamedAway;
    // GitHub omits `patch` for a diff too large to show. That is "unknown", not
    // "nothing removed". (A pure rename also carries no patch, but an unchanged
    // file is not a removal.)
    if (typeof f.patch !== "string" && !(f.status === "renamed" && !renamedAway)) {
      if (deleted || f.status === "modified") out.push({ file: was, deleted, removed: 0, added: 0, suitesOff: 0, unreadable: true });
      continue;
    }
    out.push({ file: was, deleted, renamedAway, ...countCases(f.patch, { file: was, status: f.status }) });
  }
  return out;
}

/** Keep only what is a removal: a deletion that held cases, a rename away, a net loss, a suite off, or unreadable. */
function removalsOf(list) {
  const out = [];
  for (const e of list) {
    const hit = e.unreadable || e.renamedAway || e.suitesOff > 0 || (e.deleted ? e.removed > 0 : e.removed > e.added);
    if (!hit) continue;
    const { renamedAway: _r, ...rest } = e;
    out.push(rest);
  }
  return out;
}

/**
 * Test files a single file list shows deleted, renamed away, losing active
 * cases, or with a suite switched off. `files` is a commit's or a compare's
 * `files` array (`filename`, `status`, `patch`, `previous_filename`).
 */
export function testRemovals(files) {
  return removalsOf(entries(files));
}

/**
 * The same, summed per path across a round's commits. Merge commits are
 * skipped: they bring in main, not the fixer's work. A test added in one commit
 * and deleted in a later one is a deletion that held cases — exactly what a
 * three-dot compare cannot see.
 */
export function aggregateCommits(commits) {
  const byFile = new Map();
  for (const c of Array.isArray(commits) ? commits : []) {
    if (Array.isArray(c?.parents) && c.parents.length > 1) continue;
    for (const e of entries(c?.files)) {
      const cur = byFile.get(e.file) ?? { file: e.file, deleted: false, removed: 0, added: 0, suitesOff: 0 };
      cur.removed += e.removed;
      cur.added += e.added;
      cur.suitesOff += e.suitesOff;
      cur.deleted = e.deleted || (cur.deleted && e.removed === 0 && e.added === 0);
      if (e.unreadable) cur.unreadable = true;
      if (e.renamedAway) cur.renamedAway = true;
      byFile.set(e.file, cur);
    }
  }
  return removalsOf([...byFile.values()]);
}

/**
 * The round's OWN commits: in the compare `before...after`, also in the PR's
 * commit list (which excludes everything already on the base branch), and not
 * merges. Measured on #1406: a merge of main lists main's commits in the
 * compare with ONE parent each, so "skip merges" alone would blame main's test
 * changes on the fixer. An unreadable PR list attributes nothing.
 */
export function roundCommits(compareCommits, prShas) {
  if (!(prShas instanceof Set)) return [];
  return (Array.isArray(compareCommits) ? compareCommits : [])
    .filter((c) => c && prShas.has(c.sha) && Number(c.n) === 1);
}

/** The hidden record. The terminator is escaped, as every record here does. */
export function serializeTestRemovals({ head = "", after = "", removals = [], rewritten = false } = {}) {
  const payload = {
    v: TEST_REMOVALS_VERSION,
    head: str(head).slice(0, 64),
    after: str(after).slice(0, 64),
    ...(rewritten ? { rewritten: true } : {}),
    removals: (Array.isArray(removals) ? removals : []).slice(0, 40).map((r) => ({
      file: safePath(r.file),
      deleted: r.deleted === true,
      removed: int(r.removed),
      added: int(r.added),
      suitesOff: int(r.suitesOff),
      ...(r.unreadable === true ? { unreadable: true } : {}),
    })),
  };
  return `${TEST_REMOVALS_MARKER}${JSON.stringify(payload).replace(/-->/g, "-\\u002d>")} -->`;
}

/** One removal as a line. Deletion first: a deleted file is never "merely changed". */
export function describeRemoval(r) {
  const file = `\`${safePath(r?.file)}\``;
  if (r?.deleted === true) {
    return r?.unreadable === true || !int(r?.removed)
      ? `- deleted ${file} (the whole file stopped running)`
      : `- deleted ${file} (${int(r.removed)} case(s))`;
  }
  if (r?.unreadable === true) return `- ${file}: changed, but the diff was too large to read`;
  const parts = [];
  if (int(r?.removed) || int(r?.added)) parts.push(`${int(r?.removed)} active case(s) removed or disabled, ${int(r?.added)} added`);
  if (int(r?.suitesOff)) parts.push(`${int(r.suitesOff)} test(s) or suite(s) switched off`);
  return `- ${file}: ${parts.join("; ")}`;
}

/** Visible lines plus the hidden record. Every `<!--` in the visible part is broken. */
export function renderTestRemovals(rec) {
  const list = Array.isArray(rec?.removals) ? rec.removals : [];
  const lines = [
    `🧪 **This fix round removed or disabled tests in ${list.length} file(s)** between \`${safePath(rec?.head).slice(0, 9)}\` and \`${safePath(rec?.after).slice(0, 9)}\`. ` +
      "Removing a test can be legitimate; this is passed to the next round's adjudicator as evidence.",
    ...(rec?.rewritten ? ["", "⚠️ The branch history was rewritten during the round, so commits it dropped could not be read."] : []),
    "",
    ...list.map(describeRemoval),
  ].join("\n").replace(/<!--/g, "<!-‌-");
  return `${lines}\n\n${serializeTestRemovals(rec)}`;
}

/** Every believable record on the PR, in comment order. */
export function collectTestRemovals(comments) {
  const out = [];
  for (const c of Array.isArray(comments) ? comments : []) {
    if (c?.user?.type !== "Bot" || c?.user?.login !== TEST_REMOVALS_AUTHOR_LOGIN) continue;
    const m = new RegExp(`${TEST_REMOVALS_MARKER}([\\s\\S]*?) -->`).exec(str(c.body));
    if (!m) continue;
    let d;
    try {
      d = JSON.parse(m[1]);
    } catch {
      continue;
    }
    if (!d || typeof d !== "object" || d.v !== TEST_REMOVALS_VERSION || !Array.isArray(d.removals)) continue;
    // An EMPTY record is refused unless it says history was rewritten: an empty
    // list could only ever hide a real record for the same head.
    if (d.removals.length === 0 && d.rewritten !== true) continue;
    out.push({
      head: str(d.head), after: str(d.after),
      ...(d.rewritten === true ? { rewritten: true } : {}),
      removals: d.removals.map((r) => ({ ...r, file: safePath(r?.file) })),
    });
  }
  return out;
}

function main() {
  const argv = process.argv.slice(2);
  const [verb, pr] = argv;
  const flag = (k) => {
    const i = argv.indexOf(`--${k}`);
    return i >= 0 ? str(argv[i + 1]) : "";
  };
  const before = flag("before");
  const after = flag("after");
  const head = /^[0-9a-f]{7,40}$/i.test(flag("head")) ? flag("head") : before;
  if (verb !== "post" || !/^\d+$/.test(str(pr)) || !/^[0-9a-f]{40}$/i.test(before) || !/^[0-9a-f]{40}$/i.test(after)) {
    console.error("usage: test-removals.mjs post <pr> --before <sha40> --after <sha40> [--head <sha>]");
    return;
  }
  const ghJson = (args) => JSON.parse(execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 }));
  const ghLines = (args) => execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 })
    .split("\n").filter((l) => l.trim() !== "").map((l) => JSON.parse(l));
  let commits;
  let rewritten = false;
  try {
    // The compare is used for its COMMIT list and status only — the commits
    // paginate cleanly; the file list does not (it is capped and lives on page 1).
    const status = ghJson(["api", `repos/{owner}/{repo}/compare/${before}...${after}`, "--jq", "{status: .status, behind: .behind_by}"]);
    rewritten = status.status === "diverged" || Number(status.behind) > 0;
    const inRound = ghLines(["api", "--paginate", `repos/{owner}/{repo}/compare/${before}...${after}?per_page=100`, "--jq", ".commits[] | {sha, n: (.parents | length)}"]);
    const prShas = new Set(ghLines(["api", "--paginate", `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`, "--jq", ".[].sha"]));
    commits = [];
    for (const { sha } of roundCommits(inRound, prShas).slice(0, 50)) {
      const files = ghLines(["api", "--paginate", `repos/{owner}/{repo}/commits/${sha}?per_page=100`, "--jq", ".files[]"]);
      commits.push({ sha, parents: [{}], files });
    }
  } catch (err) {
    console.error(`test-removals: could not read the round's commits (${err.message}); recording nothing.`);
    return;
  }
  const removals = aggregateCommits(commits);
  if (removals.length === 0 && !rewritten) {
    console.error("test-removals: the fix round removed no test.");
    return;
  }
  try {
    execFileSync("gh", ["pr", "comment", pr, "--body-file", "-"], {
      input: renderTestRemovals({ head, after, removals, rewritten }), encoding: "utf8", maxBuffer: 32 * 1024 * 1024,
    });
    console.error(`test-removals: recorded ${removals.length} file(s)${rewritten ? " (history rewritten)" : ""}.`);
  } catch (err) {
    console.error(`test-removals: could not post (${err.message}).`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
