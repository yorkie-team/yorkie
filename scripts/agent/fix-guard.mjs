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
// RUST (the `cargo` rule set) keeps most unit tests inside the source file
// they test, so "any test file" does not reach them: a reproducer written into
// `mod tests` of a file the pull request did not touch would be out of scope,
// and a path rule cannot tell that hunk from one in the function above it. So
// for a Rust file outside the scope the guard reads the WHOLE FILE on both
// sides of each commit and allows the change when it is confined to the
// file's test regions — top-level `#[cfg(test)] mod … { … }` blocks and
// `#[cfg(test)] mod …;` declarations (`cargo-rules.mjs` `testOnlyChange`:
// with the test regions taken out, the file must be the same tokens). That
// covers the three places a Rust reproducer goes: an existing `mod tests`, a
// new one at the bottom of a file, and a new `*_tests.rs` file (in scope by
// its name) with its one declaration line in the parent module. Whatever
// cannot be shown to be test-only — a file that was not read, does not lex,
// was added, renamed or deleted — stays out of scope, and pages.
//
// WHICH COMMITS ARE JUDGED, AND THE ONES THAT CANNOT BE. Both layers read the
// round's ordinary commits: one parent each, on the branch's own line. A
// change can also ride in a commit of another shape — typed into a merge
// commit, made on a side branch and merged in, or carried by a commit with no
// parent that is merged with `--allow-unrelated-histories` — and each of
// those used to reach neither layer. A fix round has one reason to make a
// merge: bringing the base branch in to resolve a conflict. So that one is
// allowed, when every parent but the first is already on the base branch and
// no file the merge left different from BOTH parents is out of scope (a file
// that differs from both sides is where a conflict was resolved, or where
// something was typed in). Any other merge, and any commit with no parent,
// is refused by the hook and paged by the report. So is a round with more
// commits than are read, and a round whose commits could not be listed: what
// was not judged is not "in scope".
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
//   one-sided replicated    WARNS. A non-test change to a path the harness
//   rule                    configuration lists (`fixGuard.replicatedRuleGlobs`)
//                           changes a rule a counterpart repository must apply
//                           identically or replicas diverge. This repo cannot
//                           see the counterpart, so it can only ask. A
//                           repository with no such rule lists no paths.
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
import { compileGlob, get, TEST_RULE_SETS } from "./config.mjs";
import { isCargoGoverned, isCargoUnitTestByName, isTestFile, roundCommits, fixerHead, safePath, ROUND_COMMIT_CAP } from "./test-removals.mjs";
import { declaresTestModule, patchLines, testOnlyChange, testRegionLines } from "./cargo-rules.mjs";
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

const CARGO_RULES = get("tests.rules").includes("cargo");
/** A Rust source file that is not test code as a whole: where inline tests live. */
const isRustSource = (file) => CARGO_RULES && /\.rs$/.test(str(file)) && !isTestFile(file);

/**
 * Is this entry of a commit's file list a change to a Rust source file's test
 * regions and nothing else? Decided from `contents` — `{ before, after }`,
 * the whole file on each side of the commit, which `attachRustContents` puts
 * there — and never from the patch. No contents, no proof: false.
 */
export function rustTestOnly(f) {
  if (!isRustSource(f?.filename) || f?.status !== "modified") return false;
  return testOnlyChange(f.contents?.before, f.contents?.after);
}

/**
 * The parent modules that could declare `file`, a unit-test file named by
 * convention: `dir/name.rs` is reached from `dir/mod.rs`, from `dir.rs`
 * beside the directory, or from the crate root when the directory is `src`.
 */
function parentModules(file) {
  const dir = path.posix.dirname(file);
  const up = path.posix.dirname(dir);
  const out = [`${dir}/mod.rs`];
  if (dir !== "." && path.posix.basename(dir) !== "src") out.push(up === "." ? `${dir}.rs` : `${up}/${path.posix.basename(dir)}.rs`);
  if (path.posix.basename(dir) === "src") out.push(`${dir}/lib.rs`, `${dir}/main.rs`);
  return out;
}

/**
 * Is this by-name unit-test file really test code? `read(path)` returns a
 * parent module's source at the round's head, or `null`. True when one of the
 * file's possible parents declares it `#[cfg(test)] mod name;` and nowhere
 * else (`cargo-rules.mjs` `declaresTestModule`). A file called
 * `merge_tests.rs` whose parent says plain `mod merge_tests;` is part of the
 * library, and a new one is nothing until some parent declares it.
 */
export function rustDeclared(file, read) {
  if (!isCargoUnitTestByName(file)) return false;
  const name = path.posix.basename(str(file), ".rs");
  return parentModules(str(file)).some((parent) => declaresTestModule(read(parent), name));
}

/**
 * May a fix round change this path, given the PR's original file set?
 *
 * With the cargo rules, a path cargo decides the meaning of — Rust source, a
 * manifest, a lockfile, `.cargo/` — is in scope by its path only where the
 * CONFIGURATION says it is test code. A manifest never is. A unit-test file
 * named by convention is not in scope by its name: `scopeViolations` and the
 * hook admit it once its declaration has been read (`rustDeclared`).
 */
export function inScope(file, original) {
  const f = str(file);
  if (!f) return false;
  if (original instanceof Set ? original.has(f) : (Array.isArray(original) && original.includes(f))) return true;
  if (isCargoGoverned(f)) return isTestFile(f) && !isCargoUnitTestByName(f);
  return isTestFile(f) || isDocFile(f);
}

/** An entry for a by-name unit-test file that exists after the commit and whose declaration was found. */
const declaredTestFile = (f) => f?.declared === true && (f?.status === "added" || f?.status === "modified") && isCargoUnitTestByName(f?.filename);

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
    // A Rust file outside the scope is still fine when the commit changed
    // only its tests. One commit that did more puts the path on the list.
    if (now && !inScope(now, original) && !rustTestOnly(f) && !declaredTestFile(f)) out.add(now);
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
/**
 * Words that put a line in the REVIEW conversation rather than in the product.
 * The two verb-only patterns below ("stop raising", "do not report this") read
 * just as naturally about code — "stop reporting presence after detach", "stop
 * raising an error when the key is missing" — so they match only on a line that
 * also says who or what is being argued with. The other patterns name the
 * reviewer themselves and need no help.
 */
export const REVIEW_CONTEXT = /\b(panel|reviewers?|lens(es)?|adjudicator|findings?|re-?rais\w*|re-?fil\w*|re-?flag\w*|out\s+of\s+scope)\b/i;

const VERB_ONLY = Object.freeze([
  /\b(please\s+)?(stop|quit|cease)\s+(re-?)?(rais|flagg|report|fil)(e|es|ed|ing)?\b/i,
  /\b(do\s+not|don'?t|never)\s+(re-?)?(raise|flag|report|re-?file)\s+(this|it|that|again)\b/i,
]);

export const REVIEWER_DIRECTED = Object.freeze([
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
      const named = REVIEWER_DIRECTED.some((re) => re.test(line));
      const argued = REVIEW_CONTEXT.test(line) && VERB_ONLY.some((re) => re.test(line));
      if (named || argued) {
        hits.push({ file: name, text: line.trim().slice(0, 200) });
      }
    }
  }
  return hits;
}

// What an assertion looks like, per language rule set the repository has
// switched on (`tests.rules`) — the same switch `test-removals.mjs` reads, so a
// repository with no Go tests is not matched against Go idioms.
//   go:     testify `assert.X(` / `require.X(`, and `t.Error*`/`t.Fatal*`.
//   vitest: `assert.X(` / `assert(`, `expect(`.
//   cargo:  every `…assert…!` macro (`assert!`, `assert_eq!`, `assert_ne!`,
//           `assert_matches!`, `debug_assert*!`, `prop_assert*!`), `panic!`
//           and `unreachable!`. NOT `todo!()`/`unimplemented!()`: a body
//           replaced by one FAILS, and shows as the assertions it replaced.
//           NOT `.unwrap()`/`.expect(…)`/`?`, which is how a test returning
//           `Result` asserts — they are in all the set-up code too.
const ASSERTION_BY_RULES = {
  go: ["\\b(assert|require)\\.[A-Z]\\w*\\(", "\\bt\\.(Error|Errorf|Fatal|Fatalf)\\("],
  vitest: ["\\bassert(\\.\\w+)?\\(", "\\bexpect\\("],
  cargo: ["\\b\\w*assert\\w*!\\s*[(\\[{]", "\\b(?:panic|unreachable)!\\s*[(\\[{]"],
};
// A check that can never fail, which a weakened test sometimes becomes.
const TAUTOLOGY_BY_RULES = {
  go: ["\\b(assert|require)\\.True\\(\\s*t\\s*,\\s*true\\s*[,)]"],
  vitest: ["\\bassert\\.ok\\(\\s*true\\s*[,)]", "\\bexpect\\(\\s*true\\s*\\)\\.toBe\\(\\s*true\\s*\\)"],
  cargo: [
    "\\b(?:debug_)?assert!\\s*\\(\\s*(?:true|!\\s*false)\\s*[,)]",
    // The same operand on both sides: `assert_eq!(1, 1)`, `assert_eq!(a.b, a.b)`.
    // A call counts as an operand: `assert_eq!(f(), f())`, `assert_eq!(a.b(x), a.b(x))`.
    "\\b(?:debug_)?assert_eq!\\s*\\(\\s*(?<same>(?:[\\w.:&]|\\([^(),]*\\))+)\\s*,\\s*\\k<same>\\s*[,)]",
    "\\b(?:debug_)?assert_ne!\\s*\\(\\s*(?:true\\s*,\\s*false|false\\s*,\\s*true)\\s*[,)]",
    // A pattern that matches anything.
    "\\bassert_matches!\\s*\\(.*,\\s*_\\s*[,)]",
  ],
};
/** Matches no line: what "no rule set applies here" compiles to. */
const NO_PATTERN = /(?!)/;

/**
 * One pattern for the rule sets in `sets`, in the order the table declares
 * them, whatever order the configuration lists them in, so the pattern is the
 * same for the same sets.
 *
 * TWO WAYS THIS USED TO BE ABLE TO MATCH EVERYTHING, both closed here. A rule
 * set that exists (`known`) with no entry in the table contributed nothing,
 * and when it was the only one enabled the pattern was `new RegExp("")` —
 * which matches every line, so every removed line was a removed assertion and
 * every added one a tautology. A missing or empty entry now throws, for every
 * known rule set, at load: adding a rule set without saying what its
 * assertions look like cannot be loaded. And no sets at all is `NO_PATTERN`.
 */
export function rulePattern(byRules, sets, known = TEST_RULE_SETS) {
  for (const rules of known) {
    const entry = byRules[rules];
    if (!Array.isArray(entry) || entry.length === 0 || entry.some((p) => typeof p !== "string" || p === "")) {
      throw new Error(`fix-guard: the rule set ${JSON.stringify(rules)} has no patterns in this table; a rule set must say what its assertions and its tautologies look like`);
    }
  }
  const sources = Object.keys(byRules).filter((rules) => sets.includes(rules)).flatMap((rules) => byRules[rules]);
  return sources.length > 0 ? new RegExp(sources.join("|")) : NO_PATTERN;
}
// Rust lines are read with the Rust patterns alone and every other file with
// the others: the sets do not share idioms (JavaScript's `expect(` is Rust's
// `.expect("…")`, which is in every line of set-up), and keeping cargo out of
// the shared pattern leaves it exactly what it was before cargo existed.
const ENABLED = get("tests.rules");
const SHARED_SETS = ENABLED.filter((rules) => rules !== "cargo");
const ASSERTION = rulePattern(ASSERTION_BY_RULES, SHARED_SETS);
const TAUTOLOGY = rulePattern(TAUTOLOGY_BY_RULES, SHARED_SETS);
const RUST_ASSERTION = rulePattern(ASSERTION_BY_RULES, CARGO_RULES ? ["cargo"] : []);
const RUST_TAUTOLOGY = rulePattern(TAUTOLOGY_BY_RULES, CARGO_RULES ? ["cargo"] : []);

/**
 * The lines of a Rust source file's patch that are TEST code, as
 * `{ added, removed }` texts. With the file's contents, a line counts when it
 * is inside a test region on its own side (`cargo-rules.mjs`). Without them —
 * the read failed, or the file does not lex — every line counts: the lint is
 * a warning, and one that names a file it could not read is better than a
 * weakened test it never looked at.
 */
function rustTestLines(f) {
  const lines = patchLines(f.patch).filter((l) => l.sign !== " ");
  // At any depth: a `#[cfg(test)]` module nested in another module is test
  // code to a lint, though it is not a region the scope rule accepts.
  const before = testRegionLines(f.contents?.before, { anyDepth: true });
  const after = testRegionLines(f.contents?.after, { anyDepth: true });
  const located = before && after && lines.every((l) => (l.sign === "+" ? l.newLine : l.oldLine) > 0);
  const kept = located ? lines.filter((l) => (l.sign === "+" ? after(l.newLine) : before(l.oldLine))) : lines;
  return {
    added: kept.filter((l) => l.sign === "+").map((l) => l.text),
    removed: kept.filter((l) => l.sign === "-").map((l) => l.text),
  };
}

/** Test files whose round removed more assertions than it added, or added a tautology. */
export function weakenedAssertions(files) {
  const out = [];
  for (const f of Array.isArray(files) ? files : []) {
    const name = str(f?.filename);
    if (typeof f?.patch !== "string") continue;
    // A Rust source file is read for the tests inside it; any other file
    // only when it is a test file.
    const inline = isRustSource(name);
    if (!isTestFile(name) && !inline) continue;
    const rust = CARGO_RULES && /\.rs$/.test(name);
    const [assertion, tautology] = rust ? [RUST_ASSERTION, RUST_TAUTOLOGY] : [ASSERTION, TAUTOLOGY];
    const lines = inline ? rustTestLines(f) : { added: addedLines(f.patch), removed: removedLines(f.patch) };
    const added = lines.added;
    const removed = lines.removed.filter((l) => assertion.test(l)).length;
    const kept = added.filter((l) => assertion.test(l)).length;
    const tautologies = added.filter((l) => tautology.test(l)).length;
    if (removed > kept || tautologies > 0) out.push({ file: name, removed, added: kept, tautologies });
  }
  return out;
}

// Read at import, like every configured value: a configuration without these
// keys throws here rather than leaving the lint with nothing to match.
const REPLICATED_RULE = get("fixGuard.replicatedRuleGlobs").map(compileGlob);
const REPLICATED_RULE_NOTICE = get("fixGuard.replicatedRuleNotice");
// Any one of the counterparts: a rule ported from one repository and bound to
// converge with another is visible to a human once either is linked.
const COUNTERPARTS = get("fixGuard.counterpartRepos").map((repo) => counterpartReference(repo));
const NAMES_COUNTERPART = (text) => COUNTERPARTS.some((names) => names(text));

/**
 * A predicate: does this text refer to the repository `repo` (`owner/name`)?
 *
 * A REFERENCE, NOT A SUBSTRING. The name alone is contained in its neighbours'
 * — `yorkie` in `yorkie-js-sdk`, in the `yorkie-team` organisation, in the
 * `@yorkie-js/sdk` package — so a substring test is true of nearly every pull
 * request body in a repository whose counterpart has the shorter name, and the
 * warning it is meant to silence would never be shown. What counts is one of:
 *
 *   owner/name, owner/name#12   the full name, not continued by a name character
 *   name#12                     the short issue form, not preceded by one
 *   github.com/owner/name…      a link to the repository, a pull request or an issue
 *
 * Case is ignored, as GitHub ignores it. An empty `repo` refers to nothing.
 */
export function counterpartReference(repo) {
  if (str(repo) === "") return () => false;
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const [owner, name] = repo.split("/").map(esc);
  // Not followed by more of a name: a word character, a hyphen, or a dot that
  // goes on (`yorkie.js`). A dot that ends a sentence is fine.
  const END = "(?![\\w-]|\\.[\\w-])";
  // Not preceded by more of a name, a path or a package scope.
  const START = "(?<![\\w.@/-])";
  const re = new RegExp(`${START}${owner}/${name}${END}|${START}${name}#\\d+|github\\.com/${owner}/${name}${END}`, "i");
  return (text) => re.test(str(text));
}

/** Non-test changes to a replicated rule (the paths `fixGuard.replicatedRuleGlobs` lists). */
export function crdtRuleChanges(files) {
  return (Array.isArray(files) ? files : [])
    // A commit that changed only the tests inside a Rust rule file changed no rule.
    .filter((f) => !rustTestOnly(f))
    .map((f) => str(f?.filename))
    .filter((n) => REPLICATED_RULE.some((re) => re.test(n)) && !isTestFile(n));
}

/**
 * The whole round, judged. `files` is the union of the round's commits' files
 * (one entry per commit per file — patches are read per commit). `original` is
 * the scope set, or null when it could not be established, in which case scope
 * is NOT enforced (an unknown scope must not page every round) and the record
 * says so.
 */
export function lintRound({ files = [], original = null, prBody = "", unjudged = [] } = {}) {
  const violations = original ? scopeViolations(files, original) : [];
  const reviewerDirected = reviewerDirectedHits(files);
  const weakened = weakenedAssertions(files);
  // Asked only when nothing already points at a counterpart: a PR body that
  // refers to one of them (see `counterpartReference`) has made the
  // cross-repository change visible to a human.
  const named = NAMES_COUNTERPART(prBody);
  const crdt = named ? [] : [...new Set(crdtRuleChanges(files))];
  return {
    scopeKnown: !!original,
    violations,
    reviewerDirected,
    weakened,
    crdt,
    // Present only when there is something in it, so a round of ordinary
    // commits is recorded exactly as it always was.
    ...(unjudged.length > 0 ? { unjudged } : {}),
    blocking: violations.length > 0 || reviewerDirected.length > 0 || unjudged.length > 0,
  };
}

/**
 * How many of a round's commits the report job reads. A round with more is not
 * judged whole, and pages. The same cap as the test-removal record's, which
 * says it read only part of such a round: one number, so the two never
 * disagree about where a round was cut.
 */
export { ROUND_COMMIT_CAP };

/**
 * The round's commits that are NOT ordinary — in the compare, in the pull
 * request's own list, and with some number of parents other than one — as
 * `{ sha, parents }`. `roundCommits` keeps the ordinary ones and these are
 * the rest: they used to be dropped, which is how a change inside a merge
 * commit was read by nothing.
 */
export function unusualCommits(compareCommits, prShas) {
  if (!(prShas instanceof Set)) return [];
  return (Array.isArray(compareCommits) ? compareCommits : [])
    .filter((c) => c && prShas.has(c.sha) && Number(c.n) !== 1)
    .map((c) => ({ sha: str(c.sha), parents: Array.isArray(c.parents) ? c.parents.map(str) : [] }));
}

/**
 * Why a commit that is not ordinary cannot be accepted, or `""` when it is
 * the one kind that can: a merge of the base branch that changed nothing out
 * of scope on its own account.
 *
 *   parents     the commit's parents, first parent first
 *   onBase(p)   is this parent already on the base branch? Asked of every
 *               parent but the first
 *   bothSides() the files the merge left different from BOTH its first and
 *               its second parent, or `null` when that could not be listed
 *   original    the scope set, or `null` when the scope is unknown — the
 *               shape is still judged, the files are not
 */
export function unusualCommitProblem({ parents = [], onBase = () => false, bothSides = () => null, original = null, base = "main" } = {}) {
  if (parents.length === 0) return "has no parent. A fix round's commits continue the branch; a commit with no history can carry anything in under a merge";
  if (parents.length === 1) return "";
  if (parents.length > 2) return `is a merge of ${parents.length} parents`;
  if (!onBase(parents[1])) return `is a merge that brings in commits which are not on ${base}. The only merge a fix round makes is of ${base} itself`;
  const files = bothSides();
  if (!Array.isArray(files)) return `is a merge of ${base} whose own changes could not be listed`;
  if (!original) return "";
  const out = files.filter((f) => !inScope(f, original)).sort();
  if (out.length === 0) return "";
  return `is a merge of ${base} that left files outside the scope different from both of its parents — where a conflict was resolved or something was typed into the merge: ${out.slice(0, 10).map((f) => `\`${safePath(f)}\``).join(", ")}${out.length > 10 ? `, and ${out.length - 10} more` : ""}`;
}

// Said only where the cargo rules are on, so every other repository's page
// and hook message are the words they were.
const RUST_SCOPE_NOTE = CARGO_RULES
  ? " In a Rust file outside that diff, only a top-level `#[cfg(test)]` module may change — its body, or the `#[cfg(test)] mod name;` line that declares a test file — and a commit that changes anything else in the file puts it on this list. A `*_tests.rs` file is in scope only when its parent module declares it under `#[cfg(test)]`, and a manifest, lockfile, `build.rs` or `.cargo/` file only when the pull request already changed it."
  : "";

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
    ...(result.unjudged?.length ? { unjudged: result.unjudged.length } : {}),
  };
  const lines = [];
  if (result.blocking) lines.push(PAGED_LATCH);
  lines.push(`${FIX_GUARD_MARKER}${JSON.stringify(rec).replace(/--/g, "-\\u002d")} -->`);
  if (result.blocking) {
    lines.push("🛑 **The fix round stepped outside what a fix round may change.** A human should look before the loop continues.", "");
  } else {
    lines.push("⚠️ **Fix-round lint** — warnings only; the loop continues.", "");
  }
  if (result.unjudged?.length) {
    lines.push(
      "**Commits the scope check could not judge.** It reads the round's ordinary commits, one parent each. A change that arrives any other way — in a merge that is not of the base branch, in a commit with no parent, past the number of commits it reads, or in a round it could not list — was read by nothing, so it is treated as out of scope rather than as fine:",
      ...result.unjudged.slice(0, 20).map((u) => `- ${u.sha ? `\`${safePath(u.sha).slice(0, 12)}\` ` : ""}${q(u.problem).slice(0, 600)}`),
      "",
    );
  }
  if (result.violations.length) {
    lines.push(
      `**Files outside the PR's scope.** A fix round may change the files in the PR's original diff, plus tests and docs.${RUST_SCOPE_NOTE} A finding that needs any other file is not this PR's to fix — it should have been disputed or skipped, and the panel files it as a follow-up when it is out of the diff:`,
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
      REPLICATED_RULE_NOTICE,
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

/** A file's text at a commit, through the contents API, or `null` when it cannot be read. */
function readAtRevision(rev, file) {
  try {
    const where = file.split("/").map(encodeURIComponent).join("/");
    return gh(["api", "-H", "Accept: application/vnd.github.raw+json", `repos/{owner}/{repo}/contents/${where}?ref=${encodeURIComponent(rev)}`]);
  } catch (err) {
    console.error(`fix-guard: could not read ${safePath(file)} at ${str(rev).slice(0, 12)} (${err.message.split("\n")[0]}).`);
    return null;
  }
}

/** A commit's first parent, or "" when it cannot be read. */
function readParent(sha) {
  try {
    const parent = str(ghJson(["api", `repos/{owner}/{repo}/commits/${sha}`, "--jq", "{parent: .parents[0].sha}"]).parent);
    return /^[0-9a-f]{40}$/i.test(parent) ? parent : "";
  } catch (err) {
    console.error(`fix-guard: could not read the parent of ${sha.slice(0, 12)} (${err.message.split("\n")[0]}).`);
    return "";
  }
}

/** The compare API lists at most 300 files; at the cap the set is incomplete, so unknown. */
export const COMPARE_FILE_CAP = 300;

/**
 * The PR's original file set: `base...<frozen>`, frozen at the first fix
 * dispatch. Null when it cannot be established — no dispatch yet and no
 * fallback head, an API failure, or a capped list.
 */
/**
 * The commit the scope is measured to: the first fix dispatch's head when the
 * ledger has one, else the head this round started from. No dispatch on record
 * means this is the first round (the head IS the original diff), or a fixer
 * that keeps no ledger — the CI-fix arm and `@claude fix` — where the round's
 * starting head is the narrowest honest answer. Without the fallback those two
 * fixers' report checks ran with the scope switched off.
 */
export function scopeAnchor(comments, fallbackHead = "") {
  return frozenShaFrom(comments) || (/^[0-9a-f]{40}$/i.test(str(fallbackHead)) ? str(fallbackHead) : "");
}

export function readOriginalFiles(pr, { base = "main", fallbackHead = "" } = {}) {
  try {
    const comments = ghJson(["api", "--paginate", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]);
    const frozen = scopeAnchor(comments, fallbackHead);
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
/**
 * `exempt(file)`: is this out-of-scope path fine all the same? Asked only of
 * paths the scope refuses. The cargo rules answer it for a Rust file whose
 * pushed commits changed nothing but its tests (`rustExemption`).
 */
export function prepushVerdict({ touched = [], allowed = null, exempt = () => false } = {}) {
  if (!allowed || allowed.size === 0) return { ok: true, violations: [] };
  const violations = [...new Set(touched.filter((f) => !inScope(f, allowed)))].filter((f) => !exempt(f)).sort();
  return { ok: violations.length === 0, violations };
}

/**
 * The hook's answer for one Rust source file: did EVERY pushed commit that
 * touched it change its test regions only? `commits` are those commits' ids,
 * and `read(rev, file)` returns the file at a revision or `null`. A file no
 * commit is found for, or a side that cannot be read (the file was added,
 * deleted or renamed in that commit), is not exempt.
 */
export function rustExemption(file, commits, read) {
  if (!isRustSource(file) || !Array.isArray(commits) || commits.length === 0) return false;
  return commits.every((sha) => testOnlyChange(read(`${sha}^`, file), read(sha, file)));
}

/** How many Rust files a report job reads both sides of, per round. Past it, a file is judged unread. */
export const RUST_READ_CAP = 80;

/**
 * Put `declared: true|false` on each entry for a unit-test file named by
 * convention: is it declared as a test module at the round's head?
 * `read(path)` returns a file's text there or `null`. One answer per path,
 * however many commits touched it; the budget is the round's.
 */
export function attachRustDeclarations(files, { read, budget }) {
  const known = new Map();
  for (const f of Array.isArray(files) ? files : []) {
    const name = str(f?.filename);
    if (!isCargoUnitTestByName(name)) continue;
    if (!known.has(name)) {
      if (budget.left <= 0) continue;
      budget.left--;
      known.set(name, rustDeclared(name, read));
    }
    f.declared = known.get(name);
  }
  return files;
}

/**
 * Put `contents: { before, after }` on each entry of one commit's file list
 * that needs it: a MODIFIED Rust source file (not a test file by its path —
 * those are in scope whole). `read(rev, file)` returns the file's text at a
 * revision or `null`; `parent()` returns the commit's parent, and is asked
 * for only when there is something to read. `budget` is `{ left }`, shared
 * across the round. Returns the list, for chaining.
 */
export function attachRustContents(files, { sha, parent, read, budget }) {
  let base;
  for (const f of Array.isArray(files) ? files : []) {
    if (!isRustSource(f?.filename) || f?.status !== "modified") continue;
    if (budget.left <= 0) break;
    budget.left--;
    base ??= parent();
    f.contents = { before: base ? read(base, f.filename) : null, after: read(sha, f.filename) };
  }
  return files;
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
  // The commits the listing above does not walk into: merges and commits
  // with no parent. See "WHICH COMMITS ARE JUDGED" at the top.
  const unjudged = [];
  const gitOk = (args) => {
    try {
      execFileSync("git", args, { stdio: "ignore" });
      return true;
    } catch {
      return false;
    }
  };
  const names = (args) => execFileSync("git", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 }).split("\n").map((s) => s.trim()).filter(Boolean);
  for (const line of stdin.split("\n")) {
    const [, localSha, , remoteSha] = line.trim().split(/\s+/);
    const args = pushedLogArgs({ localSha, remoteSha, baseRef });
    if (!args) continue;
    let commits;
    try {
      commits = names(args.filter((a) => a !== "--name-only" && a !== "--no-merges").map((a) => (a === "--format=" ? "--format=%H %P" : a)));
    } catch {
      continue; // Cannot list → do not block; the report job's check is the gate.
    }
    for (const entry of commits) {
      const [sha, ...parents] = entry.split(/\s+/);
      if (parents.length === 1) continue;
      const problem = unusualCommitProblem({
        parents,
        base: `origin/${baseName}`,
        original: allowed,
        onBase: (p) => baseRef !== "" && gitOk(["merge-base", "--is-ancestor", p, baseRef]),
        bothSides: () => {
          try {
            const second = new Set(names(["diff", "--name-only", "--no-renames", parents[1], sha]));
            return names(["diff", "--name-only", "--no-renames", parents[0], sha]).filter((f) => second.has(f));
          } catch {
            return null;
          }
        },
      });
      if (problem) unjudged.push({ sha, problem });
    }
  }
  if (unjudged.length > 0) {
    console.error(
      [
        "",
        "fix-guard: PUSH REFUSED — this push holds commits a fix round does not make:",
        ...unjudged.map((u) => `  ${u.sha.slice(0, 12)} ${u.problem.replace(/`/g, "")}`),
        "",
        "A fix round adds ordinary commits to the end of the branch. It may merge",
        `origin/${baseName} to resolve a conflict, and that merge may change nothing outside the`,
        "scope on its own account. No other merge, and no commit without a parent: the",
        "scope check reads ordinary commits, so a change that arrives any other way is",
        "read by nothing. Make the same change as ordinary commits on top of the",
        "branch's pushed head and push that. Do not bypass this hook: the report job",
        "checks the same rule and pages a human if it is broken.",
        "",
      ].join("\n"),
    );
    process.exit(1);
  }
  // The cargo rules: a Rust file outside the scope may still have its tests
  // changed. Each pushed commit that touched it is read on both sides.
  const show = (rev, path_) => {
    try {
      return execFileSync("git", ["show", `${rev}:${path_}`], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, stdio: ["ignore", "pipe", "ignore"] });
    } catch {
      return null;
    }
  };
  const heads = stdin.split("\n").map((line) => line.trim().split(/\s+/)[1]).filter((sha) => /^[0-9a-f]{40}$/.test(str(sha)) && !/^0{40}$/.test(sha));
  const exempt = (file) => {
    if (!CARGO_RULES) return false;
    // A unit-test file named by convention: test code if a parent module at
    // the head being pushed declares it under `#[cfg(test)]`.
    if (isCargoUnitTestByName(file)) return heads.length > 0 && heads.every((head) => show(head, file) !== null && rustDeclared(file, (parent) => show(head, parent)));
    const commits = [];
    for (const line of stdin.split("\n")) {
      const [, localSha, , remoteSha] = line.trim().split(/\s+/);
      const args = pushedLogArgs({ localSha, remoteSha, baseRef });
      if (!args) continue;
      try {
        const own = args.filter((a) => a !== "--name-only").map((a) => (a === "--format=" ? "--format=%H" : a));
        commits.push(...execFileSync("git", [...own, "--", file], { encoding: "utf8" }).split("\n").map((s) => s.trim()).filter(Boolean));
      } catch {
        return false;
      }
    }
    return rustExemption(file, commits, show);
  };
  const v = prepushVerdict({ touched, allowed, exempt });
  if (v.ok) return;
  console.error(
    [
      "",
      "fix-guard: PUSH REFUSED — these files are outside what a fix round may change:",
      ...v.violations.map((f) => `  ${f}`),
      "",
      "A fix round may change the files in the PR's original diff, plus tests and docs.",
      ...(CARGO_RULES
        ? [
          "In a Rust file outside that diff you may change ONLY a top-level `#[cfg(test)] mod`:",
          "its body, a new one at the end of the file, or the `#[cfg(test)] mod name;` line",
          "that declares a new `name.rs` test file (name it `*_tests.rs`). Nothing else in",
          "the file, in any commit of this push: squash a commit that did more. A",
          "`*_tests.rs` file is in scope only when its parent module declares it with",
          "`#[cfg(test)] mod name;` in this push or before it, and never under `src/bin/`,",
          "`examples/` or `benches/`. A `Cargo.toml`, `Cargo.lock`, `build.rs` or `.cargo/`",
          "file is in scope only if the pull request already changed it.",
        ]
        : []),
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
  let unusual = [];
  const unjudged = [];
  const base = flag(argv, "base") || "main";
  try {
    const inRound = ghLines(["api", "--paginate", `repos/{owner}/{repo}/compare/${before}...${after}?per_page=100`, "--jq", ".commits[] | {sha, n: (.parents | length), parents: [.parents[].sha]}"]);
    const prShas = new Set(ghLines(["api", "--paginate", `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`, "--jq", ".[].sha | tojson"]));
    const ordinary = roundCommits(inRound, prShas);
    unusual = unusualCommits(inRound, prShas);
    if (ordinary.length > ROUND_COMMIT_CAP) {
      unjudged.push({ sha: "", problem: `the round has ${ordinary.length} commits and only the first ${ROUND_COMMIT_CAP} are read` });
    }
    const budget = { left: RUST_READ_CAP };
    for (const { sha } of ordinary.slice(0, ROUND_COMMIT_CAP)) {
      const own = ghLines(["api", "--paginate", `repos/{owner}/{repo}/commits/${sha}?per_page=100`, "--jq", ".files[]"]);
      // The cargo rules judge a Rust source file by its contents, not its
      // patch. Read as data, through the API: nothing is checked out or run.
      if (CARGO_RULES) attachRustContents(own, { sha, parent: () => readParent(sha), read: readAtRevision, budget });
      files.push(...own);
    }
    // A unit-test file is one because its parent module says so. Read at
    // the round's head: the declaration may come in a later commit than the
    // file, and it is the head that gets merged.
    if (CARGO_RULES) attachRustDeclarations(files, { read: (file) => readAtRevision(after, file), budget });
    prBody = str(ghJson(["api", `repos/{owner}/{repo}/pulls/${pr}`, "--jq", "{body}"]).body);
  } catch (err) {
    // AN UNREAD ROUND IS NOT A ROUND THAT STAYED IN SCOPE. This used to say
    // `blocking=false` and nothing else, so failing this read was a way past
    // the gate. It pages, and says it judged nothing.
    console.error(`fix-guard: could not read the round's commits (${err.message}).`);
    files = [];
    unusual = [];
    unjudged.length = 0;
    unjudged.push({ sha: "", problem: "the round's commits could not be read, so nothing in it was checked" });
  }
  // `before` is validated above; it is the round's starting head, so a PR with
  // no dispatch ledger still gets its scope checked.
  const original = readOriginalFiles(pr, { base, fallbackHead: before });
  for (const { sha, parents } of unusual) {
    const problem = unusualCommitProblem({
      parents,
      base,
      original,
      // On the base branch: the base has nothing to gain from it.
      onBase: (p) => {
        try {
          return Number(ghJson(["api", `repos/{owner}/{repo}/compare/${encodeURIComponent(base)}...${p}`, "--jq", "{ahead: .ahead_by}"]).ahead) === 0;
        } catch {
          return false;
        }
      },
      // The merge against its first parent is what the base brought in, and
      // can be long; against its second it is the branch's own diff, which
      // the compare API caps. At the cap the list is incomplete: unknown.
      bothSides: () => {
        try {
          const first = ghLines(["api", "--paginate", `repos/{owner}/{repo}/commits/${sha}?per_page=100`, "--jq", ".files[].filename | tojson"]);
          const second = ghJson(["api", `repos/{owner}/{repo}/compare/${parents[1]}...${sha}?per_page=100`, "--jq", "[.files[].filename]"]);
          if (!Array.isArray(second) || second.length >= COMPARE_FILE_CAP) return null;
          const set = new Set(second);
          return first.filter((f) => set.has(f));
        } catch {
          return null;
        }
      },
    });
    if (problem) unjudged.push({ sha, problem });
  }
  const result = lintRound({ files, original, prBody, unjudged });
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
