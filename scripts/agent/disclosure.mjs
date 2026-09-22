// Shared AI-authorship disclosure — the SINGLE source of truth for how the
// pipeline declares that an agent wrote something, and for the moment it hands
// the result to humans. Both the cloud ready-gate (`mark-ready.mjs`) and the
// local front door (`spec-to-pr.mjs`) import the disclosure predicate, so the two
// can never drift apart; `harvest.mjs` imports the hand-off marker for the same
// reason.
//
// NO HOOK MIRRORS THIS HERE. Upstream pairs the trailer with a
// `require-ai-disclosure.sh` harness hook that enforces it at commit time; that
// hook is not ported (this repository's only git hook is `commit-msg`, and the
// harness hooks are a separate subsystem). So DISCLOSURE_TRAILER is currently
// written by the fixer prompts and read by nothing that can refuse a commit —
// the PR-body predicate below is the gate that actually holds.

/** The commit trailer autonomous runs carry. Advisory here — see above. */
export const DISCLOSURE_TRAILER = "Assisted-by: Claude Code (autonomous)";

/**
 * Hidden marker on the hand-off comment `mark-ready.mjs` posts when it promotes a
 * PR to ready-for-review.
 *
 * It lives here rather than in `mark-ready.mjs` because it is now a CONTRACT
 * between two modules: `mark-ready.mjs` writes it and `harvest.mjs` reads it to
 * find the instant the panel stopped being responsible for the PR. `mark-ready`
 * runs its CLI at import time, so nothing can import a constant from it — and a
 * second hand-written copy of a marker string is a silent failure, not a loud
 * one: the reader simply finds no hand-offs and reports an empty result.
 */
export const HANDOFF_MARKER = "<!-- agent-handoff -->";

/**
 * Words that turn a disclosure into its opposite. `n't` covers "wasn't".
 *
 * NOT AN ADVERSARIAL FILTER, and it cannot be one: this gate is a self-report,
 * and the module header already says so — a truthful agent has no reason to
 * hide its authorship and a dishonest one simply stays a draft. The list exists
 * to stop a sentence that means the OPPOSITE from satisfying the gate by
 * accident, which is a different and much smaller problem than parsing English.
 */
const NEGATOR = /\b(?:not|never|without|neither|nor|no|none|nothing)\b|n['\u2019]t\b/i;
const AUTONOMOUS = /\bautonomous(?:ly)?\b/i;
const AI_ACTOR = /\b(?:claude|ai[- ]assist(?:ed|ance)?|ai tools)\b/i;

/**
 * True iff a PR body discloses autonomous AI authorship. This IS the gate
 * `mark-ready.mjs` enforces before promoting a PR to ready — keep it here so the
 * local front door validates against the exact same predicate.
 *
 * AFFIRMATIVE, and per CLAUSE. Testing the two terms against the whole body
 * accepted the opposite of a disclosure: "This PR was NOT authored autonomously
 * with Claude" contains both words and passed the gate whose entire job is to
 * establish that an agent DID write this. Clause-level rather than body-level
 * because a real disclosure and an unrelated "no …" can share a paragraph.
 *
 * Fails CLOSED on an ambiguous phrasing — "…autonomously with no human edits"
 * is refused for the negator it happens to contain. That is the right direction
 * for a gate that flips a PR to ready, and the refusal names the line to add
 * (see `mark-ready.mjs`), so the cost is one edit rather than a stuck PR.
 */
export function disclosesAiAuthorship(body) {
  return String(body ?? "")
    .split(/[.;\n!?]+/)
    .some((clause) => AUTONOMOUS.test(clause) && AI_ACTOR.test(clause) && !NEGATOR.test(clause));
}

/** True iff a single commit message carries the autonomous disclosure trailer. */
export function hasDisclosureTrailer(message) {
  return String(message ?? "").includes(DISCLOSURE_TRAILER);
}
