// Round arithmetic for the review-round guard (agent-review-panel.yml's `fix`
// job), plus the comment markers that start and stop the loop.
//
// WHAT COUNTS AS A FIX ATTEMPT takes two filters, and for a long time it had only
// the first. A merge commit (2 parents, e.g. a human `git merge main` on an
// iterating PR) is not a fix attempt: PR #521 had 3 single-parent fixer commits
// and 3 merges, and all 6 counted. But parent count alone is not enough either —
// the implement workflow pushes its work, self-reviews, and pushes AGAIN, so two
// commits draw two panel rounds before the fix loop has run once. Both were
// counted, which on #648 and #605 consumed exactly 2 of the budget and left
// `MAX_REVIEW_ROUNDS: 3` meaning ONE real attempt.
//
// The second filter is time: a commit committed BEFORE the panel first spoke
// cannot be a response to it. Deliberately identity-independent, like the first —
// the bot identity behind fixer pushes has already changed once in this
// pipeline's history, so neither "who pushed" nor a name is a durable signal.
//
// It is a heuristic about ORDERING, not provenance, and the edge is worth naming:
// if a panel verdict lands before the implementer's self-review push, that push
// counts as an attempt. Nothing in the data distinguishes them, and the direction
// is the conservative one (it over-counts, which pages early and is undone by
// `@claude rerun`).

import { parseCommand } from "./command.mjs";

/**
 * The marker that latches a PR as "handed to a human". Written by
 * review-round-guard.mjs's `page()` and by the `stalled` job, and read back by
 * BOTH of the pipeline's stop conditions:
 *
 *   - review-round-guard.mjs, to stop dispatching the fixer;
 *   - agent-review-panel.yml's `gate` job, to stop running the panel at all.
 *
 * It lives here, exported, because those two readers are a JS module and a
 * `github-script` step that cannot import one (the `gate` job does no checkout).
 * The workflow therefore carries a literal copy, and `rounds.test.mjs` asserts
 * the two are byte-identical — a drifted copy would not error, it would just
 * silently stop latching, which is the failure mode this constant exists to
 * prevent.
 */
export const PAGED_LATCH = "<!-- agent-review-paged -->";

/**
 * Bot identities allowed to WRITE the latch. Both are real: the guard and the
 * `stalled` job comment with `secrets.GITHUB_TOKEN` (`github-actions[bot]`),
 * while the fix job's branch-head page uses the App token (`yorkie-agent[bot]`).
 *
 * A login allow-list is sound because GitHub reserves the `[bot]` suffix for
 * Apps — no account can register one of these names.
 */
export const PAGE_AUTHOR_LOGINS = Object.freeze(["github-actions[bot]", "yorkie-agent[bot]"]);

/**
 * Associations that mean "a human attached to this project", NOT "has write
 * access" — `MEMBER` is org membership and `COLLABORATOR` is satisfied by a
 * read-only invite, so neither proves repo permission. Only
 * `permissionResolver` answers that.
 *
 * Good enough for `isPagedLatchComment`, whose fail direction is the safe one:
 * over-accepting there LATCHES the PR — it stops the loop and hands it to a
 * human. `isRerunCommand` is the opposite (accepting GRANTS budget) and so does
 * not use this as an accept path when a resolver is available.
 */
const TRUSTED_ASSOCIATIONS = new Set(["OWNER", "MEMBER", "COLLABORATOR"]);

/**
 * Does this comment legitimately latch the PR as handed-to-a-human?
 *
 * The marker alone is NOT enough, and this repo is PUBLIC: anyone with a GitHub
 * account can comment on a PR, so a body test on its own lets a stranger post
 * `<!-- agent-review-paged -->` and permanently stop the fixer AND the review
 * panel on any agent PR. Denial of review, from an unauthenticated position.
 *
 * `author_association` cannot carry this on its own — the writing bots report
 * `CONTRIBUTOR`, the same value an arbitrary outside contributor gets. So trust
 * is either an allow-listed bot login, or a human who actually has write access
 * (a maintainer halting the pipeline by hand stays supported).
 *
 * Pure and total: any unknown shape is untrusted, which fails toward reviewing.
 */
export function isPagedLatchComment(comment) {
  const c = comment && typeof comment === "object" ? comment : {};
  if (!String(c.body ?? "").includes(PAGED_LATCH)) return false;
  const user = c.user && typeof c.user === "object" ? c.user : {};
  if (user.type === "Bot" && PAGE_AUTHOR_LOGINS.includes(user.login)) return true;
  return TRUSTED_ASSOCIATIONS.has(String(c.author_association ?? ""));
}

/**
 * ISO timestamp of the newest `@claude rerun` from a HUMAN MAINTAINER, or null.
 *
 * This reads the maintainer's own COMMAND, not the marker the rerun workflow
 * writes back — and that distinction is the whole security property.
 *
 * The first version keyed on a hidden marker in the workflow's result comment,
 * trusted by bot login. But `agent-rerun.yml` posts with the App token, so the
 * trusted identity is `yorkie-agent[bot]` — the SAME identity the fixer and
 * implementer post their own free-form comments under (the self-review comment on
 * every agent PR is one). That made the party bounded by `MAX_REVIEW_ROUNDS` able
 * to reset its own bound by opening a comment with the marker line: an LLM reading
 * an untrusted diff, granted unlimited fix attempts, by accident or by injection.
 *
 * A human's command cannot be forged by a bot, because `user.type === "Bot"` is
 * refused outright and no App can present as a non-Bot. That is a STRUCTURAL
 * exclusion rather than a string it must not guess, which is why it replaced the
 * marker rather than being added alongside it.
 *
 * `parseCommand` is reused rather than re-matched: `agent-rerun.yml`'s router uses
 * it to decide the command actually ran, so a second regex here could recognise a
 * rerun the router did not (or miss one it did) and move the floor for a hand-back
 * that never happened.
 *
 * `author_association` ALONE was the trust test here, and it was wrong in the
 * direction this comment did not anticipate. It was named as a known gap for
 * being too permissive (an org MEMBER without repo write passing); the failure
 * that actually happened was the opposite. On #648 a maintainer with Maintain
 * ran `@claude rerun` four times and GitHub reported every one of those comments
 * as `CONTRIBUTOR` — association describes the commenter's relationship to the
 * PR thread, not their permission, and it reads CONTRIBUTOR for anyone with
 * commits on the repo whose org membership is not public. So the floor was never
 * set, the guard counted the PR's whole history, and it paged with "tried 3
 * time(s) (limit 3)" against a rerun that had reset nothing.
 *
 * The verb worked; only its consequence was dropped. `agent-rerun.yml` gates on
 * `getCollaboratorPermissionLevel` — authoritative — so the label came off and CI
 * re-ran, while the budget silently did not move. Two checks for one question,
 * disagreeing.
 *
 * `trusts` closes it: an injected `(login) => true | false | null` that the guard
 * builds from the same API the workflows use. When it is supplied it is the ONLY
 * authority — association is not consulted at all.
 *
 * Association is NOT kept as a fast-path accept, and an earlier revision of this
 * comment was wrong to claim OWNER/MEMBER/COLLABORATOR "already mean write
 * access". They do not. `MEMBER` is membership of the owning ORG, which on this
 * repo says nothing about repo permission, and `COLLABORATOR` is satisfied by a
 * read- or triage-only invite. Both would have moved the floor for a commenter
 * `agent-rerun.yml` then REFUSED to run — the same "two checks for one question,
 * disagreeing" shape this function exists to remove, just pointing the other
 * way: budget granted for a rerun that never happened. Saving one memoized API
 * call per rerun commenter is not worth reintroducing it.
 *
 * FAIL DIRECTION: an unresolvable login does NOT set the floor. Not resetting
 * leaves the PR paged for a human, which is where a PR the loop cannot finish
 * belongs; resetting on an unknown would hand more attempts to the bounded party
 * on the strength of a failed lookup. That now covers a trusted-looking
 * association whose permission lookup failed, which previously rode the
 * fast path.
 *
 * With no `trusts` at all the function is exactly what it was before — pure, and
 * association-only. The only caller without a resolver is `loop-status.mjs`,
 * which is a PROJECTION, NEVER A GATE (see its header): the worst it can do is
 * display a round count the guard will not honour. Every gating caller injects
 * one.
 */
export function rerunPointFrom(comments, opts) {
  const stamps = (Array.isArray(comments) ? comments : [])
    .filter((c) => isRerunCommand(c, opts))
    .map((c) => Date.parse(String(c.created_at ?? "")))
    .filter((n) => Number.isFinite(n));
  return stamps.length ? new Date(Math.max(...stamps)).toISOString() : null;
}

/** Is this a maintainer's `@claude rerun`? Bots are refused — see rerunPointFrom. */
export function isRerunCommand(comment, { trusts } = {}) {
  const c = comment && typeof comment === "object" ? comment : {};
  const user = c.user && typeof c.user === "object" ? c.user : {};
  // Structural, and checked first: no App can present as a non-Bot, so this is
  // what stops the bounded party resetting its own bound.
  if (user.type === "Bot") return false;
  if (parseCommand(String(c.body ?? ""), { surface: "pr" }).command !== "rerun") return false;
  // The resolver, when there is one, is the ONLY authority — it is the same
  // question `agent-rerun.yml` asks, asked the same way, so the two cannot
  // disagree. Association is consulted only in the pure, resolver-less form,
  // which no gating caller uses.
  if (typeof trusts === "function") return trusts(String(user.login ?? "")) === true;
  return TRUSTED_ASSOCIATIONS.has(String(c.author_association ?? ""));
}

/** A commit has exactly one parent — i.e. it is not a merge commit.
 *
 * NOT "was pushed by the fixer", which is what the old name (`isFixerCommit`)
 * claimed and what every caller read it as. It cannot know that: the check is
 * deliberately identity-independent, because the bot identity behind fixer
 * pushes has already changed once in this pipeline's history. See
 * `countFailedReviewRounds` for what actually separates a fix attempt from the
 * implementer's own commits. */
export function isSingleParentCommit(commit) {
  return Array.isArray(commit?.parents) && commit.parents.length === 1;
}

/** Earliest completed panel verdict anywhere on the PR, or null. */
function firstVerdictAt(commits, names) {
  const stamps = [];
  for (const c of Array.isArray(commits) ? commits : []) {
    for (const r of c?.checkRuns ?? []) {
      // Same app guard every other lens-run consumer applies. Without it a
      // same-named check from another installed App lowers the floor, which
      // widens the count — the wrong direction for a value that gates a cap.
      if (!names.has(r?.name) || r?.app?.slug !== "github-actions") continue;
      const t = Date.parse(String(r?.completed_at ?? ""));
      if (Number.isFinite(t)) stamps.push(t);
    }
  }
  return stamps.length ? Math.min(...stamps) : null;
}

/**
 * How many times has the FIX LOOP tried and failed?
 *
 * This used to count every single-parent commit carrying a failing lens verdict,
 * which is not the same thing and was wrong on every agent PR measured. The
 * implement workflow pushes its work, self-reviews, fixes what it found, and
 * pushes AGAIN — two commits, each triggering CI, each drawing its own panel
 * round. Both were counted as failed fix rounds before the fix loop had run once.
 * On #648 and #605 alike that silently consumed exactly 2 of the budget, so when
 * #615 lowered `MAX_REVIEW_ROUNDS` from 5 to 3 believing it was trimming a
 * wasteful tail, the real effect was to cut the loop from three attempts to ONE.
 * #648 then reported "requested changes 3 times without converging" after a
 * single fix attempt.
 *
 * A commit committed BEFORE the panel first spoke cannot be a response to it.
 * That is the whole discriminator, it needs no identity, and it is already in the
 * data. `since` moves the floor forward again after a maintainer's `@claude rerun`, so a
 * hand-back grants a fresh budget rather than resuming one already spent.
 */
export function fixAttemptCommits(commits, requiredCheckNames, { since = null } = {}) {
  const names = new Set(requiredCheckNames ?? []);
  const isFailingLensRun = (r) =>
    names.has(r.name) && r.app?.slug === "github-actions" && r.conclusion === "failure";
  const first = firstVerdictAt(commits, names);
  const s = Date.parse(String(since ?? ""));
  const sinceMs = Number.isFinite(s) ? s : null;
  const floor = first === null ? null : sinceMs !== null && sinceMs > first ? sinceMs : first;
  // FAILS TOWARD INCLUDING, so both consumers stay conservative: the count pages a
  // round early (undone by a rerun) and the stall detector keeps the evidence it
  // would otherwise discard. Under-including would silently unbound both.
  return (Array.isArray(commits) ? commits : []).filter((c) => {
    if (!isSingleParentCommit(c)) return false;
    if (!(c.checkRuns ?? []).some(isFailingLensRun)) return false;
    if (floor === null) return true;
    // Committer date, not author date: a rebased commit keeps an author date that
    // can predate a verdict it plainly followed.
    const at = Date.parse(String(c?.commit?.committer?.date ?? ""));
    if (!Number.isFinite(at)) return true;
    return at > floor;
  });
}

/**
 * How many times has the FIX LOOP tried and failed? See `fixAttemptCommits` for
 * what counts and why; this is its length, kept as its own export because the
 * number is what `MAX_REVIEW_ROUNDS` compares against.
 */
export function countFailedReviewRounds(commits, requiredCheckNames, opts = {}) {
  return fixAttemptCommits(commits, requiredCheckNames, opts).length;
}

// --- the dispatch ledger -----------------------------------------------------
//
// `fixAttemptCommits` infers "the fixer ran" from the SHAPE of a commit, and the
// inference is a race it loses. Its discriminator is "committed after the panel
// first spoke", but the implement workflow pushes, self-reviews, and pushes
// AGAIN — so whether those self-review pushes count depends on whether the first
// panel verdict happened to land before or after them. On #737 the self-review
// push beat the first verdict by 20 seconds and was correctly excluded. On #695
// it lost by 2m50s: two implement pushes were counted as fix attempts, a third
// round came from a panel that was CANCELLED and never reviewed anything, and
// the loop paged with "the fixer has tried 3 time(s)" after the fixer had run
// exactly ONCE. One fix report, three counted rounds, and the report was never
// the thing that was missing.
//
// Nothing in commit data distinguishes those pushes, so stop inferring. The
// guard is the only thing that dispatches the fixer; have it SAY SO, and count
// the record. Exact instead of approximate, and no longer sensitive to timing.
//
// WHO MAY WRITE ONE is the whole security property, and it is deliberately
// NARROWER than the paged latch's rule. The latch may be written by either bot
// (and by a maintainer by hand) because over-accepting there only STOPS the
// loop. Here the direction reverses: because records become authoritative the
// moment one exists, a single forged record would switch this PR off the
// commit-count fallback and could hand the bounded party a budget it has not
// earned. So:
//
//   - `github-actions[bot]` ONLY — the guard's `secrets.GITHUB_TOKEN` identity.
//     GitHub reserves the `[bot]` suffix for Apps, so no account can register it.
//   - NOT `yorkie-agent[bot]`, even though it is a trusted latch author: that is
//     the FIXER's own identity (the App token it holds to push and comment), and
//     the party bounded by `MAX_REVIEW_ROUNDS` must not be able to choose the
//     rule it is counted by. This is the same class of hole `rerunPointFrom`
//     closed when it stopped trusting a marker the App could write.
//   - NO association fallback. A maintainer halting the loop by hand is a real
//     need the latch serves; a maintainer hand-writing a dispatch record is not.

/** Hidden-comment marker for one dispatch of the fix agent. */
export const FIX_DISPATCH_MARKER = "<!-- agent-fix-dispatch ";

/** Bumped only if the record shape changes; an unknown version parses as absent. */
export const FIX_DISPATCH_VERSION = 1;

/**
 * The ONLY identity whose dispatch records are believed. See the block above for
 * why this is one login and not `PAGE_AUTHOR_LOGINS`.
 */
export const FIX_DISPATCH_AUTHOR_LOGIN = "github-actions[bot]";

/** Serialize one dispatch record. `prior` is the migration baseline — see `fixRoundsUsed`. */
export function serializeFixDispatch({ from = "", prior = 0 } = {}) {
  const payload = {
    v: FIX_DISPATCH_VERSION,
    from: String(from ?? "").slice(0, 64),
    prior: Number.isInteger(prior) && prior > 0 ? prior : 0,
  };
  // ESCAPE THE TERMINATOR, as `rebuttal.mjs` and `fix-report.mjs` do. Today every
  // field is a number or a hex SHA, so this can never fire — but `parseFix
  // DispatchComment`'s match is non-greedy, so a `-->` reaching a field would
  // truncate the JSON, drop the record, and LOWER the count. That is the one
  // direction this bound must not fail in, and it is a one-line insurance against
  // whatever string field gets added next. `\u002d` is the JSON escape for `-`,
  // so the raw comment never contains `-->` while `JSON.parse` restores the
  // exact characters.
  return `${FIX_DISPATCH_MARKER}${JSON.stringify(payload).replace(/-->/g, "-\\u002d>")} -->`;
}

/**
 * Visible line + hidden record, the pattern `fix-report.mjs` and `rebuttal.mjs`
 * settled on. #690's lesson was written about exactly this shape: a marker-only
 * body shows a maintainer scrolling the PR "an empty-looking bot comment" while
 * something that matters plays out invisibly. A dispatch is the one event in the
 * loop that had no surface at all — the panel's verdict is on the commit's
 * checks and the fixer's account is its own comment, but nothing said "round 2
 * starts here", which is the connective tissue between them.
 *
 * One line, deliberately. The loop-status dashboard already carries the budget;
 * this exists to sit in the TIMELINE at the point the round began.
 */
export function renderFixDispatchComment({ from = "", prior = 0, round = null, max = null } = {}) {
  const record = serializeFixDispatch({ from, prior });
  const at = from ? ` on \`${String(from).slice(0, 9)}\`` : "";
  const of = Number.isInteger(round) && Number.isInteger(max) ? ` ${round} of ${max}` : "";
  return (
    `🔧 **Fix round${of}** — dispatching the fix agent${at}. ` +
    "The panel findings it is acting on are the `agent-review-*` check runs on that commit.\n\n" +
    record
  );
}

/**
 * Read one comment as a dispatch record, or `null`.
 *
 * `null` for ANY doubt — wrong author, no marker, non-JSON, wrong version. The
 * author gate is checked FIRST and is structural: `user.type === "Bot"` plus the
 * single reserved login. Unlike the latch predicates this takes no association
 * path, so there is no shape an ordinary account can present that passes.
 */
export function parseFixDispatchComment(comment) {
  const c = comment && typeof comment === "object" ? comment : {};
  const user = c.user && typeof c.user === "object" ? c.user : {};
  if (user.type !== "Bot" || user.login !== FIX_DISPATCH_AUTHOR_LOGIN) return null;
  const body = String(c.body ?? "");
  const m = new RegExp(`${FIX_DISPATCH_MARKER}([\\s\\S]*?) -->`).exec(body);
  if (!m) return null;
  let d;
  try {
    d = JSON.parse(m[1]);
  } catch {
    return null;
  }
  if (!d || typeof d !== "object" || d.v !== FIX_DISPATCH_VERSION) return null;
  const at = Date.parse(String(c.created_at ?? ""));
  return {
    from: typeof d.from === "string" ? d.from : "",
    prior: Number.isInteger(d.prior) && d.prior > 0 ? d.prior : 0,
    at: Number.isFinite(at) ? at : null,
  };
}

/** Every believable dispatch record on the PR, oldest first. */
export function collectFixDispatches(comments) {
  return (Array.isArray(comments) ? comments : [])
    .map(parseFixDispatchComment)
    .filter(Boolean)
    .sort((a, b) => (a.at ?? 0) - (b.at ?? 0));
}

/**
 * How much of the fix budget has this PR spent?
 *
 * Dispatch records are authoritative WHEN ANY EXIST; otherwise this is exactly
 * `countFailedReviewRounds`, so a PR opened before the ledger shipped keeps the
 * behaviour it started with rather than silently resetting to zero.
 *
 * THE BASELINE (`prior`) is what makes the hand-over exact instead of merely
 * generous. A PR mid-flight when this ships has already spent rounds that left
 * no record, so the first record carries the fallback count as it stood at that
 * moment and every later dispatch adds one. Without it such a PR would jump from
 * "3 spent" to "1 spent" and quietly earn two extra rounds.
 *
 * A `@claude rerun` that cuts the first record away drops the baseline WITH it —
 * a hand-back grants a fresh budget, and carrying a pre-rerun estimate past the
 * floor would spend it before the maintainer's first round.
 *
 * FAILS TOWARD PAGING, like every other reader here: a record that cannot be
 * parsed is dropped, which can only lower the count if it was ours — and an
 * unwritable record fails the guard step rather than being swallowed, so a
 * dispatch never happens unbudgeted.
 */
export function fixRoundsUsed(comments, commits, requiredCheckNames, { since = null } = {}) {
  const all = collectFixDispatches(comments);
  if (all.length === 0) return countFailedReviewRounds(commits, requiredCheckNames, { since });
  const s = Date.parse(String(since ?? ""));
  const sinceMs = Number.isFinite(s) ? s : null;
  // FAILS TOWARD INCLUDING, exactly as `fixAttemptCommits` does with an
  // undatable commit: a record whose timestamp cannot be read has not been shown
  // to predate the floor, and dropping it would GRANT a round rather than cost
  // one. `created_at` is always present on a real API comment, so this is a
  // fail-safe rather than a live path.
  const kept = sinceMs === null ? all : all.filter((d) => d.at === null || d.at > sinceMs);
  // The baseline belongs to the FIRST record; a rerun that cut it away took the
  // pre-rerun history with it.
  const baseline = kept.length === all.length ? all[0].prior : 0;
  return kept.length + baseline;
}

// --- convergence detection ---------------------------------------------------
//
// MAX_REVIEW_ROUNDS is a blunt backstop: on PR #521 the loop burned all five
// rounds re-litigating overlapping findings before anyone was paged. This
// detects that shape earlier — but the obvious implementation is wrong, so the
// reasoning matters:
//
//   Overlap between rounds is NOT evidence of a stall. The prior-findings
//   carry-forward in review-panel.mjs deliberately re-merges every unresolved
//   prior finding into the current round, so a HEALTHY PR that fixed 2 of 5
//   findings still shows 3 identical findings next round. Keying on overlap
//   alone would page on essentially every PR.
//
// What separates #521 from a healthy PR is that the blocking count did not go
// DOWN. So a pair of rounds "stalls" only when it is both highly overlapping
// AND not shrinking, and we require two consecutive such pairs before paging.
//
// Fuzzy matching lives here and ONLY here. It must never be pushed into
// dedupeFindings (review-panel.mjs), which DROPS findings — over-merging two
// distinct bugs there is exactly the fail-open its own comment warns about.
// This path only ever reports.

// Common English + review-boilerplate words carry no signal for "is this the
// same finding", and leaving them in inflates every Jaccard score toward 1.
const STOPWORDS = new Set([
  "the", "and", "not", "but", "for", "with", "without", "this", "that", "these", "those",
  "are", "was", "were", "been", "being", "has", "have", "had", "its", "it's", "from",
  "into", "than", "then", "there", "here", "when", "which", "what", "who", "whom",
  "can", "could", "should", "would", "may", "might", "will", "shall", "does", "did",
  "done", "only", "also", "any", "all", "each", "every", "some", "more", "most",
  "actually", "still", "just", "even", "because", "since", "while", "however",
]);

/**
 * Significant tokens of an LLM-written summary: camelCase / snake_case / dotted
 * and slashed identifiers are split apart, punctuation dropped, stopwords
 * removed, then sorted + deduped. The point is stability across rephrasings —
 * the panel routinely emits several different wordings of one defect (PR #564's
 * design-fit lens produced four), and an exact-text key would treat those as
 * four distinct findings.
 *
 * Tokens shorter than 3 chars are dropped as noise (`ts`, `g4`, bare digits).
 */
export function summaryTokens(summary) {
  return [
    ...new Set(
      String(summary ?? "")
        .replace(/([a-z0-9])([A-Z])/g, "$1 $2") // camelCase → camel Case
        .toLowerCase()
        .split(/[^a-z0-9]+/) // paths, dots, underscores, punctuation all separate
        .filter((t) => t.length > 2 && !STOPWORDS.has(t)),
    ),
  ].sort();
}

const trimmed = (s) => String(s ?? "").trim();

// A couple of incidentally-shared words is never a match, however short the
// shorter summary is. Calibrated below: real same-defect pairs share 7-17
// tokens; real different-defect pairs share at most 1.
export const MIN_SHARED_TOKENS = 3;

/** Default similarity threshold — see the calibration note on `findingSimilarity`. */
export const DEFAULT_SIMILARITY = 0.3;

/**
 * How alike are two findings, in [0, 1]? GATED first: a different lens or a
 * different file scores 0 outright. That gate is what stops "three distinct
 * bugs in one file, fixed one per round" from reading as the same finding three
 * times — similarity is only ever asked within a single (lens, file) pair.
 *
 * The metric is the OVERLAP (containment) coefficient — shared tokens over the
 * smaller token set — not Jaccard. The panel restates one defect at different
 * levels of detail, and Jaccard penalises the extra specifics in the longer
 * wording, which is precisely the wrong behaviour here.
 *
 * Calibrated against real data rather than guessed: PR #564's design-fit lens
 * emitted four wordings of ONE defect, measured against four unrelated real
 * findings from #564/#548.
 *
 *   metric    same-defect        different-defect
 *   jaccard   0.19 - 0.52        0.00 - 0.04
 *   dice      0.32 - 0.68        0.00 - 0.09
 *   overlap   0.39 - 0.71        0.00 - 0.08   <- widest margin
 *
 * Hence `DEFAULT_SIMILARITY = 0.3`: 1.3x under the lowest true positive and
 * 3.75x over the highest true negative. (An initial guess of 0.6 with Jaccard
 * would have matched none of the four real rephrasings.)
 *
 * When either token set degenerates to empty (a punctuation-only or
 * all-stopword summary, or coerceFindings' `(malformed finding …)`
 * placeholder), fall back to exact case-insensitive text equality so a
 * placeholder still matches itself across rounds rather than scoring 0.
 */
export function findingSimilarity(a, b) {
  if (!a || !b) return 0;
  if (trimmed(a.lens) !== trimmed(b.lens)) return 0;
  if (trimmed(a.file) !== trimmed(b.file)) return 0;
  const ta = summaryTokens(a.summary);
  const tb = summaryTokens(b.summary);
  if (ta.length === 0 || tb.length === 0) {
    return trimmed(a.summary).toLowerCase() === trimmed(b.summary).toLowerCase() ? 1 : 0;
  }
  const B = new Set(tb);
  let shared = 0;
  for (const t of ta) if (B.has(t)) shared++;
  if (shared < MIN_SHARED_TOKENS) return 0;
  return shared / Math.min(ta.length, tb.length);
}

/**
 * Fraction of `curr` findings that match ANY finding in `prev` at or above
 * `threshold`. Empty `curr` → 0, so a clean round never counts as a repeat.
 *
 * Matching is deliberately many-to-one: several current findings may all match
 * the same prior. A greedy one-to-one pairing was tried first and is wrong for
 * this question — the panel routinely emits multiple wordings of one defect
 * (PR #564's design-fit lens turned 2 priors into 4 rephrasings), and
 * consuming each prior once scored that 0.5, under-reporting the very case
 * where the loop is most obviously spinning. The question here is "how much of
 * this round is recycled prior material", and when four findings are four
 * wordings of two priors the honest answer is "all of it".
 *
 * The inflation risk that one-to-one guarded against is instead handled by the
 * caller's count test: a round that genuinely found NEW distinct problems has a
 * rising count of *unmatched* findings, and `detectStalledRounds` only treats a
 * pair as stalling when the count also fails to fall.
 */
export function repeatRatio(prev, curr, threshold = DEFAULT_SIMILARITY) {
  const p = Array.isArray(prev) ? prev : [];
  const c = Array.isArray(curr) ? curr : [];
  if (c.length === 0) return 0;
  const matched = c.filter((f) => p.some((q) => findingSimilarity(q, f) >= threshold)).length;
  return matched / c.length;
}

// The two synthetic summaries review-panel.mjs writes when a lens never
// produced a real verdict. Their blocking finding is an infrastructure signal,
// not a review finding, and two consecutive quota outages must not read as a
// stall — the guard already pages separately on the INFRA path.
const INFRA_SUMMARY = /^\s*(Review could not run|Reviewer did not produce a valid verdict)/i;

/**
 * Group the commits + check runs the round guard ALREADY fetched into review
 * rounds, oldest first. One round per commit carrying any of our lens checks;
 * `findings` is the union of each lens's LATEST run's persisted blocking
 * findings, tagged with its lens id.
 *
 * `partial: true` marks a round we could not fully read — a lens with no
 * machine-readable payload, unparseable JSON, or an infra/quota round. A
 * partial round breaks the stall run rather than contributing to it, so an
 * unreadable round can never manufacture a page.
 *
 * `commits`: Array<{ sha, checkRuns: Array<{name, app, output, started_at, completed_at}> }>
 */
export function groupReviewRounds(commits, lensCheckNames) {
  const names = new Set(lensCheckNames ?? []);
  const rounds = [];
  for (const c of Array.isArray(commits) ? commits : []) {
    const runs = (c?.checkRuns ?? []).filter(
      (r) => r && names.has(r.name) && r.app?.slug === "github-actions",
    );
    if (runs.length === 0) continue; // no panel verdict here → not a round
    // Latest run per lens: a re-run supersedes the earlier attempt.
    const latest = new Map();
    for (const r of runs) {
      const t = new Date(r.completed_at || r.started_at || 0).getTime();
      const cur = latest.get(r.name);
      if (!cur || t >= cur.t) latest.set(r.name, { run: r, t });
    }
    const findings = [];
    let partial = false;
    for (const [name, { run }] of latest) {
      const lens = name.replace(/^agent-review-/, "");
      const text = run.output?.text;
      // A CLEAN lens legitimately persists "[]", so an ABSENT payload is not
      // "found nothing" — it means we cannot see what this lens found.
      if (typeof text !== "string" || text === "") {
        partial = true;
        continue;
      }
      let parsed;
      try {
        parsed = JSON.parse(text);
      } catch {
        partial = true;
        continue;
      }
      if (!Array.isArray(parsed)) {
        partial = true;
        continue;
      }
      for (const f of parsed) {
        if (!f || typeof f !== "object" || INFRA_SUMMARY.test(String(f.summary ?? ""))) {
          partial = true;
          continue;
        }
        // `adjudication` rides along even though convergence never reads it:
        // review-round-guard.mjs reaches the rebuttal bound THROUGH this
        // projection, and a narrow projection that silently drops the field is
        // indistinguishable from "nothing was ever disputed" — the page simply
        // never fires. Inert for stall detection, which keys on lens/file/summary
        // and counts by severity.
        findings.push({
          lens,
          severity: f.severity,
          file: f.file,
          summary: f.summary,
          ...(f.adjudication ? { adjudication: f.adjudication } : {}),
          ...(Array.isArray(f.mergedFrom) ? { mergedFrom: f.mergedFrom } : {}),
        });
      }
    }
    rounds.push({ sha: c.sha, findings, partial });
  }
  return rounds;
}

/**
 * Is the fix loop re-litigating the same findings instead of converging?
 *
 * Walks CONSECUTIVE round pairs backwards from the newest. A pair stalls iff
 * the blocking count did NOT go down AND `repeatRatio` is at or above
 * `similarity` (default 0.3, calibrated on real findings). `minRepeats` consecutive stalling pairs (default 2, so three
 * rounds of evidence) is the trigger — earliest page at round 3, versus the
 * round-5 cap, while tolerating one noisy round.
 *
 * FAILS TOWARD NOT PAGING. Malformed input, a partial round, or too little
 * history all return `stalled: false`. This is the one place in this pipeline
 * where the safe default is "keep going": MAX_REVIEW_ROUNDS is still the
 * backstop, and a spurious page — summoning a human when the loop was in fact
 * converging — is the more expensive error.
 */
export function detectStalledRounds(rounds, { minRepeats = 2, similarity = DEFAULT_SIMILARITY } = {}) {
  const list = (Array.isArray(rounds) ? rounds : []).filter((r) => r && typeof r === "object");
  const base = { stalled: false, stalls: 0, rounds: list.length, repeated: [] };
  if (list.length < minRepeats + 1) return { ...base, reason: "too-few-rounds" };

  const count = (r) => (Array.isArray(r.findings) ? r.findings.length : 0);
  let stalls = 0;
  const repeated = new Map();

  for (let i = list.length - 1; i >= 1 && stalls < minRepeats; i--) {
    const curr = list[i];
    const prev = list[i - 1];
    if (curr.partial || prev.partial) break; // unreadable → end the trailing run
    if (count(curr) < count(prev)) break; // findings went down → real progress
    if (repeatRatio(prev.findings, curr.findings, similarity) < similarity) break;
    stalls++;
    for (const f of curr.findings) {
      if (!prev.findings.some((p) => findingSimilarity(p, f) >= similarity)) continue;
      repeated.set(`${f.lens}::${trimmed(f.file)}::${trimmed(f.summary).toLowerCase()}`, {
        lens: f.lens,
        file: f.file,
        summary: f.summary,
      });
    }
  }

  const stalled = stalls >= minRepeats;
  return {
    ...base,
    stalled,
    stalls,
    repeated: stalled ? [...repeated.values()] : [],
    reason: stalled ? "repeat-without-reduction" : stalls > 0 ? "partial-stall" : "progressing",
  };
}
