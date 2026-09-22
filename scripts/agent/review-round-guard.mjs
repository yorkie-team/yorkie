// CLI glue for the "Review-round guard" step in agent-review-panel.yml's
// `fix` job. Runs from a TRUSTED `ref: main` checkout (never the PR branch),
// so attacker-controlled branch code can never alter this gate's logic.
//
// Deliberately a plain `gh`-CLI-driven script (mirrors mark-ready.mjs), NOT
// `actions/github-script` — there is no precedent anywhere in this repo's
// workflows for dynamically importing a local ES module from inside a
// github-script sandbox, and this lets rounds.mjs be imported the normal,
// already-precedented way (see mark-ready.mjs's `import { allRequiredPassed }
// from "./checks.mjs"`).
//
// Usage (GH_TOKEN must be set):
//   node ./scripts/agent/review-round-guard.mjs <pr> <max-rounds> <all-valid:true|false> <required-checks-csv> [infra-detail]
// Writes `paged` and `proceed` ("true"/"false") to $GITHUB_OUTPUT.

import { execFileSync } from "node:child_process";
import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { redactSecrets } from "./redact.mjs";
import {
  collectFixDispatches,
  fixAttemptCommits,
  fixRoundsUsed,
  groupReviewRounds,
  detectStalledRounds,
  renderFixDispatchComment,
  DEFAULT_SIMILARITY,
  PAGED_LATCH,
  isPagedLatchComment,
  rerunPointFrom,
} from "./rounds.mjs";
import { exhaustedFindings, MAX_REBUTTAL_ROUNDS } from "./rebuttal.mjs";
import {
  appendStepSummary,
  guardVerdictLine,
  renderGuardSummary,
  runUrlFromEnv,
  whereToLookLine,
} from "./guard-verdict.mjs";
import { permissionResolver } from "./gh-checks.mjs";

const [, , prArg, maxArg, allValidArg, requiredChecksArg, infraArg] = process.argv;
const pr = Number(prArg);
const max = parseInt(maxArg, 10);
const allValid = allValidArg === "true";
const requiredCheckNames = (requiredChecksArg ?? "").split(",").filter(Boolean);
// Non-empty when EVERY blocking lens failed on an API/quota error (the panel
// never ran) — a distinct, non-code failure worth its own honest hand-off.
// Redacted at the boundary even though it arrives safe by construction: this
// value comes in as a COMMAND-LINE ARGUMENT from a workflow, so it is only as
// trustworthy as every future edit to that workflow, and `page()` puts it
// straight into a public PR comment. One call is cheaper than the invariant.
const infra = redactSecrets((infraArg ?? "").trim());

// Convergence tuning, read from the env rather than added as a 6th and 7th
// positional: this script already takes five, and keeping it env-driven means
// the feature ships without a workflow edit (the agent App cannot push
// .github/workflows/**).
const stallRepeats = Number(process.env.STALL_REPEATS) || 2;
const stallSimilarity = Number(process.env.STALL_SIMILARITY) || DEFAULT_SIMILARITY;

// Where the round record is STAGED for the workflow's posting step. Under Actions
// `RUNNER_TEMP` survives the branch checkout that replaces the workspace, which is
// exactly why the file lives there and not in the tree. The workflow passes the
// same path explicitly; the default only keeps a hand-run of this script working.
const dispatchFile =
  process.env.DISPATCH_FILE || join(process.env.RUNNER_TEMP || tmpdir(), "fix-dispatch.md");

if (!Number.isInteger(pr) || pr <= 0 || !Number.isFinite(max)) {
  console.error(
    "Usage: node ./scripts/agent/review-round-guard.mjs <pr> <max-rounds> <all-valid> <required-checks-csv> [infra-detail]",
  );
  process.exit(2);
}

// Imported, not re-declared: agent-review-panel.yml's `gate` job now reads the
// same latch to stop running the panel, so a second literal here would be a
// second thing to keep in sync. See PAGED_LATCH's docblock in rounds.mjs.
const PAGED = PAGED_LATCH;

function setOutput(name, value) {
  appendFileSync(process.env.GITHUB_OUTPUT, `${name}=${value}\n`);
}
function gh(args) {
  return execFileSync("gh", args, { encoding: "utf8" });
}
function ghJson(args) {
  return JSON.parse(gh(args));
}

// Bare-array endpoints: `--paginate` alone merges all pages into ONE valid
// JSON array (verified against a real multi-page response). Do NOT add
// --slurp here — that changes the shape to an array of per-page arrays.
function listAll(path) {
  return ghJson(["api", path, "--paginate"]);
}

// Appended to every page. The latch does not just stop the fixer any more —
// agent-review-panel.yml's `gate` job reads it too and stops running the panel,
// so from here on there are no fresh lens verdicts and nothing will flip the
// PR to ready. Saying so is the difference between a handoff and a PR that
// looks like it is still being worked on.
const HANDOFF_NOTE =
  "\n\nThe review panel will not run again on this PR, so the " +
  "`agent-review-*` checks are now frozen at their current state and the ready " +
  "gate will not promote it. Review and merge it manually.";

// `reason` is a short machine-ish tag (infra / invalid-verdict / standstill /
// stall / round-cap) — it only labels the page for the verdict line and the
// job summary; the page comment itself stays exactly what `msg` says.
function page(msg, reason = "paged") {
  // "Where to look": the run this guard decided in, and the step whose log and
  // summary carry the full decision detail. Empty (today's body, unchanged)
  // when run outside Actions. No artifact named — the guard runs BEFORE the
  // fixer, so at page time no fix transcript exists yet.
  const where = whereToLookLine({
    runUrl: runUrlFromEnv(),
    job: process.env.GITHUB_JOB,
    step: "Review-round guard",
  });
  gh(["pr", "comment", String(pr), "--body", `${PAGED}\n🛑 ${msg}${where}${HANDOFF_NOTE}`]);
  // Labeling is intentionally NOT done here: the single-value state machine
  // owns it. The "Set state → blocked (paged)" step (gated on this `paged`
  // output) runs set-state.mjs, which atomically strips every lifecycle label
  // and sets agent:blocked. Writing agent:needs-human-review here would only
  // be immediately overwritten and contradicts that clean single-label cutover.
  setOutput("paged", "true");
  setOutput("proceed", "false");
  // OBSERVABILITY (display only — a rendering bug can mislabel a decision but
  // never change one): the one-line verdict feeds loop-status.mjs's sticky
  // comment via the workflow; the block goes to the run page.
  const verdict = { decision: "page", reason, detail: msg, failedRounds: pagedRoundContext.failedRounds, max };
  setOutput("verdict", guardVerdictLine(verdict));
  appendStepSummary(renderGuardSummary(verdict));
}

// Round count context for page() — filled in once commits are fetched; the
// infra/invalid-verdict pages fire before that and render without it.
const pagedRoundContext = { failedRounds: null };

// PAGED latch: paginate ALL comments (an iterating PR can exceed one page),
// so a later page isn't missed and the fix loop doesn't re-fire after a human
// was already paged.
// AUTHOR-CHECKED: this repo is public, so a body test alone would let any
// GitHub account stop the fix loop by pasting the marker. See
// isPagedLatchComment.
const comments = listAll(`repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`);
if (comments.some(isPagedLatchComment)) {
  setOutput("proceed", "false");
  setOutput("verdict", guardVerdictLine({ decision: "latched" }));
  appendStepSummary(renderGuardSummary({ decision: "latched" }));
  process.exit(0);
}
// `@claude rerun` (#650) deletes the paged comments, so the latch above is
// already clear by the time we get here. What it cannot do on its own is give the
// budget back — its summary says the PR is "still bounded by the pipeline's
// round/attempt caps", which on a PR that reached the cap means one panel round
// and an immediate re-page. That is exactly what #648 did when it was un-stuck by
// hand. The marker rerun leaves behind moves the round floor forward.
//
// The floor is resolved against the SAME authority the command itself is gated on
// (`getCollaboratorPermissionLevel`), not `author_association`. On #648 those two
// disagreed: the verb ran — the label came off, CI re-ran — while GitHub reported
// the maintainer's comments as `CONTRIBUTOR`, so the budget silently did not move
// and the next round paged with "tried 3 time(s) (limit 3)" against a rerun that
// had reset nothing. The resolver memoizes per login, so a PR with many comments
// from few people costs at most one call each, and only for logins whose
// association did not already settle it.
const rerunAt = rerunPointFrom(comments, { trusts: permissionResolver({ api: ghJson }) });
if (rerunAt) console.error(`rerun: counting fix rounds from ${rerunAt}`);

// API/quota outage (every blocking lens failed on an API error) → the reviewer
// never ran. Page with the REAL reason and DO NOT dispatch the fixer or count a
// round — there's nothing to fix, and a quota outage isn't a failed review
// round. Checked before the generic all_valid page so the message is honest.
if (infra) {
  page(
    `The review panel could not run — Claude API/quota error: ${infra} ` +
      `This is an infrastructure/credential issue, not a code problem. Re-run the panel after the limit resets.`,
    "infra",
  );
  process.exit(0);
}

// A lens that failed CLOSED (no valid verdict) → structural problem, page now.
if (!allValid) {
  page(`A review lens did not produce a valid verdict. A human should review PR #${pr}.`, "invalid-verdict");
  process.exit(0);
}

// Object-wrapped-array endpoint (`{ total_count, check_runs: [] }`): plain
// `--paginate` concatenates raw per-page JSON objects, which is NOT valid
// single JSON (verified) — `--slurp` wraps each page as an array element
// instead; flatten `check_runs` across pages ourselves.
//
// Deliberately does NOT catch/swallow errors here (unlike mark-ready.mjs's
// read helpers, which fail closed by returning false): an uncaught throw
// fails this step, which fails the `fix` job, which the `stalled` job's
// safety net (its own page()) already catches and hands to a human. Silently
// treating an unreadable commit as "no failing checks" would under-count
// failedRounds and could let the loop run an extra round instead.
function checkRunsFor(sha) {
  const pages = ghJson(["api", `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`, "--paginate", "--slurp"]);
  return pages.flatMap((p) => p.check_runs ?? []);
}

const commits = listAll(`repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`);
for (const c of commits) c.checkRuns = checkRunsFor(c.sha);

// CONVERGENCE, checked before the round cap. A loop that keeps re-raising the
// same findings without reducing them has already failed; saying *that* is more
// actionable than "we hit 5 rounds", and it pages ~2 rounds sooner. Same
// ordering rationale as the INFRA branch above: the more specific reason wins.
//
// The list response for commits/{sha}/check-runs can omit or truncate
// output.text (the panel workflow's own "Read prior findings" step does a
// separate checks.get for exactly this reason), so back-fill it — but only for
// the newest rounds convergence actually inspects, and only where it is
// missing. Bounded to about (rounds x lenses) extra calls, usually zero.
const lensNames = new Set(requiredCheckNames);
const isLensRun = (r) => lensNames.has(r.name) && r.app?.slug === "github-actions";
for (const c of commits.filter((x) => (x.checkRuns ?? []).some(isLensRun)).slice(-(stallRepeats + 1))) {
  for (const r of c.checkRuns) {
    if (!isLensRun(r) || (typeof r.output?.text === "string" && r.output.text !== "")) continue;
    // A failure here leaves output.text absent, which groupReviewRounds treats
    // as a PARTIAL round — that breaks the stall run rather than inventing one.
    try {
      r.output = ghJson(["api", `repos/{owner}/{repo}/check-runs/${r.id}`]).output ?? r.output;
    } catch {
      /* fail toward not paging */
    }
  }
}

const rounds = groupReviewRounds(commits, requiredCheckNames);

// The stall detector gets FIX-ATTEMPT rounds only, for the same reason the round
// cap counts them: `groupReviewRounds` over every commit includes the two the
// implement workflow pushes before the panel has ever spoken, so with three rounds
// of "evidence" available immediately the stall door could cut the loop to one real
// attempt — the very failure the round-cap fix closed, arriving through the other
// bound. `fixAttemptCommits` is the same predicate, exposed so both agree.
const stallRounds = groupReviewRounds(
  fixAttemptCommits(commits, requiredCheckNames, { since: rerunAt }),
  requiredCheckNames,
);

// ARGUED TO A STANDSTILL, checked before convergence for the same reason the
// infra branch is checked before `allValid`: the more specific reason wins, and
// "the author disputed this twice and an independent adjudicator upheld it both
// times" tells a human far more than "the panel keeps re-raising findings".
//
// The count is read from the check runs' `output.text` — the unforgeable channel
// the panel writes — not from the author's own rebuttal comments. Counting those
// would let the author drive the page. Paging is the SAFE direction, so a forged
// extra count costs only an unnecessary human look; but a bound the disputing
// party can move is not a bound, and it would be the one number in this file that
// the party it constrains gets to choose.
// Computed BEFORE the three page paths below, because all three are bounds on the
// loop and a hand-back has to reset all three. Only the round cap honoured the
// rerun floor at first, which left the earlier two pages looking at pre-rerun
// history — so `@claude rerun` on a PR that had already stalled or exhausted its
// rebuttals re-paged on the very first post-rerun round. That is the exact "one
// panel round and an immediate re-page" this change exists to end, arriving through
// a different door.
// Reads the DISPATCH LEDGER (this script's own records) when the PR has one, and
// falls back to the commit-shape inference when it does not — see
// `rounds.mjs::fixRoundsUsed`. The number now means "times the fixer was actually
// sent in", which is what `MAX_REVIEW_ROUNDS` was always meant to bound.
const failedRounds = fixRoundsUsed(comments, commits, requiredCheckNames, { since: rerunAt });
pagedRoundContext.failedRounds = failedRounds;
// A hand-back buys the loop at least one attempt before the softer bounds may fire
// again. The round cap needs no such rule — its count already starts at the rerun.
// The other two cannot be filtered as cleanly: the stall detector reads rounds
// whose findings carry no timestamp, and the rebuttal count is an integer riding
// forward on the finding with no provenance at all. "At least one post-rerun
// attempt" is the honest common denominator, and it fails toward paging: it delays
// these two by exactly one round, never disables them.
const heldByRerun = rerunAt !== null && failedRounds === 0;
if (heldByRerun) console.error("rerun: holding the stall/standstill pages for one attempt");

const latestRound = rounds.length ? rounds[rounds.length - 1] : null;
const exhausted = heldByRerun ? [] : exhaustedFindings(latestRound?.findings);
if (exhausted.length > 0) {
  page(
    `A finding has been disputed ${MAX_REBUTTAL_ROUNDS} times and upheld every time by an ` +
      `independent adjudicator. The loop cannot settle this by itself — either the finding is ` +
      `right and the author cannot act on it, or it is wrong in a way the adjudicator cannot see. ` +
      `Standstill: ${exhausted.slice(0, 5).join("; ")}. A human should decide on PR #${pr}.`,
    "standstill",
  );
  process.exit(0);
}

const stall = detectStalledRounds(stallRounds, {
  minRepeats: stallRepeats,
  similarity: stallSimilarity,
});
console.error(`convergence: ${stall.reason} (stalls=${stall.stalls}, rounds=${stall.rounds})`);
if (stall.stalled && !heldByRerun) {
  const named = stall.repeated
    .slice(0, 5)
    .map((f) => `\`${f.file || "?"}\` — ${String(f.summary ?? "").slice(0, 160)}`)
    .join("; ");
  page(
    `The review panel is re-raising the same findings without resolving them ` +
      `(${stall.stalls + 1} consecutive rounds with no reduction in blocking findings). ` +
      `Repeated: ${named}. Another fix round would spend budget on the same ground — ` +
      `a human should take over on PR #${pr}.`,
    "stall",
  );
  process.exit(0);
}

if (failedRounds >= max) {
  page(
    `The fix agent has been dispatched ${failedRounds} time(s) (limit ${max}) without converging` +
      `${rerunAt ? " since the last rerun" : ""}. A human should take over on PR #${pr}.`,
    "round-cap",
  );
  process.exit(0);
}

// STAGE the round record here; the workflow POSTS it immediately before the
// fixer. The split is the whole point, and the first version got it wrong.
//
// Posting from this step spends the round at the top of the `fix` job — and the
// fixer is fourteen steps later, behind an App-token mint, a branch checkout and
// a `pnpm install`. The panel workflow is `cancel-in-progress: true`, so any push
// during those one-to-three minutes kills the run with the record already
// written: a round consumed by a fixer that never started. That is the same shape
// as the phantom round this whole change exists to remove (#695's cancelled panel
// left six reds on a commit nobody reviewed), just smaller and self-inflicted, and
// #695 itself had pushes 57 seconds apart. Deferring the post to the step before
// `Address panel findings` shrinks the window from minutes to seconds.
//
// The window cannot be closed entirely — some instant separates "recorded" from
// "running". What it CAN be is on the right side of the remaining risk: after the
// post, a cancellation costs a round for a fixer that had genuinely begun, which
// is a round honestly spent.
//
// Still NOT best-effort, on both halves. A dispatch whose record never landed is
// a round spent off the books that the next guard hands out again — the one
// direction this bound must not fail in. `writeFileSync` throwing reds this step;
// the posting step is a plain `run:` with no `continue-on-error`, so a failure
// there reds the job before the fixer is reached. Either way the `stalled` net
// hands it to a human.
//
// `prior` seeds the ledger from the inference it replaces, and only ever on the
// FIRST record: a PR mid-flight when this shipped has already spent rounds that
// left no record, and starting its ledger at zero would quietly hand it those
// rounds back. `failedRounds` is the fallback count at this moment precisely
// because no record exists yet.
const headSha = commits.length ? String(commits[commits.length - 1].sha ?? "") : "";
const prior = collectFixDispatches(comments).length === 0 ? failedRounds : 0;
const dispatchBody = renderFixDispatchComment({
  from: headSha,
  prior,
  round: failedRounds + 1,
  max: Number.isFinite(max) ? max : null,
});
writeFileSync(dispatchFile, dispatchBody);
console.error(`dispatch: staged round ${failedRounds + 1} of ${max} (from ${headSha.slice(0, 9)}) at ${dispatchFile}`);

// OBSERVABILITY: the PROCEED branch was the one silent decision in this loop —
// only pages ever reached a human surface, so a continuing loop and a dead one
// looked identical from the PR. Render the same inputs the decision used
// (display only; guard-verdict.mjs holds no decision logic).
const verdict = {
  decision: "proceed",
  failedRounds,
  max,
  stall,
  standstillCount: exhausted.length,
  rebuttalLimit: MAX_REBUTTAL_ROUNDS,
  infra,
  heldByRerun,
  rerunAt,
  requiredCheckNames,
};
setOutput("verdict", guardVerdictLine(verdict));
appendStepSummary(renderGuardSummary(verdict));
setOutput("proceed", "true");
