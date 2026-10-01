// Was a failed fix round the fixer's failure, or the infrastructure's?
//
// WHY THIS EXISTS. On #1426, after a rerun, fix round 2 died with
// `is_error: true` after 25 turns ($0.90) and the next one died 0.5 s after init
// ($0). Each was paged by the `stalled` net as "the fixer agent failed and the
// branch head is unchanged" — which sent a human looking for a fixer that could
// not converge, when the API had stopped answering. The cause was computed by
// `classifyFixResult`, but only into the job summary, which nobody paged reads.
//
// This runs in the trusted `fix-report` job, on a runner the agent never had a
// shell on, and decides from the fixer's execution log:
//
//   infra  the fixer step FAILED, the branch head is KNOWN not to have
//          advanced, and the session ended on an API error (no response, a
//          closed usage window, a rejected credential, a rate limit). It pages
//          with the cause and the next step, and `stalled` stands down.
//   not    anything else — a turn ceiling, a clean finish, a cancellation, a
//          round that pushed, a log with no result. The existing nets page as
//          before.
//
// NO REFUND. A first version also refunded the round. The page latches the PR,
// only `@claude rerun` lifts the latch, and a rerun restarts the fix budget — so
// a refund in the old window could never change a decision. The page says what
// is true instead.
//
// THE LOG IS AGENT-WRITABLE: the action writes it on the runner the agent ran
// on, with a shell. So nothing from it reaches the PR except `classifyResult`'s
// closed vocabulary. The worst a forged log can do is choose which page a
// human reads; the PR is latched either way.
//
// Usage:
//   node fix-outcome.mjs page <pr> --execution <file> --fixer <outcome>
//     --advanced <true|false> [--run-url <url>]
// Writes `infra=` and `paged=` to $GITHUB_OUTPUT. Exits non-zero only if the
// page itself could not be posted, so the `stalled` net still sees a failed job.

import { appendFileSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { classifyFixResult } from "./metrics.mjs";
import { PAGED_LATCH } from "./rounds.mjs";
import { parseArgs } from "./gh-checks.mjs";

/**
 * Decide from the execution log and the two facts the workflow knows. Pure.
 * Returns `{ infra: false }` or `{ infra: true, code, reason, advice }`.
 */
export function classifyFixOutcome({ messages, fixer, advanced } = {}) {
  const no = { infra: false };
  // Only a round that pushed NOTHING. One that pushed did work, and the next
  // panel judges it; calling it infra would hide that it did work.
  if (advanced !== false && advanced !== "false") return no;
  // Only a FAILED fixer step. A clean finish with no commit is the no-commit
  // page's case, and a cancellation is the job wall's.
  if (fixer !== "failure") return no;
  const result = Array.isArray(messages) ? [...messages].reverse().find((m) => m && m.type === "result") : null;
  const outcome = classifyFixResult(result);
  if (!outcome || outcome.ok) return no;
  // `limit` is a ceiling the fixer itself ran into (turns, budget) — the round
  // was honestly spent. Only an API error is the infrastructure's.
  if (outcome.kind !== "api-error") return no;
  const code = typeof outcome.code === "string" ? outcome.code : "UPSTREAM_ERROR";
  return { infra: true, code, reason: String(outcome.reason || `[${code}]`), advice: adviceFor(code) };
}

function adviceFor(code) {
  if (code === "USAGE_LIMIT" || code === "RATE_LIMITED" || code === "POOL_EXHAUSTED") {
    return "An account usage window is closed. It reopens on its own; comment `@claude rerun` once it has, or register more `CLAUDE_CODE_OAUTH_TOKEN_N` secrets so one busy account cannot starve the fixer.";
  }
  if (code.startsWith("AUTH_")) {
    return "A Claude credential was refused. Check the `CLAUDE_CODE_OAUTH_TOKEN` / `CLAUDE_CODE_OAUTH_TOKEN_N` secrets on the `agent` environment, then comment `@claude rerun`.";
  }
  return "The API stopped answering mid-session, which is usually transient. Comment `@claude rerun` to try again.";
}

/** The page body. Every `<!--` after the latch is broken, as fix-report.mjs does. */
export function renderInfraPage({ outcome, runUrl = "" }) {
  const body = [
    `🛑 The fix agent did not get to finish: **${outcome.reason}**. That is an infrastructure failure, not a verdict on this pull request — nothing was pushed.`,
    "",
    outcome.advice,
    "",
    "`@claude rerun` restarts the fix budget, and on this commit it reuses the verdicts already on this commit and dispatches the fixer directly, without spending a new review.",
    "",
    runUrl ? `Where to look: [this run](${runUrl}) → job \`fix\`, step "Address panel findings".` : null,
  ].filter((l) => l !== null).join("\n").replace(/<!--/g, "<!-\u200c-");
  return `${PAGED_LATCH}\n${body}`;
}

function main() {
  const a = parseArgs(process.argv);
  const [verb, pr] = a._;
  const out = (k, v) => {
    console.error(`  ${k}=${v}`);
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${k}=${v}\n`);
  };
  if (verb !== "page" || !/^\d+$/.test(String(pr ?? ""))) {
    console.error("usage: fix-outcome.mjs page <pr> --execution <file> --fixer <outcome> --advanced <bool>");
    out("infra", "false");
    out("paged", "false");
    return;
  }
  let messages = null;
  try {
    messages = JSON.parse(readFileSync(a.execution, "utf8"));
  } catch {
    // No log: nothing proves infra, so the existing nets page as before.
  }
  const outcome = classifyFixOutcome({ messages, fixer: a.fixer, advanced: a.advanced });
  out("infra", String(outcome.infra));
  if (!outcome.infra) {
    out("paged", "false");
    return;
  }

  // GITHUB_TOKEN in the workflow: `github-actions[bot]` is a latch author the
  // gate believes (rounds.mjs PAGE_AUTHOR_LOGINS).
  execFileSync("gh", ["pr", "comment", pr, "--body-file", "-"], {
    input: renderInfraPage({ outcome, runUrl: a["run-url"] || "" }),
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  out("paged", "true");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
