// One PR comment per review round, posted by the panel job itself.
//
// THE GAP. The autonomous panel records its verdicts ONLY as `agent-review-*`
// check runs, one set per commit. The triage renderer that makes those findings
// readable — `review-comment.mjs` — is wired only into `agent-review-on-demand.yml`,
// which posts no checks. So the two arms are complementary and neither is
// complete: `@claude review` gives you a comment and no gate, the autonomous
// panel gives you a gate and no comment. Measured across 19 `agent/*` PRs, panel
// comments = 0 on every autonomous one.
//
// The findings were never thin — #737 round 3's `design-fit` body carries a
// verdict headline, a blocking finding with verifier confidence, and two minors.
// They were parked three clicks away, on an OLD commit's check list, while the
// fix agent's account of what it did about them sat in the conversation. A
// maintainer could read the answer and not the question.
//
// A PROJECTION, NEVER A GATE, on the same terms as `loop-status.mjs`: rendered
// from `.agent-review/` (the files the orchestrator just wrote) and the check
// runs, read back by nothing, and fail-safe — every entry point exits 0, because
// a comment must never fail the panel.
//
// KEYED BY SHA, NOT UPSERTED GLOBALLY. `<!-- agent-panel-round:<sha> -->` gives
// one comment per ROUND: re-running CI on the same commit updates that round's
// comment (the SHA is unchanged), while a new commit gets a new one. The
// alternative — a single sticky comment — was considered and rejected: the point
// is to read the rounds in order against the fix reports they answer, and a
// comment that rewrites itself leaves the timeline with the fixer's replies and
// none of the questions.
//
// NEUTRALIZATION IS LOAD-BEARING HERE, not hygiene. This runs in the panel job
// with `GITHUB_TOKEN`, so the comment is authored by `github-actions[bot]` — one
// of the two identities `rounds.mjs::isPagedLatchComment` trusts to LATCH the
// pipeline. The body it renders is lens prose: model output derived from an
// attacker-authorable diff, which quotes this repo's own markers as a matter of
// course. #681 is the recorded incident — an on-demand review comment that
// merely NAMED `<!-- agent-metrics-summary -->` while enumerating the harness's
// comment surfaces was deleted four seconds later by the metrics sweep. The same
// text carrying `<!-- agent-review-paged -->` under this identity would freeze
// the panel and the fixer on the PR. Everything interpolated goes through
// `neutralizeHiddenMarkers`, and a test plants a live latch in a finding summary.
//
// Usage (GH_TOKEN must be set):
//   node ./scripts/agent/panel-round-comment.mjs post <pr>
//     --sha <head-sha> [--review-dir .agent-review] [--lenses <path>]
//     [--blob-base <url>] [--run-url <url>] [--dry-run]

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { collectLenses, renderReviewComment } from "./review-comment.mjs";
import { buildRounds, neutralizeHiddenMarkers } from "./loop-status.mjs";
import { emitBestEffortWarning } from "./guard-verdict.mjs";
import { gh, ghJson, listAllComments } from "./metrics.mjs";
import { PAGE_AUTHOR_LOGINS } from "./rounds.mjs";

/** Marker prefix; the full marker carries the round's head SHA. */
export const PANEL_ROUND_PREFIX = "<!-- agent-panel-round:";

/** Leave room under GitHub's 65,536-char comment cap for the header and footer. */
export const MAX_REGION_CHARS = 58000;

/** Newest commits whose check runs are fetched, mirroring loop-status.mjs. */
export const MAX_COMMITS_SCANNED = 40;

const TRUSTED_ASSOCIATIONS = new Set(["OWNER", "MEMBER", "COLLABORATOR"]);

/** Same shape as review-comment.mjs's private helper; a one-lens round is common. */
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

/** The exact marker for one round. */
export function markerFor(sha) {
  return `${PANEL_ROUND_PREFIX}${String(sha ?? "")} -->`;
}

/**
 * Is this an update-able round comment of OURS, for this SHA?
 *
 * Same three conditions as `loop-status.mjs::isOwnStatusComment`, for the same
 * reasons: the body must START with the marker (a quote-reply begins with ">",
 * and GitHub's quote-reply copies hidden HTML comments verbatim), the author
 * must be trusted (a marker alone is plantable on a public repo), and no paged
 * latch may appear anywhere — our renderer neutralizes markers, so a live latch
 * means this is not our comment, and a page must never be overwritten.
 */
export function isOwnRoundComment(comment, sha) {
  const c = comment && typeof comment === "object" ? comment : {};
  const body = String(c.body ?? "");
  if (!body.trimStart().startsWith(markerFor(sha))) return false;
  if (body.includes("<!-- agent-review-paged -->") || body.includes("<!-- agent-paged -->")) return false;
  const user = c.user && typeof c.user === "object" ? c.user : {};
  if (user.type === "Bot" && PAGE_AUTHOR_LOGINS.includes(user.login)) return true;
  return TRUSTED_ASSOCIATIONS.has(String(c.author_association ?? ""));
}

/** The gate rule the panel uses, applied to `collectLenses` output. Pure. */
export function verdictOf(lenses) {
  const list = (Array.isArray(lenses) ? lenses : []).filter((l) => l && typeof l === "object");
  const blocked = list.some(
    (l) => l.gating !== false && l.applicable !== false && l.conclusion && l.conclusion !== "success",
  );
  const passed = list.filter((l) => l.conclusion === "success").length;
  return { blocked, passed, total: list.length };
}

/**
 * Render one round's comment. Pure — every I/O input is a parameter, so the
 * whole body is testable without `gh` or a review directory.
 *
 * `region` is `review-comment.mjs::renderReviewComment`'s output, which is `""`
 * when a round produced no findings at all. That is not an error and must not
 * render as silence: a clean round is exactly the round a reader most wants
 * confirmed, and "the panel posted nothing" is indistinguishable from "the panel
 * never ran". So the empty case gets its own sentence.
 */
export function renderPanelRoundComment({
  sha = "",
  round = null,
  lenses = [],
  region = "",
  panelMissing = false,
  commitUrl = "",
  runUrl = "",
} = {}) {
  // The identity line only. `renderReviewComment` emits its own
  // "Review panel: changes suggested / looks good" verdict heading, so repeating
  // the words here stacked two near-identical headings on every comment.
  const short = String(sha).slice(0, 9);
  const at = commitUrl ? `[\`${short}\`](${commitUrl})` : `\`${short}\``;
  const no = Number.isInteger(round) && round > 0 ? `Round ${round}` : "Review round";
  const lines = [markerFor(sha), `### 🔎 ${no} · ${at}`, ""];

  if (panelMissing) {
    // Fail-closed, and SAID so. The lenses all read `failure` in this state, so
    // rendering them as findings would report a review that never happened.
    lines.push(
      "🛑 **The panel produced no verdict** — every lens fails closed. Nothing was reviewed on this " +
        "commit, so treat the red checks as *unreviewed*, not as findings.",
      "",
    );
  } else {
    const { blocked, passed, total } = verdictOf(lenses);
    if (region) {
      lines.push(region, "");
    } else if (!blocked && total > 0) {
      lines.push(
        "### 🟢 Review panel: looks good",
        "",
        `**No findings** — all ${plural(total, "lens", "lenses")} passed.`,
        "",
      );
    } else {
      // Blocked with nothing to render: lenses failed without producing findings
      // (a lens that errored out). Worth its own wording — "no findings" here
      // would be a lie in the dangerous direction.
      lines.push(
        `**${passed} of ${total} lenses passed**, and the rest produced no readable findings — ` +
          "see the check runs on this commit.",
        "",
      );
    }
  }

  const links = [`[check runs on this commit](${commitUrl || "#"})`];
  if (runUrl) links.push(`[panel run](${runUrl})`);
  lines.push(
    `<sub>Verdicts of record are the \`agent-review-*\` check runs on this commit — this comment renders them. ` +
      `${links.join(" · ")}</sub>`,
  );

  // Everything after our own leading marker is neutralized: the body is authored
  // by an identity the paged latch trusts, and the lens prose in it is model
  // output over an untrusted diff. See the header.
  const body = lines.join("\n");
  const marker = markerFor(sha);
  return marker + neutralizeHiddenMarkers(body.slice(marker.length));
}

// --- gh-backed CLI -----------------------------------------------------------

const HERE = path.dirname(fileURLToPath(import.meta.url));

function bail(msg) {
  console.error(`panel-round-comment: ${msg} (continuing without a round comment)`);
  emitBestEffortWarning(`panel-round-comment failed: ${msg} — this round has no findings comment on the PR`);
  process.exit(0);
}

// Object-wrapped-array endpoint — `--paginate --slurp` then flatten, the same
// shape review-round-guard.mjs and loop-status.mjs use.
function checkRunsFor(sha) {
  const pages = ghJson([
    "api",
    `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`,
    "--paginate",
    "--slurp",
  ]);
  return pages.flatMap((p) => p.check_runs ?? []);
}

/**
 * Which round is `sha`? Counted with `loop-status.mjs::buildRounds`, deliberately
 * and not with a private rule: the dashboard's table says "Round 3" and this
 * comment must agree, or the two surfaces built to be read together disagree
 * about what a round is. Returns null when it cannot be established, and the
 * header then omits the number rather than guessing one.
 */
export function roundNumberFor(pr, sha, lensCheckNames, { api = ghJson, runs = checkRunsFor } = {}) {
  try {
    const commits = api(["api", `repos/{owner}/{repo}/pulls/${pr}/commits?per_page=100`, "--paginate"]);
    for (const c of commits) c.checkRuns = [];
    for (const c of commits.slice(-MAX_COMMITS_SCANNED)) c.checkRuns = runs(c.sha);
    const rounds = buildRounds(commits, lensCheckNames);
    const i = rounds.findIndex((r) => r.sha === sha);
    return i >= 0 ? i + 1 : null;
  } catch {
    return null;
  }
}

function parseArgs(argv) {
  const flags = {};
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--dry-run") flags.dryRun = true;
    else if (a.startsWith("--")) flags[a.slice(2)] = argv[++i] ?? "";
    else positional.push(a);
  }
  return { command: positional[0], pr: positional[1], flags };
}

function main() {
  const { command, pr, flags } = parseArgs(process.argv.slice(2));
  const sha = String(flags.sha ?? "");
  if (command !== "post" || !/^\d+$/.test(String(pr ?? "")) || !sha) {
    console.error(
      "Usage: node ./scripts/agent/panel-round-comment.mjs post <pr> --sha <head-sha> " +
        "[--review-dir .agent-review] [--lenses <path>] [--blob-base <url>] [--run-url <url>] [--dry-run]",
    );
    process.exit(2);
  }

  let body;
  try {
    const reviewDir = flags["review-dir"] ?? ".agent-review";
    const lensesPath = flags.lenses || path.join(HERE, "lenses", "lenses.json");
    const manifest = JSON.parse(readFileSync(lensesPath, "utf8"));

    // Read panel.json ourselves rather than inferring from collectLenses: with it
    // absent every lens reads `failure` with no findings, which is exactly what a
    // real all-red round looks like. Only this tells the two apart.
    let panelMissing = false;
    try {
      JSON.parse(readFileSync(path.join(reviewDir, "panel.json"), "utf8"));
    } catch {
      panelMissing = true;
    }

    const lenses = collectLenses(reviewDir, manifest);
    const region = panelMissing
      ? ""
      : renderReviewComment(lenses, { blobBase: flags["blob-base"] ?? "", maxChars: MAX_REGION_CHARS });

    const repo = `${process.env.GITHUB_SERVER_URL || "https://github.com"}/${process.env.GITHUB_REPOSITORY || ""}`;
    const commitUrl = process.env.GITHUB_REPOSITORY ? `${repo}/pull/${pr}/commits/${sha}` : "";

    body = renderPanelRoundComment({
      sha,
      round: roundNumberFor(pr, sha, manifest.map((l) => `agent-review-${l.id}`)),
      lenses,
      region,
      panelMissing,
      commitUrl,
      runUrl: flags["run-url"] ?? "",
    });
  } catch (e) {
    bail(`could not build the round comment: ${e.message}`);
  }

  if (flags.dryRun) {
    console.log(body);
    return;
  }

  try {
    const existing = listAllComments(pr)
      .filter((c) => isOwnRoundComment(c, sha))
      .sort((a, b) => Number(a.id) - Number(b.id));
    if (existing.length > 0) {
      gh(["api", "-X", "PATCH", `repos/{owner}/{repo}/issues/comments/${existing[0].id}`, "-f", `body=${body}`]);
      console.log(`panel-round-comment: updated comment ${existing[0].id} on PR #${pr}`);
    } else {
      gh(["api", "-X", "POST", `repos/{owner}/{repo}/issues/${pr}/comments`, "-f", `body=${body}`]);
      console.log(`panel-round-comment: posted the round comment on PR #${pr}`);
    }
  } catch (e) {
    bail(`could not post the round comment: ${e.message}`);
  }
}

// Import-safe: only run the CLI when executed directly, so tests can import the
// pure renderers without touching gh.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
