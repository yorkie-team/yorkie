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

/**
 * This repo's test layouts: `test/` trees, `__tests__/`, `*_test.go`, Go files
 * under a `testcases/` directory, and `*.test.*` / `*.spec.*` / `*_test.*` JS
 * files.
 *
 * `testcases/` because yorkie keeps the bodies of its database and RPC suites
 * in shared, non-`_test.go` files (`server/backend/database/testcases/`,
 * `server/rpc/testcases/`) that thin `_test.go` wrappers call. Decided by the
 * PATH, not by whether the patch shows `*testing.T`: whether a signature is in
 * a hunk's context depends on how much context the diff carries, and the
 * record must not change with it.
 */
export function isTestFile(file) {
  const f = str(file);
  return /(^|\/)(test|tests|__tests__)\//.test(f) || isSharedGoSuite(f) || isRunnableTest(f);
}

/** A shared Go suite body: a `.go` file under a `testcases/` directory. */
function isSharedGoSuite(file) {
  return /(^|\/)testcases\/(.*\/)?[^/]*\.go$/.test(str(file));
}

// What `go test ./...` on CI (linux/amd64) skips by NAME: a path element named
// `testdata` or starting with `_` or `.`, and a `_GOOS`/`_GOARCH` (or
// `_GOOS_GOARCH`) suffix for another platform.
const GOOS = new Set(["aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos"]);
const GOARCH = new Set(["386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips", "mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm"]);
const CI_GOOS = "linux";
const CI_GOARCH = "amd64";

function goTestInReach(file) {
  const parts = file.split("/");
  if (parts.some((p) => p === "testdata" || p.startsWith("_") || p.startsWith("."))) return false;
  // `name_GOOS_GOARCH_test.go`: go/build ignores the FIRST element as a
  // constraint, so `linux_test.go` alone runs everywhere.
  const elems = parts[parts.length - 1].replace(/_test\.go$/, "").split("_");
  if (elems.length < 2) return true;
  const last = elems[elems.length - 1];
  const prev = elems.length >= 3 ? elems[elems.length - 2] : "";
  if (GOARCH.has(last)) {
    if (last !== CI_GOARCH) return false;
    return !(GOOS.has(prev) && prev !== CI_GOOS);
  }
  return !(GOOS.has(last) && last !== CI_GOOS);
}

/** A file the runners pick up by NAME (`*_test.go` in reach of `go test` on CI; `*.test.*`, `*.spec.*`, `*_test.*` in JS), wherever it lives. */
export function isRunnableTest(file) {
  const f = str(file);
  if (/_test\.go$/.test(f)) return goTestInReach(f);
  return /[._](test|spec)\.[cm]?[jt]sx?$/.test(f);
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
// In a shared suite body (`testcases/`), a top-level `func RunXxx(` is the case:
// gofmt breaks its signature over the following lines, so the parameter type
// is not on the line to check.
const GO_SHARED_CASE = /^[-+]func\s+Run[A-Z0-9_]\w*\s*\(/;
const GO_SUBTEST = /^[-+]\s*[\w.]+\.Run\(.*func\s*\(\s*\w+\s+\*testing\.T\s*\)/;
// A case switched off in place. Unlike `it.skip`, `t.Skip` is a new line in an
// unchanged function, so it is counted apart from cases, like a disabled suite,
// and netted only against skips removed in the same file. Any receiver
// (`t`, `b`, `tb`, `s.T()`). Comment lines are not calls.
const GO_SKIP = /^[-+](?!\s*\/\/).*[\w)]\.Skip(?:f|Now)?\(/;
// A build constraint, current or legacy. In an existing test file, a new or
// changed expression can take the whole file out of every lane that runs it
// (`//go:build integration` → `//go:build ignore`), the Go form of a disabled
// suite. A new file's constraint is not — it was never running — unless it
// names the `ignore` tag, which no lane sets: a test born ignored is a test
// switched off.
const GO_BUILD = /^([-+])\/\/(?:go:build|\s*\+build)\s+(.*?)\s*$/;
const ignores = (expr) => /(^|[^\w!])ignore(?!\w)/.test(expr);

/**
 * Build-constraint expressions added that the same span never removed. `added`
 * and `removed` are the expressions seen on `+` and `-` lines, unioned over a
 * patch or over a whole round, so a constraint changed and changed back nets
 * out. `newFile`: the file did not exist before the span.
 */
function constraintsOff(added, removed, newFile) {
  return [...added].filter((e) => !removed.has(e) && (!newFile || ignores(e))).length;
}

/**
 * The raw tally behind `countCases`, kept SIGNED so a round can net it across
 * commits before clamping: `offNet` is suites/skips switched off minus those
 * switched back on, and the build-constraint sets are left for the caller.
 */
function tally(patch, { file = "", status = "" } = {}) {
  const go = isGo(file);
  const shared = go && isSharedGoSuite(file);
  let removed = 0, added = 0, offNet = 0;
  const builds = { "+": new Set(), "-": new Set() };
  for (const line of str(patch).split("\n")) {
    if (line.startsWith("---") || line.startsWith("+++")) continue;
    const sign = line[0] === "+" ? 1 : -1;
    if (go) {
      const b = GO_BUILD.exec(line);
      if (b) {
        builds[b[1]].add(b[2]);
        continue;
      }
      if (GO_SKIP.test(line)) {
        offNet += sign;
        continue;
      }
      if (!GO_CASE.test(line) && !GO_SUBTEST.test(line) && !(shared && GO_SHARED_CASE.test(line))) continue;
    } else {
      if (SUITE_OFF.test(line)) {
        offNet += sign;
        continue;
      }
      if (!CASE.test(line)) continue;
    }
    if (sign < 0) removed++;
    else added++;
  }
  return { removed, added, offNet, buildsAdded: builds["+"], buildsRemoved: builds["-"], newFile: status === "added" };
}

const suitesOffOf = (t) => Math.max(0, t.offNet) + constraintsOff(t.buildsAdded, t.buildsRemoved, t.newFile);

/**
 * Active cases a unified diff removes and adds, and how many suites it newly
 * switches off. Suite lines are netted against THEMSELVES only, so editing an
 * already-skipped suite's title, or re-enabling one, is not a disablement.
 * `file` picks the language (a `.go` file gets the Go rules, anything else the
 * JS ones); `status` is the file's GitHub status, which only the Go
 * build-constraint rule reads.
 */
export function countCases(patch, opts = {}) {
  const t = tally(patch, opts);
  return { removed: t.removed, added: t.added, suitesOff: suitesOffOf(t) };
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
    const t = tally(f.patch, { file: was, status: f.status });
    out.push({ file: was, deleted, renamedAway, removed: t.removed, added: t.added, suitesOff: suitesOffOf(t), raw: t });
  }
  return out;
}

/** Keep only what is a removal: a deletion that held cases, a rename away, a net loss, a suite off, or unreadable. */
function removalsOf(list) {
  const out = [];
  for (const e of list) {
    const hit = e.unreadable || e.renamedAway || e.suitesOff > 0 || (e.deleted ? e.removed > 0 : e.removed > e.added);
    if (!hit) continue;
    const { renamedAway: _r, raw: _t, ...rest } = e;
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
 *
 * Cases are summed; switch-offs are NETTED across the round before they are
 * clamped. The Go fixer is told to write skips, so a skip added in one commit
 * and taken out in a later one — or a constraint changed and changed back — is
 * no switch-off at all, and clamping per commit would report it.
 */
export function aggregateCommits(commits) {
  const byFile = new Map();
  for (const c of Array.isArray(commits) ? commits : []) {
    if (Array.isArray(c?.parents) && c.parents.length > 1) continue;
    for (const e of entries(c?.files)) {
      const fresh = !byFile.has(e.file);
      const cur = byFile.get(e.file) ?? {
        file: e.file, deleted: false, removed: 0, added: 0, suitesOff: 0,
        raw: { offNet: 0, buildsAdded: new Set(), buildsRemoved: new Set(), newFile: e.raw?.newFile === true },
      };
      if (fresh && !e.raw) cur.raw.newFile = false;
      cur.removed += e.removed;
      cur.added += e.added;
      if (e.raw) {
        cur.raw.offNet += e.raw.offNet;
        for (const x of e.raw.buildsAdded) cur.raw.buildsAdded.add(x);
        for (const x of e.raw.buildsRemoved) cur.raw.buildsRemoved.add(x);
      }
      cur.suitesOff = suitesOffOf(cur.raw);
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
