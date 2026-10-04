// What may a fix round change, and did it say anything it should not have?
//
// WHY THIS EXISTS. The fixer prompt has always said "stay in scope: modify only
// the flagged files unless a fix genuinely requires touching others". Prose was
// not enough. In October 2026 loop fixers added an auth-webhook TTL cache and
// an auth gate to a payload-hardening PR (#2111), version-vector caps and HLL
// proto changes to a GC-registration PR (#2100), and a revision-API rewrite to a
// document-size PR (#2108), each to satisfy a finding on code the PR did not
// touch — and each became new surface the next round reviewed. The same rounds
// left task files saying "the panel re-raised it", "stop raising this", "fifth
// re-filing": the fixer arguing with its reviewer inside the tree, where the
// next round reads it as part of the change.
//
// So the rule is now MECHANICAL, in two layers:
//
//   1. A pre-push hook in the fixer's checkout (`prepush`) refuses a push whose
//      commits touch a path outside the scope, and tells the fixer what to do
//      instead (revert it, dispute or skip the finding). Fast feedback, inside
//      the session, while the fixer can still act on it. The agent owns its
//      checkout and could remove the hook, so this is the guide, not the gate.
//   2. The trusted report job (`check`), on a fresh runner, reads the round's
//      own commits through the API and PAGES when any of them touched a path
//      outside the scope — or wrote reviewer-directed text. That is the gate.
//
// THE SCOPE is the PR's ORIGINAL diff — the files `main...<frozen>` changed,
// where `<frozen>` is the head at the FIRST fix dispatch (`review-surface.mjs`
// `frozenShaFrom`, the same unforgeable anchor the surface gate uses) — plus
// any test file and any documentation file. Tests and docs are open because
// writing a reproducer next to the code it covers, and recording a round in the
// task's lessons file, are exactly what a fix round is asked to do; they do not
// change what the product does.
//
// WHY PAGE AND NOT STRIP. Reverting the out-of-scope paths from a trusted job
// would need a second push path (the design doc names it as future work: the
// agent hands a bundle to a trusted job that pushes), and the in-scope half of
// the round may not build without the half that was stripped — a red CI then
// hands the PR to the CI-fix arm, whose fixer would put the code straight back.
// A page is the one response that cannot make the branch worse.
//
// THE LINT (`lintRound`) runs over the same commits and reports three things:
//
//   reviewer-directed text  BLOCKS (pages with the scope violation). Narrow
//                           phrases only — "stop raising", "the panel re-raised
//                           it", "fifth re-filing", "note to the reviewer" — on
//                           lines the round ADDED to docs/task files. The lens
//                           rubric already calls such text a major finding; the
//                           page saves a $12 panel round that would only say so.
//   weakened assertions     WARNS. A test file whose round removed more
//                           assertions than it added, or added a tautology. A
//                           refactor legitimately moves assertions, and
//                           test-removals.mjs already shows deleted tests to the
//                           adjudicator, so this is evidence, not a gate.
//   one-sided CRDT rule     WARNS. A non-test change under pkg/document/crdt/
//                           changes a replicated rule that yorkie-js-sdk must
//                           apply identically or replicas diverge. This repo
//                           cannot see the other SDK, so it can only ask.
//
// Usage:
//   node fix-guard.mjs allowed <pr> [--base main] --out <file>
//   node fix-guard.mjs prepush --allowed <file> [--base main]  (git pre-push hook)
//   node fix-guard.mjs check <pr> --before <sha> --after <sha> [--base main]
//       [--pusher <login> --since <iso> --branch <name>] [--github-output]
// `check` posts one `<!-- agent-fix-guard -->` comment when it has anything to
// say and writes `blocking=true|false` to $GITHUB_OUTPUT. It exits 0 except when
// a blocking page could not be posted, so a net still sees the failure.

import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { isTestFile, roundCommits, fixerHead, safePath } from "./test-removals.mjs";
import { frozenShaFrom } from "./review-surface.mjs";
import { PAGED_LATCH } from "./rounds.mjs";

export const FIX_GUARD_MARKER = "<!-- agent-fix-guard ";
export const FIX_GUARD_VERSION = 1;
const str = (v) => (typeof v === "string" ? v : "");

/** Documentation: anything under docs/, and Markdown anywhere. */
export function isDocFile(file) {
  const f = str(file);
  return /(^|\/)docs\//.test(f) || /\.(md|mdx)$/i.test(f);
}

/** Task files: the paired todo/lessons records a fix round is told to keep. */
export function isTaskFile(file) {
  return /(^|\/)docs\/tasks\//.test(str(file));
}

/** May a fix round change this path, given the PR's original file set? */
export function inScope(file, original) {
  const f = str(file);
  if (!f) return false;
  if (original instanceof Set ? original.has(f) : (Array.isArray(original) && original.includes(f))) return true;
  return isTestFile(f) || isDocFile(f);
}

/**
 * Paths a round touched that are out of scope. `files` is a list of
 * `{filename, status, previous_filename}` (a commit's `files`). A rename is
 * judged on BOTH names: moving an original file somewhere new is still a change
 * to a path outside the original set, and moving an unrelated file into an
 * original path's name would otherwise pass.
 */
export function scopeViolations(files, original) {
  const out = new Set();
  for (const f of Array.isArray(files) ? files : []) {
    const now = str(f?.filename);
    if (now && !inScope(now, original)) out.add(now);
    if (f?.status === "renamed") {
      const was = str(f?.previous_filename);
      if (was && !inScope(was, original)) out.add(was);
    }
  }
  return [...out].sort();
}

// --- the lint ----------------------------------------------------------------

/** Lines a patch ADDED, without the leading `+`. */
export function addedLines(patch) {
  return str(patch)
    .split("\n")
    .filter((l) => l.startsWith("+") && !l.startsWith("+++"))
    .map((l) => l.slice(1));
}

function removedLines(patch) {
  return str(patch)
    .split("\n")
    .filter((l) => l.startsWith("-") && !l.startsWith("---"))
    .map((l) => l.slice(1));
}

/**
 * Text addressed to the reviewer rather than to a reader of the code. Each
 * pattern is a PHRASE, not a topic: a lessons file that says "panel round 3
 * raised X; fixed by Y" is the task workflow working as designed and must not
 * match. These are the shapes that argue with the gate.
 */
export const REVIEWER_DIRECTED = Object.freeze([
  /\b(please\s+)?(stop|quit|cease)\s+(re-?)?(rais|flagg|report|fil)(e|es|ed|ing)?\b/i,
  /\b(do\s+not|don'?t|never)\s+(re-?)?(raise|flag|report|re-?file)\s+(this|it|that|again)\b/i,
  /\b(the\s+)?(panel|reviewer|lens|adjudicator)s?\s+(re-?raised|re-?flagged|re-?filed|raised\s+it\s+again|keeps?\s+(re-?)?raising|insists?)\b/i,
  /\b(first|second|third|fourth|fifth|sixth|seventh|eighth|ninth|tenth|\d+(st|nd|rd|th))\s+(re-?filing|re-?raise|re-?flag|time\s+(the\s+)?(panel|reviewer|lens)\b)/i,
  /\b(re-?raised|re-?filed|re-?flagged)\s+(again\s+)?(for\s+the\s+\w+\s+time|\d+\s+times)\b/i,
  /\bnote\s+to\s+(the\s+)?(reviewer|panel|adjudicator|lens)s?\b/i,
  /\b(reviewers?|the\s+panel|the\s+adjudicator|lenses)\s*[:,]?\s+(should\s+not|must\s+not|need\s+not|can\s+stop|please\s+(do\s+not|don'?t|stop|ignore))\b/i,
]);

/** Reviewer-directed lines a round ADDED to docs or task files. */
export function reviewerDirectedHits(files) {
  const hits = [];
  for (const f of Array.isArray(files) ? files : []) {
    const name = str(f?.filename);
    if (!isDocFile(name)) continue;
    for (const line of addedLines(f?.patch)) {
      if (REVIEWER_DIRECTED.some((re) => re.test(line))) {
        hits.push({ file: name, text: line.trim().slice(0, 200) });
      }
    }
  }
  return hits;
}

// Go: testify `assert.X(` / `require.X(`, and `t.Error*`/`t.Fatal*`.
// JS: `assert.X(` / `assert(`, `expect(`.
const ASSERTION = /\b(assert|require)\.[A-Z]\w*\(|\bt\.(Error|Errorf|Fatal|Fatalf)\(|\bassert(\.\w+)?\(|\bexpect\(/;
// A check that can never fail, which a weakened test sometimes becomes.
const TAUTOLOGY = /\b(assert|require)\.True\(\s*t\s*,\s*true\s*[,)]|\bassert\.ok\(\s*true\s*[,)]|\bexpect\(\s*true\s*\)\.toBe\(\s*true\s*\)/;

/** Test files whose round removed more assertions than it added, or added a tautology. */
export function weakenedAssertions(files) {
  const out = [];
  for (const f of Array.isArray(files) ? files : []) {
    const name = str(f?.filename);
    if (!isTestFile(name) || typeof f?.patch !== "string") continue;
    const added = addedLines(f.patch);
    const removed = removedLines(f.patch).filter((l) => ASSERTION.test(l)).length;
    const kept = added.filter((l) => ASSERTION.test(l)).length;
    const tautologies = added.filter((l) => TAUTOLOGY.test(l)).length;
    if (removed > kept || tautologies > 0) out.push({ file: name, removed, added: kept, tautologies });
  }
  return out;
}

/** Non-test changes to a replicated CRDT rule. */
export function crdtRuleChanges(files) {
  return (Array.isArray(files) ? files : [])
    .map((f) => str(f?.filename))
    .filter((n) => /^pkg\/document\/crdt\/[^/]+\.go$/.test(n) && !isTestFile(n));
}

/**
 * The whole round, judged. `files` is the union of the round's commits' files
 * (one entry per commit per file — patches are read per commit). `original` is
 * the scope set, or null when it could not be established, in which case scope
 * is NOT enforced (an unknown scope must not page every round) and the record
 * says so.
 */
export function lintRound({ files = [], original = null, prBody = "" } = {}) {
  const violations = original ? scopeViolations(files, original) : [];
  const reviewerDirected = reviewerDirectedHits(files);
  const weakened = weakenedAssertions(files);
  // Asked only when nothing already points at the other SDK: a PR body that
  // names yorkie-js-sdk has made the cross-SDK change visible to a human.
  const crdt = /yorkie-js-sdk/i.test(str(prBody)) ? [] : [...new Set(crdtRuleChanges(files))];
  return {
    scopeKnown: !!original,
    violations,
    reviewerDirected,
    weakened,
    crdt,
    blocking: violations.length > 0 || reviewerDirected.length > 0,
  };
}

const HANDOFF =
  "The review panel will not run again on this PR, so the `agent-review-*` checks are now " +
  "frozen at their current state and the ready gate will not promote it. Revert the listed " +
  "changes (or accept them deliberately), then comment `@claude rerun`.";

/** The comment. Paths are rendered through `safePath`; quoted text is neutralised. */
export function renderFixGuard(result, { head = "", after = "" } = {}) {
  const q = (s) => str(s).replace(/<!--/g, "<!-‌-").replace(/@(?=\w)/g, "@​").replace(/`/g, "'");
  const rec = {
    v: FIX_GUARD_VERSION,
    head: str(head).slice(0, 40),
    after: str(after).slice(0, 40),
    blocking: result.blocking,
    violations: result.violations.length,
    reviewerDirected: result.reviewerDirected.length,
    weakened: result.weakened.length,
    crdt: result.crdt.length,
  };
  const lines = [];
  if (result.blocking) lines.push(PAGED_LATCH);
  lines.push(`${FIX_GUARD_MARKER}${JSON.stringify(rec).replace(/--/g, "-\\u002d")} -->`);
  if (result.blocking) {
    lines.push("🛑 **The fix round stepped outside what a fix round may change.** A human should look before the loop continues.", "");
  } else {
    lines.push("⚠️ **Fix-round lint** — warnings only; the loop continues.", "");
  }
  if (result.violations.length) {
    lines.push(
      "**Files outside the PR's scope.** A fix round may change the files in the PR's original diff, plus tests and docs. A finding that needs any other file is not this PR's to fix — it should have been disputed or skipped, and the panel files it as a follow-up when it is out of the diff:",
      ...result.violations.slice(0, 40).map((f) => `- \`${safePath(f)}\``),
      "",
    );
  }
  if (result.reviewerDirected.length) {
    lines.push(
      "**Text addressed to the reviewer**, added to docs or task files. Disagreement with a finding goes in a structured rebuttal, where an adjudicator reads it — not in the tree, where every later round reads it as part of the change:",
      ...result.reviewerDirected.slice(0, 20).map((h) => `- \`${safePath(h.file)}\`: "${q(h.text)}"`),
      "",
    );
  }
  if (result.weakened.length) {
    lines.push(
      "**Assertions removed (warning).** These test files lost more assertions than they gained, or gained one that cannot fail. Check none of them was the reproducer for a finding claimed fixed:",
      ...result.weakened.slice(0, 20).map((w) => `- \`${safePath(w.file)}\`: −${w.removed} / +${w.added}${w.tautologies ? `, ${w.tautologies} tautolog${w.tautologies === 1 ? "y" : "ies"}` : ""}`),
      "",
    );
  }
  if (result.crdt.length) {
    lines.push(
      "**Replicated CRDT rule changed in Go only (warning).** yorkie-js-sdk must apply the same rule or a Go server and JS clients diverge. Link the matching yorkie-js-sdk change in the PR body, or say why none is needed:",
      ...result.crdt.slice(0, 20).map((f) => `- \`${safePath(f)}\``),
      "",
    );
  }
  if (!result.scopeKnown) lines.push("_The PR's original file set could not be read, so scope was not checked this round._", "");
  if (result.blocking) lines.push(HANDOFF);
  return lines.join("\n");
}

// --- CLI ---------------------------------------------------------------------

const gh = (args, input) => execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, ...(input ? { input } : {}) });
const ghJson = (args) => JSON.parse(gh(args));
const ghLines = (args) => gh(args).split("\n").filter((l) => l.trim() !== "").map((l) => JSON.parse(l));

/** The compare API lists at most 300 files; at the cap the set is incomplete, so unknown. */
export const COMPARE_FILE_CAP = 300;

/**
 * The PR's original file set: `base...<frozen>`, frozen at the first fix
 * dispatch. Null when it cannot be established — no dispatch yet and no
 * fallback head, an API failure, or a capped list.
 */
export function readOriginalFiles(pr, { base = "main", fallbackHead = "" } = {}) {
  try {
    const comments = ghJson(["api", "--paginate", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]);
    // No dispatch on record means THIS is the first round, and the head the
    // fixer starts from IS the original diff.
    const frozen = frozenShaFrom(comments) || (/^[0-9a-f]{40}$/i.test(fallbackHead) ? fallbackHead : "");
    if (!frozen) return null;
    const files = ghJson(["api", `repos/{owner}/{repo}/compare/${encodeURIComponent(base)}...${frozen}?per_page=100`, "--jq", "[.files[].filename]"]);
    if (!Array.isArray(files) || files.length === 0 || files.length >= COMPARE_FILE_CAP) return null;
    return new Set(files);
  } catch (err) {
    console.error(`fix-guard: could not read the original file set (${err.message}).`);
    return null;
  }
}

function flag(argv, k) {
  const i = argv.indexOf(`--${k}`);
  return i >= 0 ? str(argv[i + 1]) : "";
}

function cmdAllowed(argv) {
  const pr = argv.find((a) => /^\d+$/.test(a));
  const out = flag(argv, "out");
  if (!pr || !out) {
    console.error("usage: fix-guard.mjs allowed <pr> --out <file> [--base main] [--head <sha>]");
    return;
  }
  const original = readOriginalFiles(pr, { base: flag(argv, "base") || "main", fallbackHead: flag(argv, "head") });
  // An EMPTY file means "scope unknown": the hook then allows everything, and the
  // report job's check says the scope was not enforced. Never a guess.
  writeFileSync(out, original ? `${[...original].join("\n")}\n` : "");
  console.error(original ? `fix-guard: ${original.size} file(s) in the original diff → ${out}` : "fix-guard: scope unknown; the hook will not restrict paths");
}

/**
 * The pre-push hook. Reads git's `<local ref> <local sha> <remote ref> <remote
 * sha>` lines from stdin, lists the files the FIXER'S OWN commits touched, and
 * refuses the push when any is out of scope.
 *
 * "Own" excludes the base branch's history, and `--no-merges` alone does not:
 * after a `git merge origin/main`, `<remote>..<local>` still contains every main
 * commit the merge brought in (only the merge commit itself is dropped), so a
 * CI-fix round that merged main was refused for main's files. `pushedLogArgs`
 * walks first-parent only — the branch's own line, never into a merged side —
 * and subtracts `origin/<base>` when that ref exists, which also covers a rebase
 * (its new base commits ARE on the first-parent line). That matches the report
 * job, which takes only commits in the PR's own commit list.
 */
export function pushedLogArgs({ localSha, remoteSha, baseRef = "" } = {}) {
  const sha = /^[0-9a-f]{40}$/;
  if (!sha.test(str(localSha)) || /^0{40}$/.test(localSha)) return null;
  const hasRemote = sha.test(str(remoteSha)) && !/^0{40}$/.test(remoteSha);
  // A new branch with no base to subtract would list the whole repository's
  // history. There is nothing sound to judge, so do not judge (the report job
  // still does).
  if (!hasRemote && !baseRef) return null;
  return [
    "log", "--first-parent", "--no-merges", "--format=", "--name-only",
    hasRemote ? `${remoteSha}..${localSha}` : localSha,
    ...(baseRef ? ["--not", baseRef] : []),
  ];
}
export function prepushVerdict({ touched = [], allowed = null } = {}) {
  if (!allowed || allowed.size === 0) return { ok: true, violations: [] };
  const violations = [...new Set(touched.filter((f) => !inScope(f, allowed)))].sort();
  return { ok: violations.length === 0, violations };
}

function cmdPrepush(argv) {
  const file = flag(argv, "allowed");
  let allowed = null;
  try {
    const list = readFileSync(file, "utf8").split("\n").map((s) => s.trim()).filter(Boolean);
    allowed = list.length ? new Set(list) : null;
  } catch {
    allowed = null;
  }
  const stdin = (() => { try { return readFileSync(0, "utf8"); } catch { return ""; } })();
  const touched = [];
  const baseName = flag(argv, "base") || "main";
  let baseRef = "";
  try {
    execFileSync("git", ["rev-parse", "--verify", "-q", `refs/remotes/origin/${baseName}`], { stdio: "ignore" });
    baseRef = `refs/remotes/origin/${baseName}`;
  } catch {
    // No remote-tracking base: first-parent alone still keeps a merge's side out.
  }
  for (const line of stdin.split("\n")) {
    const [, localSha, , remoteSha] = line.trim().split(/\s+/);
    const args = pushedLogArgs({ localSha, remoteSha, baseRef });
    if (!args) continue;
    try {
      const out = execFileSync("git", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
      touched.push(...out.split("\n").map((s) => s.trim()).filter(Boolean));
    } catch {
      // Cannot list → do not block; the report job's check is the gate.
    }
  }
  const v = prepushVerdict({ touched, allowed });
  if (v.ok) return;
  console.error(
    [
      "",
      "fix-guard: PUSH REFUSED — these files are outside what a fix round may change:",
      ...v.violations.map((f) => `  ${f}`),
      "",
      "A fix round may change the files in the PR's original diff, plus tests and docs.",
      "A finding that needs any other file is not this PR's to fix. Revert those files",
      "(git checkout <base-of-round> -- <file>, or git rm a file you added), commit, and",
      "report the finding with --skipped (or file a rebuttal) saying it needs code outside",
      "the PR. The panel files out-of-diff findings as follow-up issues. Do not bypass",
      "this hook: the report job checks the same rule and pages a human if it is broken.",
      "",
    ].join("\n"),
  );
  process.exit(1);
}

function cmdCheck(argv) {
  const pr = argv.find((a) => /^\d+$/.test(a));
  const before = flag(argv, "before");
  let after = flag(argv, "after");
  const out = (k, v) => {
    console.error(`  ${k}=${v}`);
    if (argv.includes("--github-output") && process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${k}=${v}\n`);
  };
  if (!pr || !/^[0-9a-f]{40}$/i.test(before) || !/^[0-9a-f]{40}$/i.test(after)) {
    console.error("usage: fix-guard.mjs check <pr> --before <sha40> --after <sha40> [--base main] [--pusher <login> --since <iso> --branch <name>] [--github-output]");
    out("blocking", "false");
    return;
  }
  if (flag(argv, "pusher") && flag(argv, "since") && flag(argv, "branch")) {
    try {
      const acts = ghJson(["api", `repos/{owner}/{repo}/activity?ref=${encodeURIComponent(flag(argv, "branch"))}&per_page=100`]);
      const own = fixerHead(acts, { before, pusher: flag(argv, "pusher"), since: flag(argv, "since"), ref: flag(argv, "branch") });
      if (own) after = own;
    } catch (err) {
      console.error(`fix-guard: could not read push activity (${err.message}); using the live head.`);
    }
  }
  let files = [];
  let prBody = "";
  try {
    const inRound = ghLines(["api", "--paginate", `repos/{owner}/{repo}/compare/${before}...${after}?per_page=100`, "--jq", ".commits[] | {sha, n: (.parents | length)}"]);
    const prShas = new Set(ghLines(["api", "--paginate", `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`, "--jq", ".[].sha | tojson"]));
    for (const { sha } of roundCommits(inRound, prShas).slice(0, 50)) {
      files.push(...ghLines(["api", "--paginate", `repos/{owner}/{repo}/commits/${sha}?per_page=100`, "--jq", ".files[]"]));
    }
    prBody = str(ghJson(["api", `repos/{owner}/{repo}/pulls/${pr}`, "--jq", "{body}"]).body);
  } catch (err) {
    // Unread round → nothing to say. The test-removals record makes the same call.
    console.error(`fix-guard: could not read the round's commits (${err.message}); checking nothing.`);
    out("blocking", "false");
    return;
  }
  const original = readOriginalFiles(pr, { base: flag(argv, "base") || "main" });
  const result = lintRound({ files, original, prBody });
  out("blocking", String(result.blocking));
  const anything = result.blocking || result.weakened.length || result.crdt.length;
  if (!anything) {
    console.error("fix-guard: the round stayed in scope and the lint found nothing.");
    return;
  }
  try {
    gh(["pr", "comment", pr, "--body-file", "-"], renderFixGuard(result, { head: before, after }));
  } catch (err) {
    console.error(`fix-guard: could not post (${err.message}).`);
    // A page that did not land must red the step so a net still sees it.
    if (result.blocking) process.exit(1);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [verb, ...rest] = process.argv.slice(2);
  if (verb === "allowed") cmdAllowed(rest);
  else if (verb === "prepush") cmdPrepush(rest);
  else if (verb === "check") cmdCheck(rest);
  else console.error("usage: fix-guard.mjs allowed|prepush|check ...");
}
