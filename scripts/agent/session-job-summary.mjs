// $GITHUB_STEP_SUMMARY renderer for a single claude-code-action session — the
// implement kickoff and both fixer arms. Puts turns/tokens/cost/duration and
// the success-or-failure classification on the run page, so "why did this
// session do nothing" stops requiring an artifact download.
//
// Reuses metrics.mjs's parsing (parseExecution) and its success classifier
// (classifyFixResult — written for exactly this transcript shape; see its
// docblock for why classifyResult alone misreports success as "no-output").
//
// Display only, fail-safe: exits 0 on every path; a missing or unreadable
// execution log renders a line saying so instead of failing the job.
//
// Usage:
//   node ./scripts/agent/session-job-summary.mjs --execution PATH
//     --kind implement|ci-fix|review-fix [--title "Implement agent session"]

import { readFileSync, appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { redactSecrets } from "./redact.mjs";
import {
  parseExecution,
  classifyFixResult,
  formatTokens,
  formatUsd,
  formatMinutes,
} from "./metrics.mjs";

/** Render the session table. Pure. `rec`/`outcome` may be null. */
export function renderSessionSummary({ rec = null, outcome = null, title = "Agent session" } = {}) {
  const lines = [`### ${title}`, ""];
  if (!rec) {
    lines.push(
      "No execution log was produced — the agent step likely died before its first turn. See the step log above.",
      "",
    );
    return lines.join("\n");
  }
  const weighted = Number(rec.weightedTokens) || Number(rec.tokens) || 0;
  const durMs = Number(rec.durationMs) || 0;
  // Seconds under a minute — an agent that died in 18s did no work, and
  // formatMinutes' floor of "1m" hides exactly that (same rule as renderFixEffort).
  const duration = durMs < 60_000 ? `${Math.round(durMs / 1000)}s` : formatMinutes(durMs);
  lines.push(
    "| Turns | Tokens | Cost | Duration | Model |",
    "|---|---|---|---|---|",
    `| ${rec.turns} | ${formatTokens(rec.tokens)} (weighted ${formatTokens(weighted)}) | ${formatUsd(rec.costUsd)} | ${duration} | ${(rec.models || []).join(", ") || "unknown"} |`,
    "",
  );
  if (outcome && outcome.ok === false) {
    // `reason` first (closed vocabulary), redacted `detail` as the fallback for
    // records written before the vocabulary existed. Same rule as metrics.mjs.
    const detail = String(outcome.reason || redactSecrets(String(outcome.detail || "")) || "no detail reported").slice(0, 400);
    lines.push(
      `**Outcome: failed (${outcome.kind}${outcome.status ? ` ${outcome.status}` : ""})** — ${detail}`,
      "",
    );
  } else if (outcome && outcome.ok) {
    lines.push("**Outcome: completed.**", "");
  }
  return lines.join("\n");
}

// --- CLI ---------------------------------------------------------------------

function parseArgs(argv) {
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    if (argv[i].startsWith("--")) flags[argv[i].slice(2)] = argv[++i] ?? "";
  }
  return flags;
}

function main() {
  const flags = parseArgs(process.argv.slice(2));
  const kind = flags.kind || "implement";
  const title = flags.title || `${kind} agent session`;

  let rec = null;
  let outcome = null;
  try {
    const messages = JSON.parse(readFileSync(flags.execution, "utf8"));
    rec = parseExecution(messages, kind);
    const result = Array.isArray(messages)
      ? [...messages].reverse().find((m) => m && m.type === "result")
      : null;
    outcome = classifyFixResult(result);
  } catch {
    // fall through — renderSessionSummary handles rec === null
  }

  const md = renderSessionSummary({ rec, outcome, title });
  const out = process.env.GITHUB_STEP_SUMMARY;
  try {
    if (out) appendFileSync(out, `${md}\n`);
    else console.log(md);
  } catch (e) {
    console.error(`session-job-summary: could not write: ${e.message}`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
