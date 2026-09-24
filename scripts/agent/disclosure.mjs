// Shared AI-authorship disclosure — the SINGLE source of truth for how the
// pipeline declares that an agent wrote something, and for the moment it hands
// the result to humans. Both the cloud ready-gate (`mark-ready.mjs`) and the
// local front door (`spec-to-pr.mjs`) import the disclosure predicate, so the two
// can never drift apart; `harvest.mjs` imports the hand-off marker for the same
// reason.
//
// NO HOOK MIRRORS THIS HERE, AND PORTING ONE WOULD BE A NO-OP. Upstream pairs
// the trailer with a `require-ai-disclosure.sh` harness hook that enforces it at
// commit time. This repository now has a harness-hook subsystem of its own
// (`scripts/hooks/`, wired per clone by its `install.mjs`) and three git hooks, so
// the reason is no longer "there is nowhere to put it" — it is that the hook
// would never fire. Upstream it is inert unless an environment variable is set,
// and the LOCAL autonomous arm (`spec-to-pr`) is what sets it. There is no local
// autonomous arm here, and nothing in this repository sets that variable, so the
// ported hook would be present and permanently asleep.
//
// That leg is the whole argument, deliberately. An earlier version of this
// paragraph also claimed `claude-code-action` never reads a branch's
// `.claude/settings.json` — which is unverified, and `agent-review-panel.yml`
// strips `.claude/` on the opposite assumption ("settings + hooks the SDK could
// load and run"). The rejection does not need it.
//
// So DISCLOSURE_TRAILER is written by the fixer prompts and read by nothing that
// can refuse a commit — the PR-body predicate below is the gate that holds.

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

/**
 * Hidden marker on the block `ensureDisclosed` appends to a PR body.
 *
 * Its job is idempotence and attribution: the agent may push to one PR many
 * times, and a human reading the diff of their own PR body deserves to see which
 * part they did not write.
 */
export const DISCLOSURE_BLOCK_MARKER = "<!-- agent-authorship -->";

/**
 * The exact sentence the pipeline writes when it discloses its own authorship.
 *
 * IT IS PAIRED WITH THE PREDICATE ABOVE, and a test asserts the pairing —
 * `disclosesAiAuthorship(DISCLOSURE_SENTENCE)` must be true. Without that, the
 * writer and the gate are two independent readings of the same English and can
 * drift apart silently, which is the whole failure this constant exists to end:
 * every agent-pushed PR satisfied the house attribution line
 * ("Generated with Claude Code") and none of them satisfied the gate, so a PR an
 * agent had worked on could never be promoted. No URL and no abbreviation in the
 * sentence, deliberately — the predicate splits on `.`, so a link would break the
 * clause in half and neither half would disclose anything.
 */
export const DISCLOSURE_SENTENCE =
  "Commits on this pull request were written autonomously by Claude Code";

/**
 * Append the disclosure to a PR body, unless it already discloses.
 *
 * Idempotent on BOTH halves: a body that already satisfies the predicate is
 * returned untouched (a human may have written their own sentence, and
 * overwriting it would be rude and pointless), and so is one already carrying
 * this block.
 *
 * @param {string} body the current PR body
 * @returns {{changed: boolean, body: string}}
 */
export function ensureDisclosed(body) {
  const current = String(body ?? "");
  if (disclosesAiAuthorship(current) || current.includes(DISCLOSURE_BLOCK_MARKER)) {
    return { changed: false, body: current };
  }
  const block = `${DISCLOSURE_BLOCK_MARKER}\n${DISCLOSURE_SENTENCE}.`;
  return { changed: true, body: current.trimEnd() === "" ? block : `${current.trimEnd()}\n\n${block}` };
}

/** True iff a single commit message carries the autonomous disclosure trailer. */
export function hasDisclosureTrailer(message) {
  return String(message ?? "").includes(DISCLOSURE_TRAILER);
}
