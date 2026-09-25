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

// Node builtins only, and that is a constraint rather than a coincidence —
// see the CLI guard at the bottom of this file.
import { appendFileSync } from "node:fs";
import path from "node:path";
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

/**
 * The canonical verbs, deduplicated — `summarise` and `summarize` are one
 * command. Exported so a guard can assert that every surface which accepts a
 * mention answers every verb: the inline review thread silently answered none
 * of them for months, because nothing compared this list against the
 * workflows that consume it.
 */
export const COMMANDS = Object.freeze([...new Set(Object.values(VERB_TO_COMMAND))]);

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
//
// NOT `isDirectRun` from `scripts/direct-run.mjs`, which is the shared version
// of this predicate everywhere else in the repository. Six workflows check
// this file out ALONE — `sparse-checkout: scripts/agent/command.mjs` with
// `sparse-checkout-cone-mode: false`, which writes exactly that path and
// nothing else — so a single relative import outside this file turns every
// `@claude` comment into ERR_MODULE_NOT_FOUND, and the router fails silently
// at the workflow level: an empty `command=` output reads as "no verb here".
// `scripts/README.md`'s `agent/` row states the rule; `checks.test.mjs` pins
// it.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const surfaceArg = (process.argv[3] ?? "pr").toLowerCase();
  const surface = surfaceArg === "issue" ? "issue" : "pr";
  const { command } = parseCommand(process.argv[2] ?? "", { surface });
  const line = `command=${command}\n`;
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line);
  process.stdout.write(line);
}
