// The out-of-run safety net: find loop PRs that stopped moving with nobody
// told, and either restart them or page.
//
// WHY THIS EXISTS. Every other net in the pipeline runs INSIDE a workflow run —
// the panel's `stalled` job, `fix-report`'s no-commit page, the round guard. A
// net inside a run cannot see the run that never finished: `stalled` is
// `!cancelled()` on purpose (a superseded run must not page and latch the PR at
// the moment a fresher round starts), so when the concurrency group or a
// maintainer cancels the run that would have promoted or fixed a PR, and no
// fresher run follows, nothing fires at all. Two shapes were measured:
//
//   STUCK REVIEWING — #2108 sat in `agent:reviewing` for eight hours after a
//     🟢 panel round: the panel run that would have promoted it was cancelled,
//     no push followed, so no CI and no panel ran again. The label said a
//     round was in flight; nothing was.
//   USAGE WINDOW — a fixer or panel that dies on a closed usage window is paged
//     honestly (fix-outcome.mjs, the round guard's infra page, the panel's
//     no-live-credential page), and every one of those pages says the window
//     "reopens on its own; comment `@claude rerun` once it has". Which is a
//     human being asked to set an alarm for a machine.
//
// WHAT IT DOES, per open agent-managed PR, at most ONE action per sweep:
//
//   usage-retry   every latch on the PR is a usage-limit page this pipeline
//                 wrote, the backoff for this attempt has passed, and fewer
//                 than MAX_USAGE_RETRIES retries were made on this head. Clear
//                 those pages, drop `agent:blocked`, re-run the head's CI so the
//                 panel re-engages — the same mechanics `@claude rerun` uses,
//                 minus the budget reset (only a maintainer may grant budget).
//   usage-exhausted   the same, with the retries spent: say so once, leave the
//                 page in place.
//   retrigger     labelled `agent:reviewing`, no latch, nothing running for the
//                 head, and idle for STUCK_MINUTES: re-run the head's CI once.
//                 The panel reuses the verdicts already on the commit, so a 🟢
//                 round goes straight to promote and a 🔴 one to the fixer.
//   page          the same PR, still stuck STUCK_MINUTES after its retrigger:
//                 page a human, because restarting it again would only repeat.
//
// BOUNDED BY RECORDS, NOT MEMORY. Every action leaves a hidden
// `<!-- agent-sweep {...} -->` comment keyed on the head sha, written with the
// workflow token, and the next sweep reads them back by AUTHOR — a stranger who
// pastes the marker cannot spend or refund a retry. A push changes the head and
// so restarts the count, which is right: a new head is a new question.
//
// Usage: node loop-sweep.mjs run [--dry-run] [--pr <n>]
// Best-effort per PR: one PR's API failure is logged and the sweep moves on.

import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "./gh-checks.mjs";
import { isPagedLatchComment, PAGED_LATCH, PAGE_AUTHOR_LOGINS } from "./rounds.mjs";

export const SWEEP_MARKER = "<!-- agent-sweep ";
export const USAGE_MARKER = "<!-- agent-usage-limit ";
export const SWEEP_VERSION = 1;
/** The only author whose sweep and usage records count: the workflow token. */
export const SWEEP_AUTHOR_LOGINS = Object.freeze(["github-actions[bot]"]);
const CI_PAGED_LATCH = "<!-- agent-paged -->";
const HERE = path.dirname(fileURLToPath(import.meta.url));

/** Idle time before a reviewing PR counts as stuck. Longer than a panel (≤45 min) plus CI. */
export const STUCK_MINUTES = 90;
/** Retries per head after a usage-limit page, and the wait before each. */
export const MAX_USAGE_RETRIES = 3;
/**
 * Minutes after the page before retry N (0-based). Claude usage windows reset on
 * a schedule of hours; the classified page carries no reset time (the vocabulary
 * is closed on purpose, redact.mjs), so this backs off instead of guessing. An
 * early retry is cheap: the fixer's credential probe refuses a still-closed
 * window before any round is recorded, and the panel reuses its verdicts.
 */
export const USAGE_BACKOFF_MINUTES = Object.freeze([60, 180, 360]);
/** Infra codes that mean "a window will reopen", the only ones retried. */
export const RETRYABLE_CODES = Object.freeze(["USAGE_LIMIT", "RATE_LIMITED", "POOL_EXHAUSTED", "NO_LIVE_CREDENTIAL"]);

const str = (v) => (typeof v === "string" ? v : "");
const ms = (iso) => {
  const t = Date.parse(str(iso));
  return Number.isFinite(t) ? t : null;
};

/** The hidden marker a usage-limit page carries, for the page writers. */
export function usageLimitMarker({ code, at = new Date().toISOString() } = {}) {
  const c = RETRYABLE_CODES.includes(code) ? code : "USAGE_LIMIT";
  return `${USAGE_MARKER}${JSON.stringify({ v: SWEEP_VERSION, code: c, at })} -->`;
}

/** The infra code in a rendered `[CODE] reason` string, or "". */
export function infraCodeOf(text) {
  const m = /\[([A-Z_]+)\]/.exec(str(text));
  return m ? m[1] : "";
}

function parseMarker(body, marker) {
  const b = str(body);
  const i = b.indexOf(marker);
  if (i < 0) return null;
  const end = b.indexOf(" -->", i);
  if (end < 0) return null;
  try {
    const rec = JSON.parse(b.slice(i + marker.length, end));
    return rec && typeof rec === "object" && rec.v === SWEEP_VERSION ? rec : null;
  } catch {
    return null;
  }
}

/**
 * Is this comment a latch — review-side OR CI-side — from an author the rest of
 * the pipeline believes? The CI-side `<!-- agent-paged -->` is written by the
 * CI-fix arm with the App token (`yorkie-team-agent[bot]`) and can be written by
 * a maintainer by hand; `set-state.mjs`, `agent-rerun.yml` and
 * `agent-iterate-ci.yml` all trust those authors, so the sweep must too. Trusting
 * only the workflow bot here would read a real non-usage page as "no latch" and
 * let a usage retry unblock a PR a human owns. Same trust rule as
 * `isPagedLatchComment`, which already covers the review-side marker.
 */
const TRUSTED_LATCH_ASSOCIATIONS = new Set(["OWNER", "MEMBER", "COLLABORATOR"]);
export function isLatchComment(comment) {
  if (isPagedLatchComment(comment)) return true;
  const c = comment && typeof comment === "object" ? comment : {};
  if (!str(c.body).includes(CI_PAGED_LATCH)) return false;
  if (c.user?.type === "Bot" && PAGE_AUTHOR_LOGINS.includes(str(c.user.login))) return true;
  return TRUSTED_LATCH_ASSOCIATIONS.has(str(c.author_association));
}

const byWorkflowBot = (c) => c?.user?.type === "Bot" && SWEEP_AUTHOR_LOGINS.includes(str(c.user.login));

/** The usage-limit record on a comment, only when the workflow bot wrote it. */
export function usageRecordOf(comment) {
  if (!byWorkflowBot(comment)) return null;
  const rec = parseMarker(comment.body, USAGE_MARKER);
  return rec && RETRYABLE_CODES.includes(rec.code) ? rec : null;
}

/** Sweep records, by author, for one head. */
export function sweepRecordsFor(comments, sha) {
  return (Array.isArray(comments) ? comments : [])
    .filter(byWorkflowBot)
    .map((c) => ({ c, rec: parseMarker(c.body, SWEEP_MARKER) }))
    .filter(({ rec }) => rec && str(rec.sha) === str(sha))
    .map(({ c, rec }) => ({ ...rec, at: str(c.created_at) }));
}

export function renderSweepComment(rec, text) {
  const json = JSON.stringify({ v: SWEEP_VERSION, ...rec }).replace(/--/g, "-\\u002d");
  return `${SWEEP_MARKER}${json} -->\n${text}`;
}

/**
 * The whole decision for one PR, pure — this is what the tests pin.
 *
 * `runs` are the Actions runs for the PR's head sha (any workflow); `labels` are
 * names. Returns `{ action, reason, ... }`; `action: "none"` is the common case.
 */
export function planSweep({ pr, labels = [], comments = [], runs = [], now = Date.now(), stuckMinutes = STUCK_MINUTES } = {}) {
  const none = (reason) => ({ action: "none", reason });
  if (!pr || typeof pr !== "object") return none("no pr");
  const sha = str(pr.head?.sha);
  const sameRepo = str(pr.head?.repo?.full_name) !== "" && str(pr.head?.repo?.full_name) === str(pr.base?.repo?.full_name);
  const names = (Array.isArray(labels) ? labels : []).map((l) => (typeof l === "string" ? l : str(l?.name)));
  const managed = sameRepo && (str(pr.head?.ref).startsWith("agent/") || names.includes("agent:managed"));
  if (!managed || !sha) return none("not agent-managed");

  // Anything still running for this head is somebody else's job to finish.
  const list = Array.isArray(runs) ? runs : [];
  if (list.some((r) => str(r?.status) !== "completed")) return none("a run is in flight");

  const latches = (Array.isArray(comments) ? comments : []).filter(isLatchComment);
  const records = sweepRecordsFor(comments, sha);

  // --- usage-window retry ---------------------------------------------------
  if (latches.length > 0) {
    const usage = latches.map((c) => ({ c, rec: usageRecordOf(c) }));
    // EVERY latch must be a usage page. One page of any other kind means a human
    // owns the PR, and clearing the usage page beside it would hand the PR back
    // to the loop over that human's head.
    if (!usage.every((u) => u.rec)) return none("paged for a reason other than a usage window");
    const newest = usage.reduce((a, b) => ((ms(b.c.created_at) ?? 0) > (ms(a.c.created_at) ?? 0) ? b : a));
    const tries = records.filter((r) => r.kind === "usage-retry").length;
    if (tries >= MAX_USAGE_RETRIES) {
      return records.some((r) => r.kind === "usage-exhausted")
        ? none("usage retries exhausted (already said so)")
        : { action: "usage-exhausted", reason: `${tries} automatic retries on this head`, sha };
    }
    const pagedAt = ms(newest.c.created_at);
    const wait = USAGE_BACKOFF_MINUTES[Math.min(tries, USAGE_BACKOFF_MINUTES.length - 1)];
    if (pagedAt === null || now - pagedAt < wait * 60_000) return none(`usage retry ${tries + 1} waits ${wait} min after the page`);
    // The retry IS the CI re-run. Without a completed run to re-run, clearing
    // the page would leave a PR unlatched with nothing to move it: the sweep's
    // own comment cannot trigger `@claude rerun` (agent-rerun.yml ignores bot
    // comments), so the page and the label stay until there is a run.
    const run = ciRunToRerun(list);
    if (!run) return none("usage retry due, but no completed CI run to re-run; keeping the page");
    return {
      runId: run.id,
      action: "usage-retry",
      reason: `${newest.rec.code}, retry ${tries + 1} of ${MAX_USAGE_RETRIES}`,
      sha,
      attempt: tries + 1,
      clear: usage.map((u) => u.c.id).filter((id) => id !== undefined),
    };
  }

  // --- stuck in reviewing ---------------------------------------------------
  if (!names.includes("agent:reviewing")) return none("not reviewing");
  if (pr.draft === false) return none("already promoted");
  const lastRun = list.reduce((m, r) => Math.max(m, ms(r?.updated_at) ?? 0), 0);
  const idleSince = Math.max(lastRun, ms(pr.updated_at) ?? 0);
  if (!idleSince || now - idleSince < stuckMinutes * 60_000) return none("not idle long enough");
  const retriggers = records.filter((r) => r.kind === "retrigger");
  if (retriggers.length === 0) return { action: "retrigger", reason: `idle in agent:reviewing for ${Math.round((now - idleSince) / 60_000)} min`, sha };
  const lastRetrigger = Math.max(...retriggers.map((r) => ms(r.at) ?? 0));
  if (now - lastRetrigger < stuckMinutes * 60_000) return none("retriggered recently");
  return { action: "page", reason: "still idle in agent:reviewing after an automatic retrigger", sha };
}

/** The newest COMPLETED CI run for a head — the one `@claude rerun` re-runs, and why. */
export function ciRunToRerun(runs) {
  const ci = (Array.isArray(runs) ? runs : []).filter(
    (r) => /(^|\/)ci\.yml$/.test(str(r?.path)) && str(r?.status) === "completed",
  );
  return ci.reduce((n, r) => (n === null || r.id > n.id ? r : n), null);
}

// --- CLI ---------------------------------------------------------------------

function main() {
  const a = parseArgs(process.argv, { booleans: ["dry-run"] });
  if (a._[0] !== "run") {
    console.error("usage: loop-sweep.mjs run [--dry-run] [--pr <n>]");
    return;
  }
  const dry = a["dry-run"] === true;
  const gh = (args, input) => execFileSync("gh", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, ...(input ? { input } : {}) });
  const ghJson = (args) => JSON.parse(gh(args));
  let prs;
  try {
    prs = a.pr
      ? [ghJson(["api", `repos/{owner}/{repo}/pulls/${Number(a.pr)}`])]
      : ghJson(["api", "--paginate", "repos/{owner}/{repo}/pulls?state=open&per_page=100"]);
  } catch (e) {
    console.error(`loop-sweep: could not list pull requests (${e.message}).`);
    return;
  }
  for (const pr of prs) {
    try {
      sweepOne(pr, { gh, ghJson, dry });
    } catch (e) {
      console.error(`loop-sweep: #${pr?.number}: ${e.message}`);
    }
  }
}

function sweepOne(pr, { gh, ghJson, dry }) {
  const labels = (pr.labels || []).map((l) => l.name);
  const quick = planSweep({ pr, labels, runs: [], comments: [] });
  if (quick.reason === "not agent-managed") return;
  const comments = ghJson(["api", "--paginate", `repos/{owner}/{repo}/issues/${pr.number}/comments?per_page=100`]);
  const runs = ghJson(["api", `repos/{owner}/{repo}/actions/runs?head_sha=${pr.head.sha}&per_page=100`]).workflow_runs || [];
  const plan = planSweep({ pr, labels, comments, runs });
  console.error(`loop-sweep: #${pr.number}: ${plan.action} — ${plan.reason}`);
  if (plan.action === "none" || dry) return;
  const comment = (body) => gh(["pr", "comment", String(pr.number), "--body-file", "-"], body);
  const rerunCi = (runId = ciRunToRerun(runs)?.id) => {
    if (!runId) return false;
    gh(["api", "-X", "POST", `repos/{owner}/{repo}/actions/runs/${runId}/rerun`]);
    return true;
  };
  const short = plan.sha.slice(0, 9);
  if (plan.action === "usage-retry") {
    // Re-run FIRST: if it throws, nothing has been cleared and the page stands.
    // Clearing right after is safe — the re-run's completion, which is what the
    // panel's gate admits, is minutes away.
    rerunCi(plan.runId);
    for (const id of plan.clear) {
      try { gh(["api", "-X", "DELETE", `repos/{owner}/{repo}/issues/comments/${id}`]); } catch (e) { console.error(`  could not delete ${id}: ${e.message}`); }
    }
    try { gh(["api", "-X", "DELETE", `repos/{owner}/{repo}/issues/${pr.number}/labels/agent:blocked`]); } catch { /* not set */ }
    comment(renderSweepComment(
      { kind: "usage-retry", sha: plan.sha, attempt: plan.attempt },
      `🔁 Automatic retry ${plan.attempt} of ${MAX_USAGE_RETRIES} on \`${short}\` after a closed usage window (${plan.reason}). ` +
        "Re-ran CI; the panel re-engages and reuses the verdicts already on this commit." +
        " This does not restart the fix budget; only a maintainer's `@claude rerun` does.",
    ));
  } else if (plan.action === "usage-exhausted") {
    comment(renderSweepComment(
      { kind: "usage-exhausted", sha: plan.sha },
      `⏸️ ${MAX_USAGE_RETRIES} automatic retries after closed usage windows did not get \`${short}\` through. The page above stands; comment \`@claude rerun\` once capacity is back, or register more \`CLAUDE_CODE_OAUTH_TOKEN_N\` secrets.`,
    ));
  } else if (plan.action === "retrigger") {
    const reran = rerunCi();
    comment(renderSweepComment(
      { kind: "retrigger", sha: plan.sha },
      `🔁 This PR was ${plan.reason} with nothing running for \`${short}\` — the run that should have moved it on was most likely cancelled. ` +
        (reran ? "Re-ran CI so the panel re-engages; it reuses the verdicts already on this commit." : "There was no completed CI run to re-run."),
    ));
  } else if (plan.action === "page") {
    comment(renderSweepComment(
      { kind: "page", sha: plan.sha },
      `${PAGED_LATCH}\n🛑 This PR is ${plan.reason}: the loop restarted it once and it stopped again with nothing running. **A human should look.** The review panel will not run again on this PR by itself; comment \`@claude rerun\` to restart it.`,
    ));
    // The single-value state machine owns the label: reconcile re-derives
    // `blocked` from the latch just posted, as every other page path does.
    try {
      execFileSync(process.execPath, [path.join(HERE, "set-state.mjs"), "reconcile", String(pr.number)], { stdio: "inherit" });
    } catch (e) {
      console.error(`  could not reconcile the state label: ${e.message}`);
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
