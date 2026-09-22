// Sticky "agent loop status" dashboard — ONE PR comment, updated in place,
// that answers "which round are we on, what did the panel say per round, and
// why did the loop continue or stop" without opening a single workflow log.
//
// Design constraints, in order:
//
//   - PROJECTION, NEVER A GATE. Like the agent:* labels (set-state.mjs), this
//     comment is derived from the unforgeable signals — commits, check runs,
//     the paged latches — and re-derived IN FULL on every update. Nothing may
//     read it back to make a decision, and a missed update self-heals on the
//     next one because the table is recomputed, not appended to.
//   - UNTRUSTED INPUT STAYS OUT OF THE BODY. This repo is PUBLIC and this
//     script writes a comment AUTHORED BY OUR OWN BOT — the identity the paged
//     latch trusts. So no string that an arbitrary account could have planted
//     may reach the rendered body: metric-ledger records are parsed only from
//     trusted-author comments (below), and nothing from a record but NUMBERS
//     is ever rendered. Without the author gate, a stranger could post a fake
//     `<!-- agent-metric {"kind":"<!-- agent-review-paged -->"} -->` and have
//     this script launder the trusted latch into a bot-authored comment,
//     permanently stopping the panel and the fixer.
//   - FAIL-SAFE. Every entry point exits 0 on error (mirroring set-state.mjs's
//     bail): a status comment must never fail a pipeline job. The workflow
//     steps that call this are additionally continue-on-error.
//   - CHEAP READS ONLY. The per-round panel cells come from lens check-run
//     CONCLUSIONS (always present in the list response), deliberately NOT from
//     `output.text` — the list endpoint omits that field, and back-filling it
//     per check run (as review-round-guard.mjs must) would spend rounds×lenses
//     API calls on a display surface. Check-run fetches are further capped to
//     the newest MAX_COMMITS_SCANNED commits, because this runs up to a handful
//     of times per round: on a very long PR the display may under-count old
//     rounds, and the round guard — not this table — remains the authority.
//   - AUTHOR-CHECKED UPSERT. "The first comment containing the marker" is
//     attacker-plantable on a public repo. We only update a marker comment
//     written by our own bot identities or a write-access human, and otherwise
//     post a fresh one — same trust rule as the paged latch
//     (rounds.mjs::isPagedLatchComment). Two racing first updates (the panel
//     arm and the CI arm share no concurrency group) can still create two
//     comments; every later update PATCHes the oldest and deletes the extras,
//     so a duplicate survives at most until the next update.
//
// Usage (GH_TOKEN must be set; all flags optional):
//   node ./scripts/agent/loop-status.mjs update <pr>
//     [--event panel|fix-dispatched|fix-pushed|held|promoted|not-promoted|paged|ci-paged|ci-fix-pushed|on-demand-fix|rerun]
//     [--note "<one-line explanation of the latest decision>"]
//     [--run-url <url>] [--max-rounds N] [--lenses <path>] [--dry-run]
//
// Every derived value must be CALLER-INDEPENDENT — the body is recomputed in
// full on each update, and if two arms recompute different numbers the single
// surface this exists to make legible flaps instead of converging. Hence:
// the round count calls the SAME reader the guard gates on (`fixRoundsUsed`,
// passed the full lens manifest for its fallback path), and the cap
// defaults to DEFAULT_MAX_REVIEW_ROUNDS (pinned to the panel workflow's env)
// so an arm that does not own the env renders the same "N of M".

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  collectFixDispatches,
  fixRoundsUsed,
  rerunPointFrom,
  PAGED_LATCH,
  PAGE_AUTHOR_LOGINS,
} from "./rounds.mjs";
import { DEFERRED_CHECK_NAME } from "./deferred-findings.mjs";
import { collectFixReports } from "./fix-report.mjs";
import { collectRebuttals } from "./rebuttal.mjs";
import { emitBestEffortWarning } from "./guard-verdict.mjs";
import {
  gh,
  ghJson,
  listAllComments,
  parseMetricComment,
  parseSummaryData,
  dedupRecords,
  formatUsd,
  formatMinutes,
} from "./metrics.mjs";

export const LOOP_STATUS_MARKER = "<!-- agent-loop-status -->";

/**
 * The CI arm's paged latch (agent-iterate-ci.yml), distinct from rounds.mjs's
 * review-side PAGED_LATCH. The projection must recognise BOTH — a PR the CI-fix
 * loop paged is just as handed-to-a-human as one the review guard paged.
 * agent-iterate-ci.yml carries the literal; loop-status.test.mjs pins this copy.
 */
export const CI_PAGED_LATCH = "<!-- agent-paged -->";

/** Keep the table bounded — GitHub caps comment bodies at 65,536 chars. */
export const MAX_ROUND_ROWS = 15;

/**
 * Default fix-round cap for the "N of M" budget line, used when the caller
 * does not pass --max-rounds (the CI arm, `@claude fix`, `@claude rerun` — arms
 * that do not own the panel workflow's env). LITERAL COPY of
 * `MAX_REVIEW_ROUNDS: "3"` in agent-review-panel.yml; loop-status.test.mjs
 * asserts the two match, so a cap change cannot leave this rendering stale.
 * Display only — the guard reads the env, never this.
 */
export const DEFAULT_MAX_REVIEW_ROUNDS = 3;

/** Newest commits whose check runs are fetched (one API call each; see header). */
export const MAX_COMMITS_SCANNED = 40;

const TRUSTED_ASSOCIATIONS = new Set(["OWNER", "MEMBER", "COLLABORATOR"]);

/** Same trust rule as rounds.mjs::isPagedLatchComment, marker-independent. */
function isTrustedAuthor(comment) {
  const c = comment && typeof comment === "object" ? comment : {};
  const user = c.user && typeof c.user === "object" ? c.user : {};
  if (user.type === "Bot" && PAGE_AUTHOR_LOGINS.includes(user.login)) return true;
  return TRUSTED_ASSOCIATIONS.has(String(c.author_association ?? ""));
}

/**
 * Is this PR latched as handed-to-a-human, by EITHER arm's marker? Author-
 * checked for the same reason as rounds.mjs::isPagedLatchComment (public repo).
 */
export function isAnyPagedLatchComment(comment) {
  const body = String((comment ?? {}).body ?? "");
  if (!body.includes(PAGED_LATCH) && !body.includes(CI_PAGED_LATCH)) return false;
  return isTrustedAuthor(comment);
}

/**
 * May this comment's metric-ledger payload be parsed at all? The ledger is
 * written only by our own workflows (GITHUB_TOKEN or the App token), so any
 * marker in an untrusted comment is a plant — see the header's injection note.
 */
export function isTrustedLedgerComment(comment) {
  return isTrustedAuthor(comment);
}

/**
 * Is this an update-able status comment of OURS? The comment is PATCHed — and
 * a racing duplicate DELETED — by the upsert, so this must identify only
 * comments this script itself posted, never a comment that merely CONTAINS the
 * marker. GitHub's quote-reply copies raw markdown, hidden HTML comments
 * included, so a maintainer (trusted author!) quoting the dashboard produces a
 * marker-bearing comment that a containment test would overwrite or delete —
 * and if that comment also carried a paged latch, deleting it would silently
 * clear the loop's human hand-off. Three conditions:
 *   1. the body STARTS with the marker — the renderer always emits it as
 *      line 1, while a quote-reply starts with ">" and a page comment starts
 *      with its own latch marker;
 *   2. trusted author (marker alone is plantable on a public repo — same
 *      reasoning as isPagedLatchComment);
 *   3. no paged latch anywhere in the body — our renderer neutralizes markers
 *      (see neutralizeHiddenMarkers), so a live latch means this is NOT our
 *      comment, and a comment that pages a human must never be deleted.
 */
export function isOwnStatusComment(comment) {
  const c = comment && typeof comment === "object" ? comment : {};
  const body = String(c.body ?? "");
  if (!body.trimStart().startsWith(LOOP_STATUS_MARKER)) return false;
  if (body.includes(PAGED_LATCH) || body.includes(CI_PAGED_LATCH)) return false;
  return isTrustedAuthor(c);
}

/**
 * Break every hidden HTML-comment opener in interpolated text so nothing this
 * script renders can carry a live trusted marker. The body is authored by the
 * SAME bot identity the paged latch trusts, so a marker that survives into it
 * — through a note, a future field, anything — would be laundered into a
 * trusted latch (or a second status comment, or a fake metric record). The
 * ZWNJ-split rendering is the same neutering fix-report.mjs uses for marker
 * text inside its visible prose. Pure.
 */
export function neutralizeHiddenMarkers(text) {
  return String(text ?? "").replace(/<!--/g, "<!-‌-");
}

/** conclusion values that read as "red" for the non-lens checks cell. */
const RED_CONCLUSIONS = new Set(["failure", "timed_out", "cancelled", "action_required"]);

/** null/undefined/"" mean "not measured", NOT zero — Number(null) is 0, which
 * once rendered a budget that was never counted as a confident "N of 0". */
const n = (v) =>
  v === null || v === undefined || v === "" ? null : Number.isFinite(Number(v)) ? Number(v) : null;

/**
 * Group a PR's commits into panel rounds for display: one row per commit that
 * carries at least one of our lens check runs (latest run per lens wins, same
 * rule as rounds.mjs::groupReviewRounds), oldest first. Pure.
 *
 * `commits`: Array<{ sha, commit?, checkRuns: Array<{name, app, status, conclusion, started_at, completed_at}> }>
 */
export function buildRounds(commits, lensCheckNames) {
  const names = new Set(lensCheckNames ?? []);
  const rounds = [];
  for (const c of Array.isArray(commits) ? commits : []) {
    const runs = (c?.checkRuns ?? []).filter((r) => r && r.app?.slug === "github-actions");
    const lensRuns = runs.filter((r) => names.has(r.name));
    if (lensRuns.length === 0) continue; // no panel verdict here → not a round
    const latest = new Map();
    for (const r of lensRuns) {
      const t = new Date(r.completed_at || r.started_at || 0).getTime();
      const cur = latest.get(r.name);
      if (!cur || t >= cur.t) latest.set(r.name, { run: r, t });
    }
    const lenses = [...latest.entries()].map(([name, { run }]) => ({
      lens: name.replace(/^agent-review-/, ""),
      status: run.status,
      conclusion: run.conclusion ?? null,
    }));
    // Everything that is not a lens check — CI's own job checks, mostly.
    //
    // `agent-deferred-findings` is excluded explicitly. It is written by the panel
    // itself, always `neutral`, and `neutral` is not in `RED_CONCLUSIONS` — so on a
    // commit where CI has not reported yet it would be the only member of this bucket
    // and the cell would read a confident ✅ for a CI run that never happened. An
    // advisory record of what the panel deferred is not evidence about CI.
    const others = runs.filter((r) => !names.has(r.name) && r.name !== DEFERRED_CHECK_NAME);
    let checks = "none";
    if (others.length > 0) {
      if (others.some((r) => RED_CONCLUSIONS.has(String(r.conclusion)))) checks = "failure";
      else if (others.some((r) => r.status !== "completed")) checks = "pending";
      else checks = "success";
    }
    rounds.push({ sha: String(c.sha ?? ""), lenses, checks });
  }
  return rounds;
}

const CHECKS_CELL = { success: "✅", failure: "❌", pending: "⏳", none: "—" };

/** One table cell summarizing a round's lens conclusions. Pure. */
export function lensCell(lenses) {
  const list = Array.isArray(lenses) ? lenses : [];
  if (list.length === 0) return "—";
  const failed = list.filter((l) => l.status === "completed" && l.conclusion === "failure");
  const pending = list.filter((l) => l.status !== "completed");
  const passed = list.filter((l) => l.status === "completed" && l.conclusion === "success");
  if (failed.length > 0) {
    const namesTxt = failed.map((l) => l.lens).sort().join(", ");
    const rest = pending.length > 0 ? `, ${pending.length} ⏳` : passed.length > 0 ? ` (${passed.length} ✅)` : "";
    return `❌ ${namesTxt}${rest}`;
  }
  if (pending.length > 0) return `⏳ running (${pending.length} of ${list.length})`;
  if (passed.length === list.length) return `✅ all ${list.length}`;
  // SUPERSEDED, not "neutral". A round whose panel was cancelled by the
  // concurrency guard has its lenses closed `cancelled` (see close-stuck-checks),
  // and those fall through every branch above into the trailing one — which
  // rendered the honest-but-unreadable "➖ 0 ✅ / 6 neutral". The distinction is
  // already load-bearing for the round count; it should read that way too.
  const superseded = list.filter((l) => l.status === "completed" && l.conclusion === "cancelled");
  if (superseded.length > 0 && superseded.length + passed.length === list.length) {
    return passed.length > 0 ? `⚪ superseded (${passed.length} ✅)` : "⚪ superseded";
  }
  return `➖ ${passed.length} ✅ / ${list.length - passed.length} neutral`;
}

/**
 * What did the fix agent do about round `sha`? One table cell. Pure.
 *
 * The dashboard could already say what the PANEL decided and never what the
 * FIXER did about it, so "0 disputed" — the answer to "is the rebuttal channel
 * even alive" — had no surface at all. Zero rebuttals have been filed across
 * every agent PR to date, and until this cell existed that was indistinguishable
 * from a broken channel.
 *
 * Three states, and the difference between the first two matters: a round the
 * fixer was never sent in for (the guard paged, or the panel approved) reads `—`,
 * while a round it WAS sent in for and never reported on reads as dispatched.
 * Collapsing them would hide a fixer that ran and said nothing.
 *
 * `dispatches` are `rounds.mjs::collectFixDispatches` records (`from`, `at`),
 * `reports` are `fix-report.mjs::collectFixReports` (`head`, `fixed`, `skipped`),
 * `rebuttals` are `rebuttal.mjs::collectRebuttals` (`createdAt`). Reports and
 * dispatches bind to a round by SHA. Rebuttals carry no sha — they name a
 * finding, not a commit — so they are attributed to the newest dispatch at or
 * before the rebuttal was written, which is the round the fixer was working when
 * it filed one.
 */
export function fixerCell(sha, { dispatches = [], reports = [], rebuttals = [] } = {}) {
  const ds = (Array.isArray(dispatches) ? dispatches : []).filter((d) => d && d.from);
  const mine = ds.find((d) => d.from === sha);
  if (!mine) return "—";

  const at = Number.isFinite(mine.at) ? mine.at : null;
  const nextAt = ds
    .map((d) => d.at)
    .filter((t) => Number.isFinite(t) && at !== null && t > at)
    .sort((a, b) => a - b)[0];
  const disputes = (Array.isArray(rebuttals) ? rebuttals : []).filter((r) => {
    const t = Date.parse(String(r?.createdAt ?? ""));
    if (!Number.isFinite(t) || at === null) return false;
    return t >= at && (nextAt === undefined || t < nextAt);
  }).length;

  const report = (Array.isArray(reports) ? reports : []).filter((r) => r && r.head === sha).pop();
  if (!report) return disputes > 0 ? `🔧 dispatched · ${disputes} disputed` : "🔧 dispatched";
  const fixed = Array.isArray(report.fixed) ? report.fixed.length : 0;
  const skipped = Array.isArray(report.skipped) ? report.skipped.length : 0;
  return `${fixed} fixed · ${skipped} skipped · ${disputes} disputed`;
}

/**
 * Cost/duration totals from the (trusted-author) metric ledger. Records folded
 * into the summary's data block carry no timestamp, which is fine: totals only.
 * Pure. NOTE: only the numeric totals may be rendered — record STRINGS (kind
 * included) never reach the comment body; see the injection note in the header.
 */
export function summarizeEffort(records) {
  let totalUsd = 0;
  let totalMs = 0;
  let sessions = 0;
  for (const r of Array.isArray(records) ? records : []) {
    if (!r || typeof r !== "object") continue;
    totalUsd += Number(r.costUsd) || 0;
    totalMs += Number(r.durationMs) || 0;
    sessions += 1;
  }
  return { totalUsd, totalMs, sessions };
}

const EVENT_HEADLINE = {
  panel: "review panel finished",
  "fix-dispatched": "fix round in progress",
  "fix-pushed": "fix pushed — next round starts with CI",
  held: "holding — see the latest note",
  promoted: "ready for human review",
  "not-promoted": "approved, but not promoted — see the run",
  paged: "🛑 paged to a human",
  "ci-paged": "🛑 paged to a human",
  "ci-fix-pushed": "CI fix pushed — CI re-running",
  "on-demand-fix": "on-demand fix finished — see the outcome comment",
  rerun: "loop re-engaged — CI re-running",
};

/**
 * Render the full comment body. Pure; `now` injected for testability.
 * State precedence: a trusted paged latch beats everything (a paged PR must
 * never read as "in progress"), then the caller's EVENT, then `ready`. The
 * event outranks `ready` because it reflects the action happening NOW: `ready`
 * is a static derivation (non-draft + `agent/` branch — the pipeline only
 * un-drafts via promotion; `agent:managed` human PRs are never drafts and must
 * not read as promoted), and a promoted PR that re-enters a review round would
 * otherwise read "ready for human review" forever, on every later update.
 */
export function renderLoopStatus({
  rounds = [],
  failedRounds = null,
  maxRounds = null,
  paged = false,
  ready = false,
  rerunAt = null,
  effort = null,
  event = "",
  note = "",
  runUrl = "",
  // `<server>/<owner>/<repo>/pull/<n>/commits`, derived in main() from the
  // Actions env rather than taken as a CLI flag: renderLoopStatus has six call
  // sites across four workflows, and this module requires every derived value to
  // be caller-independent (see the header). A flag some arms passed and others
  // did not would make the links appear and disappear between updates.
  commitBase = "",
  // sha → the Fixer cell for that round, from `fixerCell`.
  fixer = {},
  now = "",
} = {}) {
  const headline = paged
    ? "🛑 paged to a human"
    : EVENT_HEADLINE[event] ?? (ready ? "ready for human review" : "running");
  const lines = [LOOP_STATUS_MARKER, `## 🔄 Agent loop status — ${headline}`, ""];

  // The cap may be unknown (callers outside the panel workflow don't own
  // MAX_REVIEW_ROUNDS) — then render the count alone rather than dropping the
  // line ("N of 0" was the bug; dropping it erased the budget on every CI-arm
  // update).
  const failed = n(failedRounds);
  const max = n(maxRounds);
  if (failed !== null) {
    lines.push(
      `**Fix rounds dispatched:** ${failed}${max !== null ? ` of ${max}` : ""}` +
        (rerunAt ? ` (counted since the last \`@claude rerun\`)` : ""),
    );
  }
  if (note) lines.push(`**Latest:** ${String(note).replace(/\r?\n/g, " ").slice(0, 600)}`);
  lines.push("");

  if (rounds.length === 0) {
    lines.push("_No panel rounds yet — the review panel runs alongside the first CI run._", "");
  } else {
    const shown = rounds.slice(-MAX_ROUND_ROWS);
    const omitted = rounds.length - shown.length;
    lines.push(
      "| Round | Head | Other checks | Review panel | Fixer | Then |",
      "|---|---|---|---|---|---|",
    );
    // Newest first: the row a reader wants is the current one. `shown` is a
    // contiguous suffix of `rounds`, so every non-newest row has a successor.
    for (let i = shown.length - 1; i >= 0; i--) {
      const r = shown[i];
      const roundNo = rounds.length - shown.length + i + 1;
      const isNewest = i === shown.length - 1;
      // Same precedence as the headline: paged, then the event, then ready.
      const then = isNewest
        ? paged
          ? "🛑 paged"
          : event === "fix-dispatched"
            ? "🔧 fixer running…"
            : event === "fix-pushed" || event === "ci-fix-pushed"
              ? "🔧 fix pushed"
              : event === "promoted" || (ready && !EVENT_HEADLINE[event])
                ? "🤝 promoted"
                : "…"
        : `→ \`${shown[i + 1].sha.slice(0, 9)}\``;
      // The head cell is a LINK to that round's checks. The verdicts of record
      // live there, one set per commit, and GitHub's Checks tab only ever shows
      // the head commit's — so before this the table named a round it gave no way
      // to open. Degrades to today's code span when the repo is unknown.
      const short = r.sha.slice(0, 9);
      const head = commitBase ? `[\`${short}\`](${commitBase}/${r.sha})` : `\`${short}\``;
      lines.push(
        `| ${roundNo} | ${head} | ${CHECKS_CELL[r.checks] ?? "—"} | ${lensCell(r.lenses)} | ${fixer[r.sha] ?? "—"} | ${then} |`,
      );
    }
    if (omitted > 0) lines.push("", `_(${omitted} earlier round(s) omitted — see the check runs on older commits.)_`);
    lines.push("");
  }

  // Totals only — the per-kind/per-session breakdown belongs to the metrics
  // effort summary comment, which already owns that surface. Numbers only; no
  // record string is rendered (see the injection note in the header).
  if (effort && effort.sessions > 0) {
    lines.push(
      `**Agent effort so far:** ${formatUsd(effort.totalUsd)} · ~${formatMinutes(effort.totalMs)} across ${effort.sessions} session(s) — breakdown in the effort summary comment.`,
      "",
    );
  }

  lines.push(
    `<sub>Verdicts of record are the \`agent-review-*\` check runs on each commit — this table only summarizes their conclusions. A "round" is a commit that received panel verdicts, oldest = 1. Updated ${now}${runUrl ? ` · [latest run](${runUrl})` : ""}</sub>`,
  );
  // Everything after our own leading marker is neutralized so no interpolated
  // string (the note today, any field tomorrow) can carry a live hidden marker
  // into a body authored by the trusted bot identity.
  const body = lines.join("\n");
  return LOOP_STATUS_MARKER + neutralizeHiddenMarkers(body.slice(LOOP_STATUS_MARKER.length));
}

// --- gh-backed CLI -----------------------------------------------------------

function bail(msg) {
  console.error(`loop-status: ${msg} (continuing without a status update)`);
  // Exit-0 by design, so the step never shows failed — the warning is the one
  // trace that the dashboard a maintainer is about to read went stale here.
  emitBestEffortWarning(`loop-status failed: ${msg} — the loop-status dashboard comment may be stale`);
  process.exit(0);
}

function lensCheckNamesFrom(lensesPath) {
  const p =
    lensesPath || path.join(path.dirname(fileURLToPath(import.meta.url)), "lenses", "lenses.json");
  const manifest = JSON.parse(readFileSync(p, "utf8"));
  return manifest.map((l) => `agent-review-${l.id}`);
}

// Object-wrapped-array endpoint — --paginate --slurp then flatten, same as
// review-round-guard.mjs::checkRunsFor (see its comment for why).
function checkRunsFor(sha) {
  const pages = ghJson([
    "api",
    `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`,
    "--paginate",
    "--slurp",
  ]);
  return pages.flatMap((p) => p.check_runs ?? []);
}

function parseArgs(argv) {
  const args = { flags: {} };
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--dry-run") args.flags.dryRun = true;
    else if (a.startsWith("--")) {
      args.flags[a.slice(2)] = argv[++i] ?? "";
    } else positional.push(a);
  }
  args.command = positional[0];
  args.pr = Number(positional[1]);
  return args;
}

function main() {
  const { command, pr, flags } = parseArgs(process.argv.slice(2));
  if (command !== "update" || !Number.isInteger(pr) || pr <= 0) {
    console.error(
      "Usage: node ./scripts/agent/loop-status.mjs update <pr> [--event <e>] [--note <n>] [--run-url <u>] [--max-rounds N] [--lenses <path>] [--dry-run]",
    );
    process.exit(2);
  }

  let body;
  try {
    const lensCheckNames = lensCheckNamesFrom(flags.lenses);
    const prView = ghJson(["pr", "view", String(pr), "--json", "isDraft,headRefName"]);
    const comments = listAllComments(pr);

    const commits = ghJson([
      "api",
      `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`,
      "--paginate",
    ]);
    // Cost cap: one check-runs call per commit, and this script runs several
    // times per round. Older commits keep empty checkRuns, so a >40-commit PR
    // may under-display old rounds/counts — the guard stays authoritative.
    for (const c of commits) c.checkRuns = [];
    for (const c of commits.slice(-MAX_COMMITS_SCANNED)) c.checkRuns = checkRunsFor(c.sha);

    const paged = comments.some(isAnyPagedLatchComment);
    const rerunAt = rerunPointFrom(comments);
    const rounds = buildRounds(commits, lensCheckNames);
    // The SAME reader the guard gates on, so the dashboard cannot disagree with
    // the decision it reports. On a PR with a dispatch ledger the lens set is not
    // consulted at all — the records answer it — which makes this trivially
    // caller-independent. On the fallback path the old argument still holds and
    // is why the FULL manifest is passed rather than a per-caller set:
    // required_checks is derived from the CUMULATIVE changed-file list (the panel
    // keeps it unfiltered precisely so a lens that failed can never stop being
    // required), so manifest ⊇ required only by lenses that never fail — and a
    // lens that never fails contributes nothing to a count of commits carrying
    // FAILING lens runs. A per-caller set would make this number flap between
    // arms on the one surface built to converge.
    const failedRounds = fixRoundsUsed(comments, commits, lensCheckNames, { since: rerunAt });

    // `ready` means "passed the ready gate", not "is not a draft": the promote
    // job is the only thing that un-drafts an agent/ branch, but an
    // `agent:managed` human PR was never a draft and must not read as promoted.
    const ready =
      flags.event === "promoted" ||
      (prView.isDraft === false && String(prView.headRefName ?? "").startsWith("agent/"));

    // Metric ledger, TRUSTED AUTHORS ONLY — see the injection note in the header.
    const records = dedupRecords(
      comments.filter(isTrustedLedgerComment).flatMap((c) => {
        const one = parseMetricComment(c.body);
        return one ? [one] : parseSummaryData(c.body);
      }),
    );

    // What the FIXER did per round, from records already on the PR — no extra
    // API calls, `comments` is in hand.
    const dispatches = collectFixDispatches(comments);
    const reports = collectFixReports(comments);
    const rebuttals = collectRebuttals(comments);
    const fixer = Object.fromEntries(
      rounds.map((r) => [r.sha, fixerCell(r.sha, { dispatches, reports, rebuttals })]),
    );

    // Derived here, not passed in: see the `commitBase` note on renderLoopStatus.
    // Empty outside Actions, which degrades the head cell to a code span.
    const repo = process.env.GITHUB_REPOSITORY;
    const commitBase = repo
      ? `${process.env.GITHUB_SERVER_URL || "https://github.com"}/${repo}/pull/${pr}/commits`
      : "";

    body = renderLoopStatus({
      rounds,
      failedRounds,
      maxRounds: n(flags["max-rounds"]) ?? DEFAULT_MAX_REVIEW_ROUNDS,
      paged,
      ready,
      rerunAt,
      commitBase,
      fixer,
      effort: summarizeEffort(records),
      event: flags.event ?? "",
      note: flags.note ?? "",
      runUrl: flags["run-url"] ?? "",
      now: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    });
  } catch (e) {
    bail(`could not build the status body: ${e.message}`);
  }

  if (flags.dryRun) {
    console.log(body);
    return;
  }

  try {
    // Re-list comments immediately before the write: the build above spends an
    // unbounded number of check-run calls, and deciding create-vs-update on a
    // snapshot that old is how two racing first updates each POST. The window
    // cannot be closed from here (no cross-workflow lock exists), so the
    // fallback is self-healing: PATCH the OLDEST own comment and delete any
    // younger duplicates a race left behind.
    const fresh = listAllComments(pr)
      .filter(isOwnStatusComment)
      .sort((a, b) => Number(a.id) - Number(b.id));
    if (fresh.length > 0) {
      gh([
        "api",
        "-X",
        "PATCH",
        `repos/{owner}/{repo}/issues/comments/${fresh[0].id}`,
        "-f",
        `body=${body}`,
      ]);
      for (const dup of fresh.slice(1)) {
        try {
          gh(["api", "-X", "DELETE", `repos/{owner}/{repo}/issues/comments/${dup.id}`]);
          console.error(`loop-status: deleted duplicate status comment ${dup.id}`);
        } catch {
          /* best-effort — the next update retries */
        }
      }
      console.log(`loop-status: updated comment ${fresh[0].id} on PR #${pr}`);
    } else {
      gh(["api", "-X", "POST", `repos/{owner}/{repo}/issues/${pr}/comments`, "-f", `body=${body}`]);
      console.log(`loop-status: posted a new status comment on PR #${pr}`);
    }
  } catch (e) {
    bail(`could not upsert the status comment: ${e.message}`);
  }
}

// Import-safe: only run the CLI when executed directly (so tests can import
// the pure functions without touching gh).
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
