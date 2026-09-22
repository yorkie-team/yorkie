// Command router for the "@claude" automation surface — the ONE place that maps
// a comment body to a pipeline verb, so every agent workflow dispatches the same
// way and can't drift. Matching is flexible / containment-based: a comment
// triggers a verb if it contains "@claude <verb>" anywhere (case-insensitive),
// regardless of surrounding words — "please @claude fix this now" and
// "@claude fix" both parse as `fix`. The verb must follow the mention directly
// (one run of whitespace), so "@claude please review" is NOT `review` (it does
// not contain the contiguous "@claude review") — it falls through to `reply`.
//
// Recognized verbs: fix | summarize (alias summarise) | review | loop | rerun.
// If a comment contains more than one verb, the FIRST occurrence wins
// (leftmost match — deterministic and documented).
//
// Fallbacks (no recognized verb):
//   - "@claude" present, surface 'pr'    → `reply`  (the existing address-feedback path)
//   - "@claude" present, surface 'issue' → `help`   (post a "did you mean @claude fix?" reply)
//   - no "@claude" at all                → `none`   (not for us; do nothing)
//
// Usage (CLI): node ./scripts/agent/command.mjs "<comment body>" <issue|pr>
//   Emits `command=<verb>` to $GITHUB_OUTPUT (when set) and stdout.

import { appendFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// verb token in the comment -> canonical command. Order is irrelevant to
// "first occurrence wins": the regex finds the leftmost match in the body.
const VERB_TO_COMMAND = {
  fix: "fix",
  summarize: "summarize",
  summarise: "summarize", // en-GB spelling normalizes to the same command
  review: "review",
  loop: "loop",
  rerun: "rerun", // clear a paged/blocked agent PR and re-engage the review→fix loop
};

// "@claude" + one run of whitespace + a recognized verb, as a whole word.
// `i` = case-insensitive (@Claude / REVIEW); no `g` — we want the leftmost match.
const COMMAND_RE = new RegExp(
  String.raw`@claude\s+(${Object.keys(VERB_TO_COMMAND).join("|")})\b`,
  "i",
);
// Bare "@claude" mention — but NOT "@claude-bot" / "@claudefoo" (a different
// account). Negative lookahead forbids a trailing word char OR hyphen.
const MENTION_RE = /@claude(?![\w-])/i;

/**
 * Parse a comment body into a pipeline command.
 * @param {string} body - the raw comment body.
 * @param {{surface?: 'issue'|'pr'}} [opts] - where the comment was posted; only
 *   affects the no-verb fallback (`reply` on a PR vs `help` on an issue).
 * @returns {{command: 'fix'|'summarize'|'review'|'loop'|'rerun'|'reply'|'help'|'none', rest: string}}
 *   `rest` is the text following the matched command (trimmed), for passing any
 *   extra instructions through to the agent; "" when there is no verb match.
 */
export function parseCommand(body, { surface = "pr" } = {}) {
  const text = String(body ?? "");
  const m = text.match(COMMAND_RE);
  if (m) {
    const command = VERB_TO_COMMAND[m[1].toLowerCase()];
    const rest = text.slice(m.index + m[0].length).trim();
    return { command, rest };
  }
  if (MENTION_RE.test(text)) {
    return { command: surface === "issue" ? "help" : "reply", rest: "" };
  }
  return { command: "none", rest: "" };
}

// --- CLI -------------------------------------------------------------------
// Only run when invoked directly (not when imported by the test file).
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const surfaceArg = (process.argv[3] ?? "pr").toLowerCase();
  const surface = surfaceArg === "issue" ? "issue" : "pr";
  const { command } = parseCommand(process.argv[2] ?? "", { surface });
  const line = `command=${command}\n`;
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line);
  process.stdout.write(line);
}
