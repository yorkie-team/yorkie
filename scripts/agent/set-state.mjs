// Single-value agent STATE for the autonomous issue→PR pipeline.
//
// The pipeline's lifecycle used to be an additive pile of labels
// (`agent:iterating`, `agent:needs-human-review`) with nothing enforcing mutual
// exclusion — a PR could sit in two states at once, and `needs-human-review` was
// overloaded (it meant BOTH "promoted, please merge" AND "gave up, please
// rescue"). This module makes state a SINGLE value: exactly one `agent:<state>`
// label at a time, out of:
//
//   implementing → awaiting-ci → reviewing → fixing → ready | blocked
//
// IMPORTANT — the label is an ADVISORY PROJECTION, never a gate. No workflow
// `if:` and no script decision may read it: the author agent holds issues:write,
// so the label is forgeable. Gates stay on unforgeable signals (lens check runs,
// CI conclusion, job results, the `<!-- agent(-review)-paged -->` comment
// latch). The `reconcile` command re-derives the state from those signals and
// overwrites drift, so a stale/forged label self-heals.
//
// `agent:candidate` (issue provenance) is deliberately NOT a lifecycle label and
// is never stripped.
//
// Usage:
//   node ./scripts/agent/set-state.mjs <pr> <state> [--force] [--dry-run]
//   node ./scripts/agent/set-state.mjs reconcile <pr> [--dry-run]
//
// Pure helpers are exported and unit-tested (no gh). The CLI talks to GitHub via
// the `gh` CLI. Writing labels is FAIL-SAFE: any gh/API error logs and exits 0
// so an advisory label write can never break the pipeline (mirrors metrics.mjs).

import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { emitBestEffortWarning } from "./guard-verdict.mjs";
import { ciConclusion, CI_WORKFLOW_FILE } from "./checks.mjs";

// --- pure helpers (exported for tests; no gh) ------------------------------

/** The lifecycle states, in rough forward order. */
export const STATES = ["implementing", "awaiting-ci", "reviewing", "fixing", "ready", "blocked"];
export const LABEL_PREFIX = "agent:";
/** The current lifecycle labels. */
export const LIFECYCLE_LABELS = STATES.map((s) => `${LABEL_PREFIX}${s}`);
/** Pre-cutover labels that this machine replaces; stripped alongside the current
 * lifecycle labels so an in-flight PR ends up with exactly one new state label
 * (clean cutover). `agent:candidate` is intentionally absent from BOTH sets so
 * issue provenance is preserved. */
export const LEGACY_LIFECYCLE_LABELS = ["agent:iterating", "agent:needs-human-review"];
/** Every label computeLabelSet strips before adding the one new state label. */
export const MANAGED_LABELS = [...LIFECYCLE_LABELS, ...LEGACY_LIFECYCLE_LABELS];

/** "reviewing" → "agent:reviewing"; null for an unknown state. */
export function labelFor(state) {
  return STATES.includes(state) ? `${LABEL_PREFIX}${state}` : null;
}

/** "agent:reviewing" → "reviewing"; null if not a lifecycle label. */
export function stateFor(label) {
  const name = typeof label === "string" ? label : label && label.name;
  if (typeof name !== "string" || !name.startsWith(LABEL_PREFIX)) return null;
  const s = name.slice(LABEL_PREFIX.length);
  return STATES.includes(s) ? s : null;
}

/** Normalize a label (string or {name}) to its string name. */
function labelName(label) {
  return typeof label === "string" ? label : label && label.name;
}

/**
 * Mutual-exclusion core: the exact label set to apply for `newState` — strip
 * EVERY lifecycle label, keep everything else (non-agent labels, agent:candidate),
 * add exactly the one new state label. Idempotent; de-duplicated. Throws on an
 * unknown state (a wiring bug worth catching, not silencing).
 */
export function computeLabelSet(currentLabels, newState) {
  const label = labelFor(newState);
  if (!label) throw new Error(`unknown state: ${newState}`);
  const kept = (Array.isArray(currentLabels) ? currentLabels : [])
    .map(labelName)
    .filter((n) => typeof n === "string" && !MANAGED_LABELS.includes(n));
  return [...new Set([...kept, label])];
}

/** True iff two label collections hold the same SET of names (order-independent;
 * accepts string- or {name}-shaped labels). Used to decide whether a PR's labels
 * already match the desired normalized set — a first-label-equality check would
 * miss leftover duplicate lifecycle labels (drift) that still need collapsing. */
export function sameLabelSet(a, b) {
  const norm = (xs) => new Set((Array.isArray(xs) ? xs : []).map(labelName).filter(Boolean));
  const sa = norm(a);
  const sb = norm(b);
  if (sa.size !== sb.size) return false;
  for (const x of sa) if (!sb.has(x)) return false;
  return true;
}

// Best-effort legality (NOT a gate): guards against downgrading a PR through a
// stale/racy inline call. reconcile always --forces past this.
const TRANSITIONS = {
  implementing: ["awaiting-ci", "reviewing", "fixing", "blocked"],
  "awaiting-ci": ["reviewing", "fixing", "blocked"],
  reviewing: ["fixing", "ready", "blocked"],
  fixing: ["awaiting-ci", "reviewing", "blocked"],
  ready: ["blocked"], // ready→fixing needs an intervening push (→ --force/reconcile)
  blocked: [], // terminal: leaving `blocked` needs --force (human re-engaged) or reconcile
};

/** Is from→to a legal transition? `null`/unknown `from` → any (first assignment).
 * Same-state re-assert is always legal (idempotent). */
export function isValidTransition(from, to) {
  if (!STATES.includes(to)) return false;
  if (from == null || !STATES.includes(from)) return true;
  if (from === to) return true;
  return (TRANSITIONS[from] || []).includes(to);
}

/**
 * Pure projection from unforgeable signals → state, for reconcile. Precedence
 * (first match wins) mirrors the pipeline's own gate order.
 * signals: { isDraft, ciConclusion, lensBlocked, reviewChecksPresent,
 *            ciPagedLatch, reviewPagedLatch }
 */
export function deriveState(signals = {}) {
  const s = signals || {};
  if (s.ciPagedLatch || s.reviewPagedLatch) return "blocked";
  if (s.isDraft === false) return "ready"; // draft→ready flip is the promotion signal
  if (s.lensBlocked === true) return "fixing"; // a blocking lens failed, not yet paged
  if (s.reviewChecksPresent === true) return "reviewing"; // panel ran, not blocked, still draft
  if (s.ciConclusion === "success") return "reviewing"; // green → panel imminent
  if (s.isDraft === true) return "awaiting-ci"; // draft, CI pending/failed
  return "implementing";
}

// --- gh-backed CLI ---------------------------------------------------------

function gh(args) {
  return execFileSync("gh", args, { encoding: "utf8" });
}
function ghJson(args) {
  return JSON.parse(gh(args));
}

// Label writes need a token that can edit the PR. Prefer GH_MUTATION_TOKEN (a
// GitHub App token) when provided; fall back to the ambient `gh` token (local
// dry runs). Same pattern as mark-ready.mjs.
function ghMutate(args) {
  const token = process.env.GH_MUTATION_TOKEN;
  const env = token ? { ...process.env, GH_TOKEN: token } : process.env;
  return execFileSync("gh", args, { encoding: "utf8", env });
}

// Advisory: never break the pipeline. Log and exit 0 on any operational problem.
// The exit-0 is exactly why the failure also needs a breadcrumb a human can see:
// the `continue-on-error:` on every call site never observes it, so without the
// warning a stale agent:* label is indistinguishable from a label nobody set.
function bail(msg) {
  console.error(`set-state: ${msg}`);
  emitBestEffortWarning(`set-state failed: ${msg} — the PR's agent:* lifecycle label may be stale`);
  process.exit(0);
}

/** Current PR label names (strings). */
function readLabels(pr) {
  const data = ghJson(["pr", "view", pr, "--json", "labels"]);
  return (data.labels || []).map((l) => l.name).filter(Boolean);
}

/** Apply `newState` to the PR by REPLACING the full label set (atomic — the
 * computed set becomes the post-state, so exactly one lifecycle label survives,
 * and non-agent labels are carried through). */
function applyLabels(pr, labels, dryRun) {
  if (dryRun) {
    console.log(`[dry-run] would set PR #${pr} labels → ${labels.join(", ")}`);
    return;
  }
  const args = ["api", "-X", "PUT", `repos/{owner}/{repo}/issues/${pr}/labels`];
  for (const l of labels) args.push("-f", `labels[]=${l}`);
  ghMutate(args);
}

function cmdSet(pr, state, force, dryRun) {
  if (!pr || !state) return bailUsage();
  if (!STATES.includes(state)) {
    console.error(`set-state: unknown state '${state}' (expected: ${STATES.join(", ")})`);
    process.exit(2);
  }
  let current;
  try {
    current = readLabels(pr);
  } catch (e) {
    return bail(`could not read labels for PR #${pr}: ${e.message}`);
  }
  const from = current.map(stateFor).find((s) => s != null) ?? null;
  if (!force && !isValidTransition(from, state)) {
    console.log(`set-state: skipping illegal transition ${from} → ${state} for PR #${pr} (use --force / reconcile)`);
    process.exit(0);
  }
  try {
    applyLabels(pr, computeLabelSet(current, state), dryRun);
  } catch (e) {
    return bail(`could not set state '${state}' on PR #${pr}: ${e.message}`);
  }
  console.log(`set PR #${pr} → ${state}${from ? ` (was ${from})` : ""}`);
}

/** Gather the unforgeable signals reconcile projects from, and report whether the
 * evidence is COMPLETE. A failed fetch must NOT masquerade as "absent"/"pending":
 * that could make deriveState downgrade a correct blocked/fixing/reviewing to
 * awaiting-ci on a transient API error. On any fetch failure, `complete` is false
 * and the caller leaves the label untouched. Returns `{ complete, signals }`. */
function gatherSignals(pr) {
  const sig = {};
  let complete = true;
  let sha;
  try {
    const view = ghJson(["pr", "view", pr, "--json", "isDraft,headRefOid"]);
    sig.isDraft = view.isDraft;
    sha = view.headRefOid;
  } catch {
    complete = false;
  }
  if (!sha) {
    complete = false; // without the head SHA we can't read CI / lens checks
  } else {
    try {
      // Scoped to the CI workflow file by the API — same reason as mark-ready's
      // gate 1: a run's display name is not unique, and matching its path in JS
      // is parsing an attacker-influenced string.
      const runs =
        ghJson([
          "api",
          `repos/{owner}/{repo}/actions/workflows/${CI_WORKFLOW_FILE}/runs?head_sha=${sha}&per_page=100`,
        ]).workflow_runs || [];
      sig.ciConclusion = ciConclusion(runs); // genuinely null while in progress or absent
    } catch {
      complete = false; // do NOT coerce a failed fetch to null (would read as "pending")
    }
    try {
      const checks = ghJson(["api", `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`]).check_runs || [];
      const lens = checks.filter((c) => (c.name || "").startsWith("agent-review-"));
      sig.reviewChecksPresent = lens.length > 0;
      sig.lensBlocked = lens.some((c) => c.conclusion === "failure");
    } catch {
      complete = false;
    }
  }
  try {
    const pages = ghJson(["api", "--paginate", "--slurp", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]);
    const comments = Array.isArray(pages) ? pages.flat() : [];
    sig.ciPagedLatch = comments.some((c) => (c.body || "").includes("<!-- agent-paged -->"));
    sig.reviewPagedLatch = comments.some((c) => (c.body || "").includes("<!-- agent-review-paged -->"));
  } catch {
    complete = false; // the paged latch dominates deriveState — never guess it
  }
  return { complete, signals: sig };
}

function cmdReconcile(pr, dryRun) {
  if (!pr) return bailUsage();
  let current, complete, signals;
  try {
    current = readLabels(pr);
    ({ complete, signals } = gatherSignals(pr));
  } catch (e) {
    return bail(`could not reconcile PR #${pr}: ${e.message}`);
  }
  // Incomplete evidence → do NOT risk overwriting a correct label with a guess.
  // reconcile is best-effort and re-runs, so skipping just defers the self-heal.
  if (!complete) {
    console.log(`reconcile: incomplete signals for PR #${pr}; leaving the state label unchanged.`);
    return;
  }
  const state = deriveState(signals);
  const desired = computeLabelSet(current, state);
  // Compare the FULL set, not just the first lifecycle label — otherwise leftover
  // duplicate lifecycle labels (drift) would be skipped instead of collapsed.
  if (sameLabelSet(current, desired)) {
    console.log(`reconcile: PR #${pr} already normalized to ${state}; nothing to do.`);
    return;
  }
  const from = current.map(stateFor).find((s) => s != null) ?? null;
  try {
    applyLabels(pr, desired, dryRun); // reconcile always forces
  } catch (e) {
    return bail(`could not reconcile PR #${pr} to '${state}': ${e.message}`);
  }
  console.log(`reconciled PR #${pr} → ${state}${from ? ` (was ${from})` : ""}`);
}

function bailUsage() {
  console.error(
    "usage: set-state.mjs <pr> <state> [--force] [--dry-run]\n" +
      "       set-state.mjs reconcile <pr> [--dry-run]\n" +
      `       state ∈ {${STATES.join(", ")}}`,
  );
  process.exit(2);
}

// Only run the CLI when executed directly (not when imported for tests).
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const argv = process.argv;
  const dryRun = argv.includes("--dry-run");
  if (argv[2] === "reconcile") {
    cmdReconcile(argv[3], dryRun);
  } else {
    cmdSet(argv[2], argv[3], argv.includes("--force"), dryRun);
  }
}
