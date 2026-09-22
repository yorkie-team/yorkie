// Shared AI-authorship disclosure — the SINGLE source of truth for how the
// pipeline declares that an agent wrote something, and for the moment it hands
// the result to humans. Both the cloud ready-gate (`mark-ready.mjs`) and the
// local front door (`spec-to-pr.mjs`) import the disclosure predicate, so the two
// can never drift apart; `harvest.mjs` imports the hand-off marker for the same
// reason.
//
// `scripts/hooks/require-ai-disclosure.sh` is a shell hook and keeps its own copy
// of the trailer string; DISCLOSURE_TRAILER here mirrors it.

/** The commit trailer autonomous runs must carry (see require-ai-disclosure.sh). */
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
 * True iff a PR body discloses autonomous AI authorship. This IS the gate
 * `mark-ready.mjs` enforces before promoting a PR to ready — keep it here so the
 * local front door validates against the exact same predicate.
 */
export function disclosesAiAuthorship(body) {
  const b = String(body ?? "");
  return /autonomous/i.test(b) && /(claude|ai[- ]assist|ai tools)/i.test(b);
}

/** True iff a single commit message carries the autonomous disclosure trailer. */
export function hasDisclosureTrailer(message) {
  return String(message ?? "").includes(DISCLOSURE_TRAILER);
}
