// Presentation for the review-round guard's decision (review-round-guard.mjs).
//
// The guard has always COMPUTED why the loop continues — failed rounds vs the
// cap, the stall detector's reason, the standstill count — and then thrown all
// of it away on the PROCEED path: only pages were ever visible, so from the PR
// a continuing loop and a dead loop looked identical. These helpers render the
// decision once, for two surfaces: a one-line `verdict` step output (consumed
// by loop-status.mjs as the sticky comment's "Latest:" line) and a markdown
// block for $GITHUB_STEP_SUMMARY (the run page).
//
// Pure rendering only — no decision logic lives here, so a bug in this file
// can mislabel a decision but never change one. Every field is optional:
// the guard's infra/invalid-verdict pages fire before rounds are counted, so
// the renderer must tolerate a verdict with nothing but a decision and a reason.

import { appendFileSync } from "node:fs";
import { redactSecrets } from "./redact.mjs";

/** Human labels for the guard's page reasons, keyed by the caller's `reason`. */
const PAGE_REASONS = {
  infra: "the review panel could not run (Claude API/quota error)",
  "invalid-verdict": "a review lens did not produce a valid verdict",
  standstill: "a finding was disputed twice and upheld twice by the adjudicator",
  stall: "the panel is re-raising the same findings with no reduction",
  "round-cap": "the fix-round budget is exhausted",
};

// null/undefined/"" mean "not measured", NOT zero — Number(null) is 0, which
// would render the guard's early pages (infra / invalid-verdict, fired before
// commits are fetched) as a confident "0 of 3" that was never counted.
const n = (v) =>
  v === null || v === undefined || v === "" ? null : Number.isFinite(Number(v)) ? Number(v) : null;

/** Single line, safe for $GITHUB_OUTPUT (no newlines) and for a comment cell. */
export function guardVerdictLine(v = {}) {
  const line = buildLine(v);
  return line.replace(/\r?\n/g, "; ");
}

function buildLine(v) {
  if (v.decision === "latched") {
    return "Round guard: skipped — this PR was already paged to a human; the fixer was not dispatched.";
  }
  if (v.decision === "page") {
    const why = PAGE_REASONS[v.reason] ?? String(v.reason ?? "see the page comment");
    return `Round guard: paged a human — ${why}. The loop is stopped until \`@claude rerun\`.`;
  }
  // proceed
  const used = n(v.failedRounds);
  const max = n(v.max);
  const parts = [];
  if (used !== null && max !== null) {
    // `used` dispatches are already on the ledger; this one is used+1.
    parts.push(`dispatching fix round ${used + 1} of ${max}`);
  } else {
    parts.push("dispatching the fix agent");
  }
  if (v.stall && v.stall.reason) {
    parts.push(`stall detector: ${v.stall.reason} (${n(v.stall.stalls) ?? 0} stalling pair(s) over ${n(v.stall.rounds) ?? 0} round(s))`);
  }
  parts.push(`standstill: ${n(v.standstillCount) ?? 0} finding(s) at the rebuttal limit`);
  if (v.heldByRerun) parts.push("stall/standstill pages held for one attempt after `@claude rerun`");
  else if (v.rerunAt) parts.push(`round floor: last \`@claude rerun\` (${v.rerunAt})`);
  return `Round guard: proceed — ${parts.join("; ")}.`;
}

/** Markdown block for the job summary. Tolerates missing fields (early pages). */
export function renderGuardSummary(v = {}) {
  if (v.decision === "latched") {
    return [
      "### Review-round guard — SKIPPED (already paged)",
      "",
      "A trusted `<!-- agent-review-paged -->` latch comment is already on this PR, so a human owns it. The fixer was not dispatched.",
      "",
    ].join("\n");
  }
  if (v.decision === "page") {
    const why = PAGE_REASONS[v.reason] ?? String(v.reason ?? "unknown");
    const lines = [
      `### Review-round guard — PAGED (${v.reason ?? "unknown"})`,
      "",
      `- Why: ${why}`,
    ];
    if (n(v.failedRounds) !== null && n(v.max) !== null) {
      lines.push(`- Fix rounds dispatched so far: ${n(v.failedRounds)} of ${n(v.max)}`);
    }
    // FENCED, not blockquoted: for the stall/standstill pages `detail` embeds
    // lens finding summaries — LLM output derived from the attacker-authorable
    // diff — and a blockquote would let crafted markdown/HTML render as
    // structure on the run page. A text fence displays it inert; any fence
    // run inside the text is defanged so it cannot break out.
    //
    // `redactSecrets` for the same reason the fence is here — this text is not
    // ours. The fence stops it rendering as structure; redaction stops it
    // carrying a credential.
    if (v.detail) {
      lines.push("", "```text", redactSecrets(String(v.detail)).slice(0, 600).replace(/`{3,}/g, "···"), "```");
    }
    lines.push("", "The page comment on the PR carries the full hand-off text; `set-state.mjs` moves the PR to `agent:blocked`.", "");
    return lines.join("\n");
  }
  // proceed
  const lines = ["### Review-round guard — PROCEED", ""];
  if (n(v.failedRounds) !== null && n(v.max) !== null) {
    lines.push(
      `- Fix rounds dispatched: ${n(v.failedRounds)} of ${n(v.max)} (counted from this guard's own \`agent-fix-dispatch\` records, or from single-parent commits carrying failing lens checks on a PR that predates the ledger${v.rerunAt ? `, floored at the last \`@claude rerun\`` : ""})`,
    );
  }
  if (v.stall && v.stall.reason) {
    lines.push(`- Stall detector: \`${v.stall.reason}\` (${n(v.stall.stalls) ?? 0} stalling pair(s) over ${n(v.stall.rounds) ?? 0} fix-attempt round(s))`);
  }
  lines.push(`- Standstill: ${n(v.standstillCount) ?? 0} finding(s) at the ${n(v.rebuttalLimit) ?? 2}-uphold rebuttal limit`);
  // The PROCEED path publishes the infra text too — a second surface, and the one
  // easiest to forget because the page path above looks like the only one.
  lines.push(`- Infra: ${v.infra ? redactSecrets(String(v.infra)).slice(0, 200) : "none"}`);
  if (v.heldByRerun) {
    lines.push("- `@claude rerun` hand-back: stall/standstill pages held for this one attempt");
  }
  if (Array.isArray(v.requiredCheckNames) && v.requiredCheckNames.length) {
    lines.push("", `→ dispatching the fix agent against the failing checks: ${v.requiredCheckNames.join(", ")}`);
  } else {
    lines.push("", "→ dispatching the fix agent");
  }
  lines.push("");
  return lines.join("\n");
}

/**
 * The Actions run URL from the runner's own default env, or null when any
 * piece is missing (a local run). Null, never a partial string: a URL built
 * around an absent `GITHUB_RUN_ID` renders as a clickable link to
 * `.../actions/runs/undefined`, which is worse than no link at all.
 */
export function runUrlFromEnv(env = process.env) {
  const part = (k) => (typeof env[k] === "string" && env[k] !== "" ? env[k] : null);
  const server = part("GITHUB_SERVER_URL");
  const repo = part("GITHUB_REPOSITORY");
  const run = part("GITHUB_RUN_ID");
  return server && repo && run ? `${server}/${repo}/actions/runs/${run}` : null;
}

/**
 * One "Where to look" line for a 🛑 page comment: the failed run, the job
 * inside it, and the artifact carrying the transcript — the three clicks a
 * page used to make a maintainer reconstruct from the Actions tab. Returns ""
 * without a URL, so a page posted from a context with no run link renders
 * exactly as it does today rather than pointing at nothing.
 */
export function whereToLookLine({ runUrl, job, step, artifact } = {}) {
  if (!runUrl) return "";
  const jobPart = job ? ` → job \`${job}\`` : "";
  const stepPart = job && step ? `, step "${step}"` : "";
  const artifactPart = artifact ? `; transcript in the \`${artifact}\` artifact` : "";
  return `\n\nWhere to look: [this run](${runUrl})${jobPart}${stepPart}${artifactPart}.`;
}

/**
 * Surface a best-effort failure as a run annotation + one job-summary line.
 *
 * The fail-safe scripts (set-state, loop-status, metrics) deliberately exit 0
 * on any operational problem, so their `continue-on-error:` steps NEVER show a
 * failed outcome — the failure lives only in a log nobody opens, and the
 * symptom (a stale label, a missing effort comment) surfaces later with
 * nothing connecting it to the cause. `::warning::` renders on the run page
 * and in the PR checks-tab header, which is human-visible without opening
 * logs.
 *
 * The workflow command goes to STDOUT — the runner only scans stdout for
 * commands — and only when actually running inside Actions, so local runs
 * stay clean (the caller's own stderr message is still printed). `%0A` etc.
 * are not escaped because every caller passes single-line prose it wrote
 * itself. Never throws: display only.
 */
export function emitBestEffortWarning(msg, env = process.env) {
  if (env.GITHUB_ACTIONS !== "true") return;
  try {
    console.log(`::warning::${String(msg).replace(/\r?\n/g, " ")}`);
    appendStepSummary(`⚠️ ${msg}`);
  } catch {
    /* display only */
  }
}

/**
 * Append markdown to the job summary when running inside Actions; no-op (with
 * a stderr echo, so local runs still show it) otherwise. Never throws — this
 * is display, and a full disk or bad path must not fail the guard.
 */
export function appendStepSummary(md) {
  const p = process.env.GITHUB_STEP_SUMMARY;
  try {
    if (p) appendFileSync(p, `${md}\n`);
    else console.error(md);
  } catch (e) {
    console.error(`could not write step summary: ${e.message}`);
  }
}
