// $GITHUB_STEP_SUMMARY renderer for the review-panel job — makes the run page
// self-explanatory from files the orchestrator already writes (panel.json,
// review-lens-stats.json, review-timing.json, review-execution.json).
//
// Display only, fail-safe by construction: it exits 0 on every path, renders
// "—" for anything absent, and a missing panel.json renders the fail-closed
// explanation rather than an empty table (a crashed orchestrator is exactly
// when a human reads this page). It must never fail the panel job — the
// workflow step is additionally continue-on-error.
//
// Usage:
//   node ./scripts/agent/panel-job-summary.mjs --review-dir .agent-review
//     [--pr N] [--scope-mode M] [--scope-reason R] [--scope-rounds N]

import { readFileSync, appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { formatUsd, formatMinutes } from "./metrics.mjs";

/** severity counts → "1 crit, 2 maj" (blocking first, zeroes dropped). */
export function severityCell(counts) {
  const c = counts && typeof counts === "object" ? counts : {};
  const parts = [
    ["critical", "crit"],
    ["major", "maj"],
    ["minor", "min"],
    ["nit", "nit"],
  ]
    .filter(([k]) => Number(c[k]) > 0)
    .map(([k, label]) => `${Number(c[k])} ${label}`);
  return parts.length ? parts.join(", ") : "0";
}

function verdictCell(entry) {
  if (!entry) return "❌ missing (fails closed)";
  if (entry.applicable === false) return "➖ not applicable";
  if (entry.conclusion === "success") return "✅ success";
  if (entry.conclusion === "skipped") return "➖ skipped";
  return entry.infraError ? "❌ failure (infra)" : "❌ failure";
}

/**
 * Render the summary markdown. Pure. `panel` is panel.json's array (or null
 * when unreadable); `stats` review-lens-stats.json's array; `timing`
 * review-timing.json's object; `totalCostUsd`/`sdkCalls` from the execution log.
 */
export function renderPanelJobSummary({
  panel = null,
  stats = [],
  timing = null,
  totalCostUsd = null,
  sdkCalls = null,
  pr = "",
  scope = {},
} = {}) {
  const scopeTxt = scope.mode
    ? ` · scope: **${scope.mode}** (\`${scope.reason ?? "?"}\`${scope.rounds ? `, round ${scope.rounds}` : ""})`
    : "";
  const lines = [`## Review panel${pr ? ` — PR #${pr}` : ""}${scopeTxt}`, ""];

  if (!Array.isArray(panel)) {
    lines.push(
      "**Panel produced no readable `panel.json` — every lens fails closed.**",
      "",
      'See the "Run review panel" step log; the check runs on the PR are marked `failure` by the posting step.',
      "",
    );
    return lines.join("\n");
  }

  const statById = new Map((Array.isArray(stats) ? stats : []).map((s) => [s?.id, s]));
  lines.push(
    "| Lens | Verdict | Raised | Sent to verifier | Refuted | Kept blocking | Verifier errors |",
    "|---|---|---|---|---|---|---|",
  );
  for (const entry of panel) {
    const s = statById.get(entry?.id);
    const v = s?.verifier ?? null;
    lines.push(
      `| ${entry?.id ?? "?"} | ${verdictCell(entry)} | ${s ? severityCell(s.raised) : "—"} | ${
        v ? (Number(v.sentToVerifier) || 0) : "—"
      } | ${v ? (Number(v.refuted) || 0) : "—"} | ${s ? severityCell(s.kept) : "—"} | ${
        v && Number(v.errored) > 0 ? `⚠️ ${Number(v.errored)}` : v ? "0" : "—"
      } |`,
    );
  }
  lines.push("");

  const footer = [];
  const wallMs = Number(timing?.wallMs);
  if (Number.isFinite(wallMs) && wallMs > 0) footer.push(`wall time ${formatMinutes(wallMs)}`);
  if (Number.isFinite(Number(totalCostUsd))) footer.push(`panel cost ${formatUsd(Number(totalCostUsd))}`);
  if (Number.isFinite(Number(sdkCalls))) footer.push(`${Number(sdkCalls)} SDK call(s)`);
  if (footer.length) lines.push(footer.join(" · "), "");
  lines.push(
    "_Verdicts of record are the `agent-review-*` check runs on the PR; findings live in each check's summary._",
    "",
  );
  return lines.join("\n");
}

// --- CLI ---------------------------------------------------------------------

function readJson(p) {
  try {
    return JSON.parse(readFileSync(p, "utf8"));
  } catch {
    return null;
  }
}

function parseArgs(argv) {
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    if (argv[i].startsWith("--")) flags[argv[i].slice(2)] = argv[++i] ?? "";
  }
  return flags;
}

function main() {
  const flags = parseArgs(process.argv.slice(2));
  const dir = flags["review-dir"] || ".agent-review";

  const panel = readJson(path.join(dir, "panel.json"));
  const stats = readJson(path.join(dir, "review-lens-stats.json")) ?? [];
  const timing = readJson(path.join(dir, "review-timing.json"));

  // Panel cost = sum over every SDK result message in the execution log.
  let totalCostUsd = null;
  let sdkCalls = null;
  const exec = readJson(path.join(dir, "review-execution.json"));
  if (Array.isArray(exec)) {
    const results = exec.filter((m) => m && m.type === "result");
    sdkCalls = results.length;
    totalCostUsd = results.reduce((sum, m) => sum + (Number(m.total_cost_usd) || 0), 0);
  }

  const md = renderPanelJobSummary({
    panel,
    stats,
    timing,
    totalCostUsd,
    sdkCalls,
    pr: flags.pr ?? "",
    scope: {
      mode: flags["scope-mode"] || "",
      reason: flags["scope-reason"] || "",
      rounds: flags["scope-rounds"] || "",
    },
  });

  const out = process.env.GITHUB_STEP_SUMMARY;
  try {
    if (out) appendFileSync(out, `${md}\n`);
    else console.log(md);
  } catch (e) {
    console.error(`panel-job-summary: could not write: ${e.message}`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
