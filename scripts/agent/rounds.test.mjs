import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  isSingleParentCommit,
  countFailedReviewRounds,
  summaryTokens,
  findingSimilarity,
  repeatRatio,
  groupReviewRounds,
  detectStalledRounds,
  PAGED_LATCH,
  PAGE_AUTHOR_LOGINS,
  isPagedLatchComment,
  rerunPointFrom,
  isRerunCommand,
  fixAttemptCommits,
  FIX_DISPATCH_MARKER,
  FIX_DISPATCH_AUTHOR_LOGIN,
  serializeFixDispatch,
  renderFixDispatchComment,
  parseFixDispatchComment,
  collectFixDispatches,
  fixRoundsUsed,
} from "./rounds.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PANEL_WORKFLOW = readFileSync(
  path.join(HERE, "..", "..", ".github", "workflows", "agent-review-panel.yml"),
  "utf8",
);

// --- the paged latch, shared between a module and a github-script step -------
//
// The latch is the pipeline's stop condition and has TWO readers:
// review-round-guard.mjs (stops the fixer) and the `gate` job (stops the panel).
// `gate` does no checkout, so it cannot import PAGED_LATCH and carries a literal
// copy. A drifted copy does not error — it just silently stops latching, so the
// panel would keep re-reviewing a PR that was handed to a human. Hence a test.

test("PAGED_LATCH is the exact marker, pinned as a literal", () => {
  // Deliberately not derived from the module: the point is that changing the
  // marker must break a test rather than quietly un-latch every open PR.
  assert.equal(PAGED_LATCH, "<!-- agent-review-paged -->");
});

test("the gate job's literal copy of the latch matches the module", () => {
  assert.ok(
    PANEL_WORKFLOW.includes(`const PAGED_LATCH = '${PAGED_LATCH}';`),
    "agent-review-panel.yml's gate job must carry a byte-identical copy of PAGED_LATCH",
  );
  // The author allow-list is half the rule; a copy that drifted to a smaller set
  // would silently stop honouring a real page, and a larger one re-opens the
  // spoof this check exists to close.
  for (const login of PAGE_AUTHOR_LOGINS) {
    assert.ok(PANEL_WORKFLOW.includes(`'${login}'`), `gate job must trust ${login}`);
  }
  assert.ok(
    PANEL_WORKFLOW.includes("const isPagedLatchComment = (c) =>"),
    "gate job must author-check the latch, not match the marker alone",
  );
});

// --- who may write the latch ------------------------------------------------
//
// wafflebase/wafflebase is PUBLIC. Matching on the marker alone lets any GitHub
// account post it and permanently stop both the fix loop and the review panel,
// which is denial of review from an unauthenticated position.

test("isPagedLatchComment: an untrusted author cannot latch the PR", () => {
  const body = `${PAGED_LATCH}\n🛑 nice pipeline you have there`;
  const untrusted = [
    { body, user: { login: "drive-by", type: "User" }, author_association: "NONE" },
    { body, user: { login: "drive-by", type: "User" }, author_association: "CONTRIBUTOR" },
    { body, user: { login: "drive-by", type: "User" }, author_association: "FIRST_TIME_CONTRIBUTOR" },
    // A human cannot register a '[bot]' login, but pin that the TYPE is checked
    // too rather than the name alone.
    { body, user: { login: "github-actions[bot]", type: "User" }, author_association: "NONE" },
    // An unrelated App is a Bot, but not one of ours.
    { body, user: { login: "coderabbitai[bot]", type: "Bot" }, author_association: "CONTRIBUTOR" },
  ];
  for (const c of untrusted) {
    assert.equal(isPagedLatchComment(c), false, JSON.stringify(c.user) + " " + c.author_association);
  }
});

test("isPagedLatchComment: the real writers, and a maintainer by hand, do latch", () => {
  const body = `${PAGED_LATCH}\n🛑 paged`;
  // Both observed in production: the guard and `stalled` comment as
  // github-actions[bot]; the fix job's branch-head page uses the App token.
  for (const login of PAGE_AUTHOR_LOGINS) {
    assert.equal(
      isPagedLatchComment({ body, user: { login, type: "Bot" }, author_association: "CONTRIBUTOR" }),
      true,
      login,
    );
  }
  // Halting the pipeline by hand stays supported for anyone with write access.
  for (const assoc of ["OWNER", "MEMBER", "COLLABORATOR"]) {
    assert.equal(
      isPagedLatchComment({ body, user: { login: "a-maintainer", type: "User" }, author_association: assoc }),
      true,
      assoc,
    );
  }
});

test("isPagedLatchComment: no marker, or junk, is never a latch", () => {
  const trusted = { user: { login: PAGE_AUTHOR_LOGINS[0], type: "Bot" }, author_association: "OWNER" };
  assert.equal(isPagedLatchComment({ ...trusted, body: "just a normal comment" }), false);
  assert.equal(isPagedLatchComment({ ...trusted, body: "" }), false);
  assert.equal(isPagedLatchComment({ ...trusted }), false, "absent body");
  for (const junk of [null, undefined, 42, "string", [], { body: null }, { body: PAGED_LATCH }]) {
    assert.equal(isPagedLatchComment(junk), false, JSON.stringify(junk));
  }
});

test("every site that writes the latch also says the panel has stopped", () => {
  // Writing the latch now freezes the agent-review-* checks, so a page that
  // does not say so leaves a human waiting for verdicts that will never come.
  // Three writers today: the guard's page() (in review-round-guard.mjs), the
  // fix job's branch-head-did-not-advance path, and the stalled job.
  const HANDOFF = "The review panel will not run again on this PR";
  const guard = readFileSync(path.join(HERE, "review-round-guard.mjs"), "utf8");
  assert.ok(guard.includes(HANDOFF), "review-round-guard.mjs page() must carry the handoff note");

  // In the workflow, count latch WRITES (the marker emitted into a comment body)
  // and require as many handoff sentences. Two kinds of line mention the marker
  // without writing it and must not be counted: the gate job's read-side
  // `const PAGED_LATCH =`, and prose — both `#` YAML comments and `//` JS
  // comments, since the rationale above the gate check quotes the marker.
  const isProse = (l) => /^\s*(#|\/\/)/.test(l);
  const writes = PANEL_WORKFLOW.split("\n").filter(
    (l) => l.includes(PAGED_LATCH) && !l.includes("const PAGED_LATCH") && !isProse(l),
  ).length;
  const notes = PANEL_WORKFLOW.split(HANDOFF).length - 1;
  assert.ok(writes > 0, "expected the workflow to write the latch somewhere");
  assert.equal(notes, writes, `each of the ${writes} latch write(s) needs a handoff note`);
});

const fixer = (sha, checkRuns = []) => ({ sha, parents: [{ sha: "p1" }], checkRuns });
const merge = (sha, checkRuns = []) => ({ sha, parents: [{ sha: "p1" }, { sha: "p2" }], checkRuns });
const failingRun = (name = "agent-review-correctness") => ({ name, app: { slug: "github-actions" }, conclusion: "failure" });
const passingRun = (name = "agent-review-correctness") => ({ name, app: { slug: "github-actions" }, conclusion: "success" });

test("isSingleParentCommit: one parent → true; merge (2) or root (0) → false", () => {
  assert.equal(isSingleParentCommit({ parents: [{ sha: "a" }] }), true);
  assert.equal(isSingleParentCommit({ parents: [{ sha: "a" }, { sha: "b" }] }), false);
  assert.equal(isSingleParentCommit({ parents: [] }), false);
  assert.equal(isSingleParentCommit({}), false);
  assert.equal(isSingleParentCommit(undefined), false);
});

test("countFailedReviewRounds: PR #521 shape — 3 fixer + 3 merge, same failing check → 3, not 6", () => {
  const req = ["agent-review-correctness"];
  const commits = [
    fixer("f1", [failingRun()]), fixer("f2", [failingRun()]), fixer("f3", [failingRun()]),
    merge("m1", [failingRun()]), merge("m2", [failingRun()]), merge("m3", [failingRun()]),
  ];
  assert.equal(countFailedReviewRounds(commits, req), 3);
});

test("countFailedReviewRounds: empty commits → 0", () => {
  assert.equal(countFailedReviewRounds([], ["agent-review-correctness"]), 0);
});

test("countFailedReviewRounds: fixer commit with only a passing check → not counted", () => {
  assert.equal(countFailedReviewRounds([fixer("f1", [passingRun()])], ["agent-review-correctness"]), 0);
});

test("countFailedReviewRounds: check-run name not in requiredCheckNames → not counted", () => {
  assert.equal(countFailedReviewRounds([fixer("f1", [failingRun("some-other-check")])], ["agent-review-correctness"]), 0);
});

test("countFailedReviewRounds: failing check-run from a different app.slug → not counted (regression guard)", () => {
  const commits = [fixer("f1", [{ name: "agent-review-correctness", app: { slug: "some-other-app" }, conclusion: "failure" }])];
  assert.equal(countFailedReviewRounds(commits, ["agent-review-correctness"]), 0);
});

// --- convergence detection ---------------------------------------------------

// The four summaries below are VERBATIM from PR #564's design-fit lens, round 2:
// one defect, four wordings, same file. An exact-text key (what dedupeFindings
// uses) saw four distinct findings; that is why this path needs fuzzy matching.
const D564 = [
  "Deliverables 2 and 3 are not actually implemented: the changes to agent-review-panel.yml, agent-iterate-ci.yml, and agent-implement.yml live only as text inside a committed .patch file under docs/tasks/active/, and are never applied to the real workflow files.",
  "Deliverables 2 & 3 are not actually applied — the workflow changes live only in an unapplied .patch file, so the pipeline gets none of the fixer-checklist or exploration-focus behavior the issue requires.",
  "Deliverables 2 and 3 are not actually implemented: the workflow prompt changes live only as inert text inside docs/tasks/active/20260727-scope-lenses-tighten-fixer-workflow-prompts.patch, and were never applied to the real .github/workflows/*.yml files.",
  "Deliverables 2 & 3 are not actually implemented — the fixer/implement prompt changes exist only as an inert .patch file committed under docs/, not applied to the workflow YAMLs. The issue requires edits to agent-review-panel.yml, agent-iterate-ci.yml, and agent-implement.yml; those files are unchanged",
];
const PATCH_FILE = "docs/tasks/active/20260727-scope-lenses-tighten-fixer-workflow-prompts.patch";
const f = (lens, file, summary) => ({ lens, file, summary, severity: "major" });
const round = (findings, partial = false, sha = "s") => ({ sha, findings, partial });

test("summaryTokens: splits identifiers, drops stopwords/short tokens, sorted + deduped", () => {
  // camelCase splits (readOnly → read+only, EditorAPI → editor+api); "on" is
  // too short and "only" is a stopword, so both drop out.
  assert.deepEqual(summaryTokens("readOnly guard on EditorAPI.paste()"), [
    "api", "editor", "guard", "paste", "read",
  ]);
  // snake_case, paths and dots all split; "the"/"is" dropped; "ts" too short
  assert.deepEqual(
    summaryTokens("the cache_read_input_tokens in a/b/file.ts is wrong"),
    ["cache", "file", "input", "read", "tokens", "wrong"].sort(),
  );
  assert.deepEqual(summaryTokens(""), []);
  assert.deepEqual(summaryTokens(null), []);
  assert.deepEqual(summaryTokens("!!! ... ???"), []); // punctuation only
  assert.deepEqual(summaryTokens("Bug X"), summaryTokens("bug x")); // case-insensitive
});

test("findingSimilarity: the real #564 rephrasings all match; lens/file gate to 0", () => {
  // Every pair of the four REAL wordings must clear the calibrated 0.3 threshold.
  for (let i = 0; i < D564.length; i++) {
    for (let j = i + 1; j < D564.length; j++) {
      const s = findingSimilarity(f("design-fit", PATCH_FILE, D564[i]), f("design-fit", PATCH_FILE, D564[j]));
      assert.ok(s >= 0.3, `#564 wordings ${i}/${j} scored ${s.toFixed(2)}, expected >= 0.3`);
    }
  }
  // Same text, DIFFERENT lens or DIFFERENT file → 0. This gate is what stops
  // three distinct bugs in one file (fixed one per round) reading as a repeat.
  assert.equal(findingSimilarity(f("design-fit", "a.ts", D564[0]), f("security", "a.ts", D564[0])), 0);
  assert.equal(findingSimilarity(f("design-fit", "a.ts", D564[0]), f("design-fit", "b.ts", D564[0])), 0);
  // Unrelated findings in the same file stay well below threshold.
  assert.ok(findingSimilarity(f("c", "a.ts", "off-by-one in the row loop"), f("c", "a.ts", D564[0])) < 0.3);
  // Degenerate tokens (coerceFindings placeholder) fall back to exact equality.
  const ph = "(malformed finding — treated as blocking)";
  assert.equal(findingSimilarity(f("c", "a.ts", ph), f("c", "a.ts", ph)), 1);
  assert.equal(findingSimilarity(f("c", "a.ts", "!!!"), f("c", "a.ts", "???")), 0);
  assert.equal(findingSimilarity(null, f("c", "a.ts", "x")), 0);
});

test("repeatRatio: many-to-one — N wordings of one prior are all repeats", () => {
  const a = f("c", "a.ts", "MIN over an all-blank range returns #NUM!");
  const b = f("c", "b.ts", "unrelated null deref in the header path");
  assert.equal(repeatRatio([a], [a]), 1);
  // The duplication case the panel actually produces: one prior, two wordings
  // of it this round. All of this round is recycled, so 1.0 — a one-to-one
  // pairing would score 0.5 and miss exactly when the loop is most stuck.
  assert.equal(repeatRatio([a], [a, a]), 1);
  const rephrased = f("c", PATCH_FILE, D564[1]);
  assert.equal(repeatRatio([f("c", PATCH_FILE, D564[0])], [rephrased, rephrased]), 1);
  // A genuinely new finding alongside a repeat still drags the ratio down.
  assert.equal(repeatRatio([a], [a, b]), 0.5);
  assert.equal(repeatRatio([a], [b]), 0);
  assert.equal(repeatRatio([], [a]), 0);
  assert.equal(repeatRatio([a], []), 0); // clean round is never a repeat
  assert.equal(repeatRatio(null, null), 0);
});

test("detectStalledRounds: the #521 shape — flat count, same findings → stalled", () => {
  const fs = [f("design-fit", PATCH_FILE, D564[0]), f("security", "lenses.json", "security scoped away from root files")];
  const r = detectStalledRounds([round(fs), round(fs), round(fs)]);
  assert.equal(r.stalled, true);
  assert.equal(r.stalls, 2);
  assert.equal(r.reason, "repeat-without-reduction");
  assert.ok(r.repeated.length > 0, "must name the findings it is paging about");
});

// THE most important negative. Carry-forward re-merges unresolved priors every
// round BY DESIGN, so a healthy PR shows near-total overlap. Keying on overlap
// alone would page on essentially every PR; only the count test saves us.
test("detectStalledRounds: shrinking 3→2→1 at overlap 1.0 is PROGRESS, not a stall", () => {
  const [x, y, z] = [f("c", "a.ts", D564[0]), f("c", "b.ts", D564[1]), f("c", "c.ts", D564[2])];
  const r = detectStalledRounds([round([x, y, z]), round([x, y]), round([x])]);
  assert.equal(r.stalled, false);
  assert.equal(r.reason, "progressing");
});

test("detectStalledRounds: rising count with high overlap → stalled (getting worse counts too)", () => {
  const x = f("design-fit", PATCH_FILE, D564[0]);
  const r = detectStalledRounds([
    round([x]),
    round([x, f("design-fit", PATCH_FILE, D564[1])]),
    round([x, f("design-fit", PATCH_FILE, D564[2]), f("design-fit", PATCH_FILE, D564[3])]),
  ]);
  assert.equal(r.stalled, true);
});

test("detectStalledRounds: flat count but DISJOINT findings → not a stall", () => {
  const mk = (n) => [f("c", `${n}.ts`, `a completely distinct defect number ${n} in the parser`)];
  const r = detectStalledRounds([round(mk(1)), round(mk(2)), round(mk(3))]);
  assert.equal(r.stalled, false);
  assert.equal(r.reason, "progressing");
});

test("detectStalledRounds: a partial round breaks the trailing run (unreadable never pages)", () => {
  const fs = [f("design-fit", PATCH_FILE, D564[0])];
  assert.equal(detectStalledRounds([round(fs), round(fs, true), round(fs)]).stalled, false);
  // partial as the newest round also breaks it
  assert.equal(detectStalledRounds([round(fs), round(fs), round(fs, true)]).stalled, false);
});

test("detectStalledRounds: needs minRepeats+1 rounds; one stalling pair is not enough", () => {
  const fs = [f("design-fit", PATCH_FILE, D564[0])];
  assert.equal(detectStalledRounds([round(fs), round(fs)]).reason, "too-few-rounds");
  assert.equal(detectStalledRounds([round(fs)]).stalled, false);
  // 3 rounds where only the newest pair repeats → 1 stall, below the threshold
  const other = [f("c", "z.ts", "an entirely different problem in another module")];
  const r = detectStalledRounds([round(other), round(fs), round(fs)]);
  assert.equal(r.stalls, 1);
  assert.equal(r.stalled, false);
  assert.equal(r.reason, "partial-stall");
});

test("detectStalledRounds: junk input → false, never throws", () => {
  for (const junk of [null, undefined, [], "nope", [null, 3, "x"], [{}, {}, {}]]) {
    assert.doesNotThrow(() => detectStalledRounds(junk));
    assert.equal(detectStalledRounds(junk).stalled, false);
  }
  // findings not an array on every round → counts as 0, ratio 0 → no stall
  assert.equal(detectStalledRounds([{ findings: "x" }, { findings: "x" }, { findings: "x" }]).stalled, false);
});

test("detectStalledRounds: two clean rounds never stall (empty curr → ratio 0)", () => {
  assert.equal(detectStalledRounds([round([]), round([]), round([])]).stalled, false);
});

// --- groupReviewRounds -------------------------------------------------------

const NAMES = ["agent-review-correctness", "agent-review-design-fit"];
const run = (name, findings, over = {}) => ({
  name, app: { slug: "github-actions" }, conclusion: "failure",
  completed_at: "2026-07-27T06:00:00Z",
  output: { summary: "s", text: JSON.stringify(findings) }, ...over,
});
const commit = (sha, checkRuns) => ({ sha, parents: [{ sha: "p" }], checkRuns });

test("groupReviewRounds: commit order preserved; non-panel commits are not rounds", () => {
  const one = [{ severity: "major", file: "a.ts", summary: "x" }];
  const rounds = groupReviewRounds(
    [commit("c1", [run("agent-review-correctness", one)]), commit("c2", []), commit("c3", [run("agent-review-correctness", one)])],
    NAMES,
  );
  assert.deepEqual(rounds.map((r) => r.sha), ["c1", "c3"]);
  assert.equal(rounds[0].findings[0].lens, "correctness"); // prefix stripped
});

test("groupReviewRounds: unreadable payloads mark the round partial, not clean", () => {
  const bad = (over) => groupReviewRounds([commit("c1", [{ name: "agent-review-correctness", app: { slug: "github-actions" }, ...over }])], NAMES)[0];
  assert.equal(bad({ output: { text: "not json" } }).partial, true);
  assert.equal(bad({ output: { text: '{"not":"an array"}' } }).partial, true);
  assert.equal(bad({ output: {} }).partial, true); // ABSENT text ≠ "found nothing"
  assert.equal(bad({ output: { text: "[]" } }).partial, false); // "[]" IS a clean verdict
  assert.deepEqual(bad({ output: { text: "[]" } }).findings, []);
});

test("groupReviewRounds: infra/quota findings mark the round partial (not a repeat)", () => {
  const infra = [{ severity: "major", summary: "Review could not run — Claude API/quota error (429): limit" }];
  const r = groupReviewRounds([commit("c1", [run("agent-review-correctness", infra)])], NAMES)[0];
  assert.equal(r.partial, true);
  assert.equal(r.findings.length, 0);
  const noVerdict = [{ severity: "major", summary: "Reviewer did not produce a valid verdict: boom" }];
  assert.equal(groupReviewRounds([commit("c1", [run("agent-review-correctness", noVerdict)])], NAMES)[0].partial, true);
});

test("groupReviewRounds: other-app check ignored; newest run per lens wins", () => {
  const one = [{ severity: "major", file: "a.ts", summary: "x" }];
  const foreign = { name: "agent-review-correctness", app: { slug: "some-other-app" }, output: { text: JSON.stringify(one) } };
  assert.deepEqual(groupReviewRounds([commit("c1", [foreign])], NAMES), []);
  // two runs of one lens on one commit → the later completed_at wins
  const older = run("agent-review-correctness", [{ severity: "major", file: "a.ts", summary: "OLD" }], { completed_at: "2026-07-27T05:00:00Z" });
  const newer = run("agent-review-correctness", [{ severity: "major", file: "a.ts", summary: "NEW" }], { completed_at: "2026-07-27T07:00:00Z" });
  assert.deepEqual(groupReviewRounds([commit("c1", [older, newer])], NAMES)[0].findings.map((x) => x.summary), ["NEW"]);
});

// --- the rerun resume point --------------------------------------------------

const human = (body, over = {}) => ({
  body, created_at: "2026-08-04T09:30:00Z",
  user: { login: "harrykim8672", type: "User" }, author_association: "MEMBER", ...over,
});

test("rerunPointFrom: a maintainer's @claude rerun moves the floor", () => {
  assert.equal(rerunPointFrom([]), null);
  assert.equal(rerunPointFrom([human("just a comment")]), null);
  assert.equal(rerunPointFrom([human("@claude rerun")]), "2026-08-04T09:30:00.000Z");
  assert.equal(
    rerunPointFrom([human("@claude rerun"), human("@claude rerun", { created_at: "2026-08-04T14:00:00Z" })]),
    "2026-08-04T14:00:00.000Z",
  );
  assert.equal(rerunPointFrom([human("@claude rerun", { created_at: "nonsense" })]), null);
  for (const bad of [null, undefined, "x", 7]) assert.equal(rerunPointFrom(bad), null);
});

test("isRerunCommand: A BOT CAN NEVER RESET ITS OWN BOUND", () => {
  // The reason this reads the maintainer's COMMAND and not the workflow's result
  // marker. agent-rerun.yml posts with the App token, so the trusted identity would
  // be yorkie-agent[bot] — the same identity the fixer and implementer post their
  // own free-form comments under (the self-review comment on every agent PR). A
  // marker keyed on bot login let the party bounded by MAX_REVIEW_ROUNDS grant
  // itself unlimited attempts, by accident or by injection from the diff it reads.
  //
  // Structural, not a secret: no App can present as a non-Bot.
  for (const login of ["yorkie-agent[bot]", "github-actions[bot]", "coderabbitai[bot]"]) {
    for (const assoc of ["OWNER", "MEMBER", "COLLABORATOR", "CONTRIBUTOR"]) {
      assert.equal(
        isRerunCommand(human("@claude rerun", { user: { login, type: "Bot" }, author_association: assoc })),
        false,
        `${login}/${assoc}`,
      );
    }
  }
  assert.equal(rerunPointFrom([human("@claude rerun", { user: { login: "yorkie-agent[bot]", type: "Bot" } })]), null);
});

test("isRerunCommand: a stranger cannot reset it either; junk never throws", () => {
  for (const assoc of ["NONE", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR"]) {
    assert.equal(isRerunCommand(human("@claude rerun", { author_association: assoc })), false, assoc);
  }
  for (const assoc of ["OWNER", "MEMBER", "COLLABORATOR"]) {
    assert.equal(isRerunCommand(human("@claude rerun", { author_association: assoc })), true, assoc);
  }
  for (const bad of [null, undefined, "x", 7, {}]) assert.equal(isRerunCommand(bad), false);
});

test("isRerunCommand: it is parseCommand's verb, so the router cannot disagree", () => {
  // A second regex here could recognise a rerun agent-rerun.yml's router did not,
  // moving the floor for a hand-back that never happened.
  assert.equal(isRerunCommand(human("@claude rerun")), true);
  assert.equal(isRerunCommand(human("@claude rerun this please")), true);
  assert.equal(isRerunCommand(human("@claude loop")), false);
  assert.equal(isRerunCommand(human("@claude review")), false);
  // A different account is not a command for us.
  assert.equal(isRerunCommand(human("@claude-bot rerun")), false);
});

// --- the round count, against #648's real shape ------------------------------

const ROUND_NAMES = ["agent-review-correctness", "agent-review-security"];
const lensRun = (conclusion, completed_at) => ({
  name: "agent-review-correctness", app: { slug: "github-actions" }, conclusion, completed_at,
});
const commitAt = (sha, date, runs) => ({
  sha, parents: [{ sha: "p" }], commit: { committer: { date } }, checkRuns: runs,
});

// The exact shape of PR #648: the implement agent pushes its work, self-reviews,
// fixes what it found and pushes AGAIN — two commits before the panel has ever
// spoken — then the fix loop pushes once. The old count returned 3 and tripped a
// cap of 3 after a SINGLE fix attempt.
const PR648 = [
  commitAt("cde0fad48", "2026-08-03T09:31:22Z", [lensRun("failure", "2026-08-03T10:05:15Z")]),
  commitAt("1d9e19dee", "2026-08-03T09:43:53Z", [lensRun("failure", "2026-08-03T10:11:28Z")]),
  commitAt("102e0fa73", "2026-08-03T10:32:17Z", [lensRun("failure", "2026-08-03T17:42:33Z")]),
];

test("countFailedReviewRounds: commits predating the FIRST verdict are not fix attempts", () => {
  // A commit committed before the panel first spoke cannot be a response to it.
  assert.equal(countFailedReviewRounds(PR648, ROUND_NAMES), 1);
});

test("countFailedReviewRounds: a retry moves the floor, granting a fresh budget", () => {
  // #648 after a maintainer hands it back: the pre-retry attempt no longer counts,
  // so the loop gets its full MAX_REVIEW_ROUNDS again instead of re-paging on the
  // first round.
  assert.equal(countFailedReviewRounds(PR648, ROUND_NAMES, { since: "2026-08-03T18:00:00Z" }), 0);
  // A post-retry failure does count.
  const after = [...PR648, commitAt("aaaaaaaaa", "2026-08-03T18:30:00Z", [lensRun("failure", "2026-08-03T18:45:00Z")])];
  assert.equal(countFailedReviewRounds(after, ROUND_NAMES, { since: "2026-08-03T18:00:00Z" }), 1);
  // A retry OLDER than the first verdict must not widen the count back out.
  assert.equal(countFailedReviewRounds(PR648, ROUND_NAMES, { since: "2026-08-03T08:00:00Z" }), 1);
});

test("countFailedReviewRounds: FAILS TOWARD COUNTING when the floor is unknowable", () => {
  // This number feeds a CAP. Over-counting pages a round early, which `@claude
  // retry` undoes; under-counting means the cap never trips and the loop is
  // unbounded, recoverable only if someone notices. So missing timestamps must
  // not silently disable the bound.
  //
  // No verdict timestamps anywhere → no floor → every failing commit counts,
  // exactly as before this refinement. (This is the PR #521 fixture's shape.)
  const noStamps = PR648.map((c) => ({
    ...c,
    checkRuns: c.checkRuns.map((r) => ({ ...r, completed_at: undefined })),
  }));
  assert.equal(countFailedReviewRounds(noStamps, ROUND_NAMES), 3);
  // A floor exists, but ONE commit has no committer date → it cannot be proven to
  // predate the floor, so it counts.
  const undated = [PR648[0], { ...PR648[1], commit: {} }, PR648[2]];
  assert.equal(countFailedReviewRounds(undated, ROUND_NAMES), 2);
  // Genuinely nothing to count.
  const noRuns = PR648.map((c) => ({ ...c, checkRuns: [] }));
  assert.equal(countFailedReviewRounds(noRuns, ROUND_NAMES), 0);
  for (const bad of [null, undefined, "x", 7]) assert.equal(countFailedReviewRounds(bad, ROUND_NAMES), 0);
  assert.equal(countFailedReviewRounds(PR648, []), 0);
});

test("countFailedReviewRounds: merges and passing rounds still do not count", () => {
  const merge = { ...PR648[2], parents: [{ sha: "a" }, { sha: "b" }] };
  assert.equal(countFailedReviewRounds([PR648[0], PR648[1], merge], ROUND_NAMES), 0);
  const passed = { ...PR648[2], checkRuns: [lensRun("success", "2026-08-03T17:42:33Z")] };
  assert.equal(countFailedReviewRounds([PR648[0], PR648[1], passed], ROUND_NAMES), 0);
  // A same-named check from another app cannot inflate the count.
  const foreign = { ...PR648[2], checkRuns: [{ ...lensRun("failure", "2026-08-03T17:42:33Z"), app: { slug: "other" } }] };
  assert.equal(countFailedReviewRounds([PR648[0], PR648[1], foreign], ROUND_NAMES), 0);
});

test("countFailedReviewRounds: a malformed `since` is ignored, not treated as zero", () => {
  // A `since` of NaN must fall back to the first-verdict floor rather than
  // silently becoming epoch 0 (which would count every commit) or Infinity.
  for (const bad of ["nonsense", "", null, undefined, 7, {}]) {
    assert.equal(countFailedReviewRounds(PR648, ROUND_NAMES, { since: bad }), 1, JSON.stringify(bad));
  }
});

test("firstVerdictAt: a foreign app's same-named check cannot lower the floor", () => {
  // Reached through countFailedReviewRounds. A lower floor widens the count, which
  // is the wrong direction for a value that gates a cap.
  const spoofed = [
    { ...PR648[0], checkRuns: [{ name: "agent-review-correctness", app: { slug: "impostor" }, conclusion: "failure", completed_at: "2026-08-03T09:00:00Z" }] },
    PR648[1],
    PR648[2],
  ];
  // Without the app guard the floor would drop to 09:00 and 1d9e19dee (09:43) would
  // count as an attempt; with it, the floor stays at the real first verdict.
  assert.equal(countFailedReviewRounds(spoofed, ROUND_NAMES), 1);
});

test("fixAttemptCommits: the STALL door sees fix rounds only, like the cap", () => {
  // The stall bound ran over groupReviewRounds(ALL commits), which includes the two
  // the implement workflow pushes before the panel has spoken — so three rounds of
  // "evidence" existed immediately and the stall door could cut the loop to one real
  // attempt, the same failure the cap fix closed arriving through the other bound.
  const kept = fixAttemptCommits(PR648, ROUND_NAMES);
  assert.deepEqual(kept.map((c) => c.sha), ["102e0fa73"]);
  // Same predicate the cap uses — if these ever disagree, one bound is counting
  // something the other is not.
  assert.equal(kept.length, countFailedReviewRounds(PR648, ROUND_NAMES));
  // A rerun floor narrows both together.
  assert.deepEqual(fixAttemptCommits(PR648, ROUND_NAMES, { since: "2026-08-03T18:00:00Z" }), []);
  for (const bad of [null, undefined, "x", 7]) assert.deepEqual(fixAttemptCommits(bad, ROUND_NAMES), []);
});

// --- the rerun floor must use permission, not author_association ------------

test("isRerunCommand: a maintainer reported as CONTRIBUTOR still sets the floor", () => {
  // #648, exactly. Four `@claude rerun` commands from a Maintain-level maintainer
  // were all reported CONTRIBUTOR — association describes the relationship to the
  // PR thread, not permission — so the floor was never set and the guard counted
  // the PR's whole history, paging "tried 3 time(s) (limit 3)" against a rerun
  // that had reset nothing.
  const c = {
    user: { login: "harrykim8672", type: "User" },
    author_association: "CONTRIBUTOR",
    body: "@claude rerun",
    created_at: "2026-08-06T04:30:47Z",
  };
  assert.equal(isRerunCommand(c), false, "association alone still refuses — that is the bug");
  assert.equal(isRerunCommand(c, { trusts: () => true }), true, "the authoritative check accepts");
  assert.equal(rerunPointFrom([c], { trusts: () => true }), "2026-08-06T04:30:47.000Z");
});

test("isRerunCommand: the resolver OVERRIDES a trusted-looking association", () => {
  // The gap this closes: `MEMBER` is membership of the owning ORG and
  // `COLLABORATOR` is satisfied by a read-only invite, so neither proves repo
  // write. Accepting them would move the floor for a commenter
  // `agent-rerun.yml` then refuses to run — budget granted for a rerun that
  // never happened, which is the same two-authorities-disagreeing bug this
  // function exists to remove, pointed the other way.
  const base = { user: { login: "m", type: "User" }, body: "@claude rerun", created_at: "2026-08-06T00:00:00Z" };
  const asked = [];
  const denies = (login) => { asked.push(login); return false; };

  for (const a of ["OWNER", "MEMBER", "COLLABORATOR"]) {
    assert.equal(
      isRerunCommand({ ...base, author_association: a }, { trusts: denies }),
      false,
      `${a} must not bypass the authoritative check`,
    );
  }
  assert.deepEqual(asked, ["m", "m", "m"], "every non-Bot rerun commenter is resolved");

  // And it is a real check, not a blanket deny: the same associations pass when
  // the resolver says the login actually has write access.
  for (const a of ["OWNER", "MEMBER", "COLLABORATOR", "NONE", "CONTRIBUTOR"]) {
    assert.equal(isRerunCommand({ ...base, author_association: a }, { trusts: () => true }), true);
  }
});

test("isRerunCommand: without a resolver it stays pure and association-only", () => {
  // The resolver-less form is the legacy behaviour, kept so the function is
  // testable without a network. `loop-status.mjs` is its only caller and is a
  // projection, never a gate — no gating caller reaches this path.
  const base = { user: { login: "m", type: "User" }, body: "@claude rerun", created_at: "2026-08-06T00:00:00Z" };
  for (const a of ["OWNER", "MEMBER", "COLLABORATOR"]) {
    assert.equal(isRerunCommand({ ...base, author_association: a }), true);
  }
  assert.equal(isRerunCommand({ ...base, author_association: "NONE" }), false);
});

test("FAIL DIRECTION: an unresolvable login does NOT reset the budget", () => {
  // Resetting on a failed lookup would hand more attempts to the very party the
  // budget bounds. Leaving it paged sends the PR to a human, which is where one
  // the loop cannot finish belongs.
  const c = { user: { login: "who", type: "User" }, author_association: "NONE", body: "@claude rerun", created_at: "2026-08-06T00:00:00Z" };
  assert.equal(isRerunCommand(c, { trusts: () => null }), false);
  assert.equal(isRerunCommand(c, { trusts: () => undefined }), false);
  assert.equal(isRerunCommand(c, { trusts: () => "yes" }), false, "only a strict true counts");
  assert.equal(rerunPointFrom([c], { trusts: () => null }), null);
  // And with no resolver at all the function is exactly what it was before.
  assert.equal(isRerunCommand(c), false);
});

test("a BOT is refused before any permission lookup", () => {
  // The structural exclusion is what stops the bounded party resetting its own
  // bound; a resolver must never be able to override it.
  let asked = 0;
  const c = {
    user: { login: "yorkie-agent[bot]", type: "Bot" },
    author_association: "OWNER",
    body: "@claude rerun",
    created_at: "2026-08-06T00:00:00Z",
  };
  assert.equal(isRerunCommand(c, { trusts: () => { asked++; return true; } }), false);
  assert.equal(asked, 0);
});

test("rerunPointFrom: the NEWEST qualifying rerun wins", () => {
  const mk = (t) => ({ user: { login: "m", type: "User" }, author_association: "CONTRIBUTOR", body: "@claude rerun", created_at: t });
  const got = rerunPointFrom([mk("2026-08-04T06:01:06Z"), mk("2026-08-06T04:30:47Z"), mk("2026-08-05T01:30:10Z")], { trusts: () => true });
  assert.equal(got, "2026-08-06T04:30:47.000Z");
});

test("the guard actually passes a permission resolver to rerunPointFrom", async () => {
  // The tests above prove the predicate; they cannot prove review-round-guard.mjs
  // supplies `trusts`, because that call is top-level CLI code needing `gh`.
  // Without it the module falls back to association-only — i.e. straight back to
  // the #648 behaviour, with every one of these tests still green.
  const { readFileSync } = await import("node:fs");
  const src = readFileSync(new URL("./review-round-guard.mjs", import.meta.url), "utf8");
  assert.match(src, /rerunPointFrom\(comments,\s*\{\s*trusts:\s*permissionResolver\(/);
  assert.match(src, /permissionResolver\(\{\s*api:\s*ghJson\s*\}\)/, "the resolver needs PARSED json, not this module's string-returning gh()");
  assert.match(src, /import \{ permissionResolver \} from "\.\/gh-checks\.mjs"/);
});

// --- the dispatch ledger -----------------------------------------------------
//
// Replaces "a commit that looks like a fix" with "the guard says it sent the
// fixer in". #695 is the case that forced it: three counted rounds, ONE fixer
// invocation, and a page whose message was false.

const dispatch = (over = {}, rec = {}) => ({
  user: { login: FIX_DISPATCH_AUTHOR_LOGIN, type: "Bot" },
  body: serializeFixDispatch(rec),
  created_at: "2026-08-06T10:00:00Z",
  ...over,
});

test("FIX_DISPATCH_MARKER is the exact marker, pinned as a literal", () => {
  assert.equal(FIX_DISPATCH_MARKER, "<!-- agent-fix-dispatch ");
  assert.equal(FIX_DISPATCH_AUTHOR_LOGIN, "github-actions[bot]");
});

test("a dispatch record round-trips through the comment body", () => {
  const got = parseFixDispatchComment(dispatch({}, { from: "6d0b9229f", prior: 2 }));
  assert.deepEqual({ from: got.from, prior: got.prior }, { from: "6d0b9229f", prior: 2 });
});

test("a `-->` in any field cannot truncate the record", () => {
  // Unreachable today (every field is a number or a hex SHA) but the fail
  // direction is the bad one: a truncated payload drops the record and LOWERS
  // the count. Pinned so the next string field added here inherits the escape.
  const body = serializeFixDispatch({ from: "abc--> injected", prior: 1 });
  assert.ok(!body.slice(0, -4).includes("-->"), "the raw body must carry no early terminator");
  const got = parseFixDispatchComment(dispatch({ body }));
  assert.equal(got.from, "abc--> injected", "and JSON.parse must restore it exactly");
  assert.equal(got.prior, 1);
});

test("the dispatch comment says something a human can read", () => {
  // #690's lesson: a marker-only body is an empty-looking bot comment, and a
  // dispatch was the one loop event with no timeline surface at all.
  const body = renderFixDispatchComment({ from: "4d46871ac0", prior: 0, round: 2, max: 3 });
  assert.match(body, /Fix round 2 of 3/);
  assert.match(body, /`4d46871ac`/, "the round's head, short-form");
  assert.ok(parseFixDispatchComment(dispatch({ body })), "and the record still round-trips");
  // The body is written by a TRUSTED latch author, so it must not be able to
  // carry a live latch of its own.
  assert.ok(!body.includes(PAGED_LATCH));
});

test("an unknown round or cap degrades to a bare line, never 'undefined'", () => {
  const body = renderFixDispatchComment({ from: "abc123def" });
  assert.match(body, /\*\*Fix round\*\* —/);
  assert.ok(!/undefined|null|NaN/.test(body), body);
});

test("the fixer's OWN identity cannot write a dispatch record", () => {
  // THE point of the narrower author gate. `yorkie-agent[bot]` is a trusted
  // paged-latch author AND the App identity the fix agent posts under, so
  // accepting it would let the party bounded by MAX_REVIEW_ROUNDS plant the
  // record that decides which counting rule applies to it.
  const fixerAuthored = dispatch({ user: { login: "yorkie-agent[bot]", type: "Bot" } });
  assert.equal(parseFixDispatchComment(fixerAuthored), null);
});

test("no association path: a maintainer's hand-written record is not believed", () => {
  // Unlike the paged latch, where a human halting the loop by hand is supported.
  for (const assoc of ["OWNER", "MEMBER", "COLLABORATOR", "CONTRIBUTOR", "NONE"]) {
    const byHand = dispatch({ user: { login: "harrykim8672", type: "User" }, author_association: assoc });
    assert.equal(parseFixDispatchComment(byHand), null, assoc);
  }
  // Nor can an account merely NAMED like the bot — the type is checked too.
  const impostor = dispatch({ user: { login: FIX_DISPATCH_AUTHOR_LOGIN, type: "User" } });
  assert.equal(parseFixDispatchComment(impostor), null);
});

test("a malformed record is absent, never half-read", () => {
  assert.equal(parseFixDispatchComment(dispatch({ body: "no marker here" })), null);
  assert.equal(parseFixDispatchComment(dispatch({ body: `${FIX_DISPATCH_MARKER}{not json} -->` })), null);
  assert.equal(parseFixDispatchComment(dispatch({ body: `${FIX_DISPATCH_MARKER}{"v":99} -->` })), null);
  for (const bad of [null, undefined, "x", 7, {}]) assert.equal(parseFixDispatchComment(bad), null);
  assert.deepEqual(collectFixDispatches(null), []);
});

test("collectFixDispatches orders oldest-first regardless of comment order", () => {
  const got = collectFixDispatches([
    dispatch({ created_at: "2026-08-06T12:00:00Z" }, { from: "c" }),
    dispatch({ created_at: "2026-08-06T10:00:00Z" }, { from: "a" }),
    dispatch({ created_at: "2026-08-06T11:00:00Z" }, { from: "b" }),
  ]);
  assert.deepEqual(got.map((d) => d.from), ["a", "b", "c"]);
});

test("fixRoundsUsed: with no ledger it IS countFailedReviewRounds", () => {
  // A PR opened before the ledger shipped keeps the behaviour it started with.
  assert.equal(fixRoundsUsed([], PR648, ROUND_NAMES), countFailedReviewRounds(PR648, ROUND_NAMES));
  assert.equal(fixRoundsUsed([], PR648, ROUND_NAMES), 1);
  // Comments that are not records leave the fallback in place.
  assert.equal(fixRoundsUsed([human("nice work")], PR648, ROUND_NAMES), 1);
});

test("fixRoundsUsed: a forged record cannot switch a PR off the fallback", () => {
  // The attack the author gate exists to stop: one planted record would otherwise
  // make records authoritative and drop a 3-round PR to 1, handing back budget.
  const forged = dispatch({ user: { login: "stranger", type: "User" }, author_association: "OWNER" });
  assert.equal(fixRoundsUsed([forged], PR648, ROUND_NAMES), countFailedReviewRounds(PR648, ROUND_NAMES));
});

test("fixRoundsUsed: records are counted, and the lens set stops mattering", () => {
  const ledger = [dispatch(), dispatch(), dispatch()];
  assert.equal(fixRoundsUsed(ledger, PR648, ROUND_NAMES), 3);
  // No commit or lens input is consulted once a ledger exists.
  assert.equal(fixRoundsUsed(ledger, [], []), 3);
});

test("fixRoundsUsed: the FIRST record's baseline carries the pre-ledger history", () => {
  // Otherwise a PR mid-flight when this shipped silently earns its spent rounds back.
  const ledger = [dispatch({ created_at: "2026-08-06T10:00:00Z" }, { prior: 2 })];
  assert.equal(fixRoundsUsed(ledger, [], []), 3);
  ledger.push(dispatch({ created_at: "2026-08-06T11:00:00Z" }, { prior: 0 }));
  assert.equal(fixRoundsUsed(ledger, [], []), 4);
  // Only the first record's baseline is read — a later one cannot add to it.
  const noisy = [...ledger, dispatch({ created_at: "2026-08-06T12:00:00Z" }, { prior: 7 })];
  assert.equal(fixRoundsUsed(noisy, [], []), 5);
});

test("fixRoundsUsed: a rerun that cuts the ledger drops the baseline with it", () => {
  const ledger = [
    dispatch({ created_at: "2026-08-06T10:00:00Z" }, { prior: 2 }),
    dispatch({ created_at: "2026-08-06T11:00:00Z" }),
  ];
  // Hand-back after both: a fresh budget, and the pre-rerun estimate must not
  // ride across the floor and spend it before the first new attempt.
  assert.equal(fixRoundsUsed(ledger, [], [], { since: "2026-08-06T12:00:00Z" }), 0);
  // Hand-back between them: one attempt since, still no baseline.
  assert.equal(fixRoundsUsed(ledger, [], [], { since: "2026-08-06T10:30:00Z" }), 1);
  // A floor that cuts nothing keeps the baseline.
  assert.equal(fixRoundsUsed(ledger, [], [], { since: "2026-08-06T09:00:00Z" }), 4);
  // A malformed floor is ignored, not read as zero.
  for (const bad of [null, undefined, "", "not-a-date"]) {
    assert.equal(fixRoundsUsed(ledger, [], [], { since: bad }), 4, JSON.stringify(bad));
  }
});

test("fixRoundsUsed: an undatable record is KEPT, not silently forgiven", () => {
  // Same fail direction as fixAttemptCommits' undatable commit. A record whose
  // timestamp cannot be read has not been shown to predate the floor, and
  // dropping it would hand back a round rather than cost one.
  const undatable = dispatch({ created_at: "" });
  assert.equal(fixRoundsUsed([undatable], [], [], { since: "2026-08-06T12:00:00Z" }), 1);
});

test("PR #695: three counted rounds become the ONE fix round that happened", () => {
  // The real shape. 8d85caa13 implement, 66b5b2833 + 3a6f5859f the implement
  // job's own self-review pushes, 6d0b9229f the single fix round. The first
  // verdict landed 09:57:43 — before the two self-review pushes, which is why
  // the commit-shape rule counted them and paged after one real attempt.
  const PR695 = [
    commitAt("8d85caa13", "2026-08-06T09:49:04Z", [lensRun("failure", "2026-08-06T09:57:43Z")]),
    commitAt("66b5b2833", "2026-08-06T10:00:33Z", [lensRun("failure", "2026-08-06T10:01:42Z")]),
    commitAt("3a6f5859f", "2026-08-06T10:01:30Z", [lensRun("failure", "2026-08-06T10:11:19Z")]),
    commitAt("6d0b9229f", "2026-08-06T10:38:05Z", [lensRun("failure", "2026-08-06T10:49:45Z")]),
  ];
  assert.equal(countFailedReviewRounds(PR695, ROUND_NAMES), 3, "the shape that paged #695");
  assert.equal(fixRoundsUsed([dispatch()], PR695, ROUND_NAMES), 1, "what actually happened");
});

// --- a superseded round is not a failed round --------------------------------

test("a superseded lens run does not count as a fix attempt", () => {
  // close-stuck-checks marks a cancelled panel's lenses `cancelled`, and the
  // whole point is that this predicate then ignores them. If that conclusion
  // ever goes back to `failure`, #695's phantom round comes back with it.
  const superseded = commitAt("66b5b2833", "2026-08-06T10:00:33Z", [
    lensRun("cancelled", "2026-08-06T10:01:42Z"),
  ]);
  const commits = [
    commitAt("8d85caa13", "2026-08-06T09:49:04Z", [lensRun("failure", "2026-08-06T09:57:43Z")]),
    superseded,
  ];
  assert.equal(countFailedReviewRounds(commits, ROUND_NAMES), 0);
});

test("the panel workflow does not record verdicts for a cancelled round", () => {
  // Prose in a YAML comment is not a guard. `always()` here is what wrote six
  // fail-closed reds onto a commit nobody reviewed.
  const from = PANEL_WORKFLOW.indexOf("- name: Post per-lens check runs");
  assert.ok(from > 0, "the verdict step must still be findable by name");
  const head = PANEL_WORKFLOW.slice(from, PANEL_WORKFLOW.indexOf("uses:", from));
  assert.match(head, /!cancelled\(\)/, "a superseded panel must not write verdicts");
  assert.doesNotMatch(head, /always\(\)/, "always() is exactly the bug");
});

test("close-stuck-checks distinguishes superseded from broken", () => {
  const job = PANEL_WORKFLOW.slice(PANEL_WORKFLOW.indexOf("close-stuck-checks:"));
  assert.match(job, /PANEL_RESULT: \$\{\{ needs\.review-panel\.result \}\}/);
  assert.match(job, /superseded \? 'cancelled' : 'failure'/);
});
