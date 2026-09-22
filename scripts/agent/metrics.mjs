// Agent effort/cost metrics for the autonomous issue→PR pipeline.
//
// Each agent session (kickoff `implement` / `ci-fix` / `review-fix`) appends a
// machine-readable record to a hidden LEDGER comment on the PR. When the PR is
// promoted to ready-for-review, the promote job renders one aggregated,
// human-readable SUMMARY comment:
//
//   - Total-cost: $1.23 (code-fix $1.10 + review $0.13)
//   - Total-tokens: ~120K weighted (~1.0M raw)
//   ...
//   - Cost: $1.10
//   - Tokens: ~110K weighted (~950K raw)
//
// Data source: `claude-execution-output.json` (the claude-code-action transcript)
// — its final `result` message carries num_turns / duration_ms / usage /
// modelUsage / total_cost_usd.
//
// Cost = model-reported `total_cost_usd`, the truest measure of how
// resource-intensive a run was: it already prices cache reads at ~1/10 of fresh
// input and the review panel's pricier/other models correctly. Raw tokens =
// input+output+cache (total processed) — but raw over-counts, since cache-read
// reprocessing is billed at ~1/10 yet summed at full weight. Weighted tokens
// re-weight the fields toward spend (see `weightedTokensFor`). Scope-size = PR
// diff lines.
//
// Pure helpers are exported and unit-tested (no gh). The CLI (`record` /
// `summarize`) talks to GitHub via the `gh` CLI (GH_TOKEN / GITHUB_TOKEN).
// Recording is FAIL-SAFE: any error exits 0 so metrics can never break the
// pipeline (the workflow steps are also continue-on-error).

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
// Node builtins only, transitively too: ask.mjs has no top-level imports at all,
// which is what makes it importable here. The `agent:tests` lane runs with
// scripts/agent/node_modules ABSENT, so a module that statically pulled in the
// Agent SDK would take this whole file down with it (ask.test.mjs enforces this).
import { classifyResult } from "./ask.mjs";
import { redactSecrets } from "./redact.mjs";
import { emitBestEffortWarning } from "./guard-verdict.mjs";

// Each session posts its OWN hidden metric comment (append-only) — no shared
// ledger to read-modify-write, so concurrent sessions can't overwrite each
// other's records. `summarize` aggregates them into one human-readable SUMMARY,
// posted FRESH at the bottom of the thread (the old summary is deleted, not
// edited in place, so the up-to-date one isn't buried mid-thread). It also folds
// the raw records INTO the summary as a hidden data block and then SWEEPS the
// per-session comments every round — they otherwise render as empty comment
// boxes that flood the thread, and their history is now preserved in the summary
// (so a later re-run still re-aggregates the full ledger). The terminal promote
// (`--final`) strips the data block for a clean final artifact and sweeps every
// remaining record.
export const METRIC_PREFIX = "<!-- agent-metric ";
export const SUMMARY_MARKER = "<!-- agent-metrics-summary -->";
// Hidden data block appended to the summary, carrying the raw ledger as JSON so
// the per-session comments can be swept without losing re-aggregation history.
export const SUMMARY_DATA_MARKER = "<!-- agent-metrics-data ";
// Keep summary body + data block under GitHub's 65 536-char comment cap. Records
// that don't fit keep their standalone comment and fold in on a later round.
export const SUMMARY_DATA_BUDGET = 55000;

// A STANDALONE per-session effort comment, deliberately not part of the machinery
// above. `@claude fix` is a maintainer-initiated one-off, and its cost has to be
// legible as its own line item — not folded into a PR-wide total whose next
// revision deletes and reposts it, and not hidden in a `METRIC_PREFIX` comment
// that `summarize` sweeps. This marker matches NEITHER of the two above
// (`agent-fix-effort` shares no prefix with `agent-metric ` or
// `agent-metrics-summary`), so the sweep and the summary cannot touch it and it
// stays where the maintainer read it. One comment per run, never edited in place:
// two `@claude fix` invocations are two spends and must show as two.
export const FIX_EFFORT_MARKER = "<!-- agent-fix-effort -->";

/**
 * Is this comment one WE wrote, rather than one that merely mentions a marker?
 *
 * `includes(marker)` was the old test, and it deleted other people's comments.
 * Both sweeps below run over EVERY comment on the PR, so any body containing the
 * literal marker string was removed — and the strings are `<!-- agent-metric … -->`
 * and `<!-- agent-metrics-summary -->`, which anything discussing this module
 * quotes as a matter of course.
 *
 * That is not hypothetical. On #681 (a PR about pipeline observability) the
 * on-demand review's own findings comment named `<!-- agent-metrics-summary -->`
 * while explaining the comment surfaces — so `summarize`, running five seconds
 * later in the same job, deleted the review. The run was green end to end and
 * `safeDeleteComment` is best-effort, so nothing failed and nothing logged: the
 * comment simply vanished. CodeRabbit reviewing metrics.mjs, a maintainer pasting
 * a marker, and the pipeline's own paged latch are all exposed the same way.
 *
 * Two conditions, both cheap:
 *   - POSITION. `renderSummary` and `serializeRecord` both emit the marker as the
 *     very first characters of the body. A quotation is prose *about* a marker and
 *     appears mid-sentence; ours is the body's opening. This alone fixes the bug.
 *   - AUTHOR. Every writer here posts through a token, so our comments are always
 *     a Bot. `user.type` is set by GitHub and cannot be chosen by a commenter.
 *
 * Fails toward KEEPING. An unknown author or a marker that is not at position 0 is
 * somebody else's comment, and leaving a stale summary behind costs a duplicate;
 * deleting the wrong one destroys work with no record that it happened.
 */
export function isOwnComment(comment, marker) {
  const c = comment && typeof comment === "object" ? comment : {};
  const body = typeof c.body === "string" ? c.body : "";
  if (!body.startsWith(marker)) return false;
  return Boolean(c.user && typeof c.user === "object" && c.user.type === "Bot");
}

// --- pure helpers (exported for tests; no gh) ------------------------------

/**
 * Per-token weights relative to fresh input, so a token total tracks spend
 * rather than raw throughput. `cache_read_input_tokens` (re-processing an
 * already-cached prefix) is billed at ~1/10 of fresh input but is otherwise
 * summed at full weight, which inflates the raw total; cache creation carries a
 * ~1.25x write premium. Output stays at 1x here — weighting it accurately needs
 * per-model pricing, which the model-reported `total_cost_usd` (kept as
 * `costUsd`) already applies exactly, so cost is the authoritative measure.
 */
export const TOKEN_WEIGHTS = { input: 1, output: 1, cacheCreation: 1.25, cacheRead: 0.1 };

/** Weighted (spend-representative) token count from a usage object. */
export function weightedTokensFor(usage) {
  const u = usage || {};
  return Math.round(
    (u.input_tokens || 0) * TOKEN_WEIGHTS.input +
      (u.output_tokens || 0) * TOKEN_WEIGHTS.output +
      (u.cache_creation_input_tokens || 0) * TOKEN_WEIGHTS.cacheCreation +
      (u.cache_read_input_tokens || 0) * TOKEN_WEIGHTS.cacheRead,
  );
}

/** Extract one session record from a parsed claude-execution-output.json array. */
export function parseExecution(messages, kind = "implement") {
  const arr = Array.isArray(messages) ? messages : [];
  const result = [...arr].reverse().find((m) => m && m.type === "result");
  if (!result) return null;
  const u = result.usage || {};
  const tokens =
    (u.input_tokens || 0) +
    (u.output_tokens || 0) +
    (u.cache_creation_input_tokens || 0) +
    (u.cache_read_input_tokens || 0);
  return {
    kind,
    models: Object.keys(result.modelUsage || {}),
    turns: result.num_turns || 0,
    tokens,
    weightedTokens: weightedTokensFor(u),
    durationMs: result.duration_ms || 0,
    costUsd: result.total_cost_usd || 0,
    sessionId: result.session_id || "",
  };
}

/** Sum turns/tokens/duration/cost across EVERY result message in a parsed
 * execution log — NOT last-wins like `parseExecution`. One review-panel round
 * makes many small SDK calls (lens samples + verifier calls) inside a single
 * process, so its total compute is a sum of every call, not "the last call's
 * numbers". `calls` is the count of result messages actually summed, so a
 * caller can tell "0 calls" (nothing to record) from "1 call, cheap round". */
export function sumExecutions(messages, kind = "review") {
  const arr = Array.isArray(messages) ? messages : [];
  const results = arr.filter((m) => m && m.type === "result");
  const models = new Set();
  let turns = 0, tokens = 0, weightedTokens = 0, durationMs = 0, costUsd = 0;
  for (const r of results) {
    const u = r.usage || {};
    tokens +=
      (u.input_tokens || 0) +
      (u.output_tokens || 0) +
      (u.cache_creation_input_tokens || 0) +
      (u.cache_read_input_tokens || 0);
    weightedTokens += weightedTokensFor(u);
    turns += r.num_turns || 0;
    durationMs += r.duration_ms || 0;
    costUsd += r.total_cost_usd || 0;
    for (const m of Object.keys(r.modelUsage || {})) models.add(m);
  }
  return {
    kind,
    models: [...models].sort(),
    turns,
    tokens,
    weightedTokens,
    durationMs,
    costUsd,
    sessionId: results.length ? results[results.length - 1].session_id || "" : "",
    calls: results.length,
  };
}

/**
 * The panel's TRUE wall-clock duration for one round, from review-panel.mjs's
 * `review-timing.json` (written next to the execution log). This exists because
 * the flat sum of `duration_ms` over the execution log is NOT wall-clock: the
 * panel runs its lenses — and each lens's samples + verifier calls —
 * CONCURRENTLY, so summing overcounts by the concurrency factor (a ~12-min panel
 * was reported as 36-63). When the timing file is present and sane, its `wallMs`
 * is the real elapsed time; absent or malformed → null, and the caller keeps the
 * summed value (unchanged for pre-instrumentation logs). `timingPath` overrides
 * the sibling default (for tests / explicit `--timing`).
 */
export function readWallMs(executionPath, timingPath) {
  const p = timingPath
    || (executionPath ? path.join(path.dirname(executionPath), "review-timing.json") : null);
  if (!p) return null;
  try {
    const ms = Number(JSON.parse(readFileSync(p, "utf8"))?.wallMs);
    return Number.isFinite(ms) && ms > 0 ? ms : null;
  } catch {
    return null;
  }
}

/** Per-message usage tally (raw + weighted tokens, turns, cost). */
function messageUsage(m) {
  const u = (m && m.usage) || {};
  return {
    tokens:
      (u.input_tokens || 0) +
      (u.output_tokens || 0) +
      (u.cache_creation_input_tokens || 0) +
      (u.cache_read_input_tokens || 0),
    weightedTokens: weightedTokensFor(u),
    turns: (m && m.num_turns) || 0,
    costUsd: (m && m.total_cost_usd) || 0,
  };
}

const emptyBucket = () => ({ costUsd: 0, weightedTokens: 0, tokens: 0, turns: 0, calls: 0 });
function addBucket(acc, u) {
  acc.costUsd += u.costUsd; acc.weightedTokens += u.weightedTokens;
  acc.tokens += u.tokens; acc.turns += u.turns; acc.calls += 1;
}

/**
 * Break a review execution log down by ATTRIBUTION — `{ lens: { role: bucket } }`,
 * role ∈ `detection` | `verifier` (anything else → `other`). Each SDK result
 * message carries `attribution: { lens, role }` (stamped by ask.mjs when the
 * panel tags the call); a message without it lands under lens `"unattributed"`,
 * role `"other"`, so a pre-instrumentation round is visible as one bucket rather
 * than silently dropped. This is the per-lens / detection-vs-verifier split the
 * flat `sumExecutions` total cannot give. Shape-safe: junk contributes nothing.
 */
export function attributionBreakdown(messages) {
  const out = {};
  for (const m of Array.isArray(messages) ? messages : []) {
    if (!m || m.type !== "result") continue;
    const a = (m.attribution && typeof m.attribution === "object") ? m.attribution : {};
    const lens = typeof a.lens === "string" && a.lens ? a.lens : "unattributed";
    const role = a.role === "detection" || a.role === "verifier" ? a.role : "other";
    out[lens] = out[lens] || {};
    out[lens][role] = out[lens][role] || emptyBucket();
    addBucket(out[lens][role], messageUsage(m));
  }
  return out;
}

/** Merge N `attributionBreakdown` objects (one per round) into one. */
export function aggregateAttribution(breakdowns) {
  const out = {};
  for (const bd of Array.isArray(breakdowns) ? breakdowns : []) {
    if (!bd || typeof bd !== "object") continue;
    for (const [lens, roles] of Object.entries(bd)) {
      if (!roles || typeof roles !== "object") continue;
      out[lens] = out[lens] || {};
      for (const [role, b] of Object.entries(roles)) {
        out[lens][role] = out[lens][role] || emptyBucket();
        const t = out[lens][role];
        t.costUsd += Number(b?.costUsd) || 0;
        t.weightedTokens += Number(b?.weightedTokens) || 0;
        t.tokens += Number(b?.tokens) || 0;
        t.turns += Number(b?.turns) || 0;
        t.calls += Number(b?.calls) || 0;
      }
    }
  }
  return out;
}

/** Aggregate an array of session records into pipeline-wide totals. */
export function aggregate(records) {
  const list = Array.isArray(records) ? records : [];
  const agents = [...new Set(list.flatMap((r) => r.models || []))].sort();
  // Attempt = review cycles: 1 = approved on the first review; +1 per review-fix
  // round. (Distinct from Sessions, which also counts CI-fix runs.)
  const reviewFixes = list.filter((r) => r.kind === "review-fix").length;
  const sum = (f) => list.reduce((s, r) => s + (Number(r[f]) || 0), 0);
  return {
    agents,
    sessions: list.length,
    attempt: reviewFixes + 1,
    turns: sum("turns"),
    tokens: sum("tokens"),
    // Records written before weighted tokens existed have no `weightedTokens`;
    // fall back to raw `tokens` for those so an in-flight PR's total isn't
    // silently under-counted mid-rollout.
    weightedTokens: list.reduce(
      (s, r) => s + (Number(r.weightedTokens) || Number(r.tokens) || 0),
      0,
    ),
    durationMs: sum("durationMs"),
    costUsd: sum("costUsd"),
  };
}

/**
 * Roll up review-panel `lensStats` entries (one per lens per round, from
 * `review-panel.mjs`'s `kind:"review"` records) into PR-wide totals: sample
 * agreement, severity-weighted raised/kept counts, verifier confirm/refute
 * outcomes. Summed across every round on the PR, not just the latest — a
 * finding that persists across rounds is raised/verified again each round
 * (mirrors how `aggregate()` sums Sessions/Turns/Tokens across every session
 * rather than reporting only the last one).
 */
export function aggregatePanelStats(entries) {
  const list = Array.isArray(entries) ? entries : [];
  const agreementCounts = { identical: 0, partial: 0, disjoint: 0, single: 0 };
  const raised = { critical: 0, major: 0, minor: 0, nit: 0 };
  const kept = { critical: 0, major: 0, minor: 0, nit: 0 };
  // Confidence of what each lens RAISED. Entries written before the field
  // existed contribute nothing at all — not even to `unknown` — so an all-zero
  // row means "no data yet", which is the honest reading for a PR whose rounds
  // predate the change. Within a round that does carry the field, `unknown`
  // counts findings the lens declined to rate.
  const raisedConfidence = { high: 0, medium: 0, low: 0, unknown: 0 };
  // `dropped` is absent from ledger entries written before the grounded-refute
  // change; those coerce to 0, which reads correctly — under the old rule the
  // drop count WAS `refutedHighConfidence`, so a mixed-history PR shows the
  // grounded drops it can actually account for rather than an inflated total.
  let sentToVerifier = 0, refuted = 0, refutedHighConfidence = 0, dropped = 0;
  // Absence claims ("there is no X"), which are refuted by finding ONE
  // counterexample rather than by failing to find the code. Absent from entries
  // written before the split existed, which coerce to 0.
  let absenceRaised = 0, absenceRefuted = 0, unresolved = 0;
  // Verifier sessions that THREW. Absent from pre-instrumentation entries, so
  // they coerce to 0 — but a non-zero value means those findings were never
  // filtered at all, which is the difference between a review and a raw dump.
  let errored = 0;
  // Prior-round findings this round's fresh pass re-found, so one verdict settled
  // both and only one session ran. Absent from entries written before the reuse
  // existed, which coerce to 0 — the honest reading, since those rounds really
  // did verify every carried-forward finding a second time.
  let reusedPriorVerdicts = 0;
  // WHY those sessions threw, split by `classifyResult`'s kind. Absent from
  // pre-instrumentation entries, which contribute nothing — so an all-zero
  // `failures` means "no data yet", and the renderer omits the line rather than
  // implying zero failures. `errored` above stays the wider count: it also
  // includes a folded wording whose verdict was never produced, which never
  // threw and so is deliberately not represented here.
  const failures = { apiError: 0, limit: 0, noOutput: 0, unknown: 0, total: 0 };
  // Novelty gate. Absent from entries written before it existed, which coerce to
  // 0 — an all-zero row reads as "the gate never fired", the honest reading for
  // both a pre-instrumentation round and a round where it ran inert.
  // `unknownOrigin` is the one that matters: it is how often git could not place
  // a finding, so `backlog: 0` with a high `unknownOrigin` means the gate is
  // blind (no --base-sha, a shallow clone, findings with no line) rather than
  // that nothing was relocated.
  let laneBlocking = 0, laneBacklog = 0, laneUnknownOrigin = 0;
  // Restatement collapsed by the clustering pass. `collapsed` is pure count
  // inflation removed — wordings of a defect already reported under another
  // finding. Absent from pre-clustering entries, which coerce to 0.
  let clustered = 0, collapsed = 0;
  for (const e of list) {
    if (!e || typeof e !== "object") continue;
    if (Object.prototype.hasOwnProperty.call(agreementCounts, e.agreement)) agreementCounts[e.agreement]++;
    for (const sev of ["critical", "major", "minor", "nit"]) {
      raised[sev] += Number(e.raised && e.raised[sev]) || 0;
      kept[sev] += Number(e.kept && e.kept[sev]) || 0;
    }
    for (const c of ["high", "medium", "low", "unknown"]) {
      raisedConfidence[c] += Number(e.raisedConfidence && e.raisedConfidence[c]) || 0;
    }
    sentToVerifier += Number(e.verifier && e.verifier.sentToVerifier) || 0;
    refuted += Number(e.verifier && e.verifier.refuted) || 0;
    refutedHighConfidence += Number(e.verifier && e.verifier.refutedHighConfidence) || 0;
    dropped += Number(e.verifier && e.verifier.dropped) || 0;
    absenceRaised += Number(e.verifier && e.verifier.absenceRaised) || 0;
    absenceRefuted += Number(e.verifier && e.verifier.absenceRefuted) || 0;
    unresolved += Number(e.verifier && e.verifier.unresolved) || 0;
    errored += Number(e.verifier && e.verifier.errored) || 0;
    reusedPriorVerdicts += Number(e.verifier && e.verifier.reusedPriorVerdicts) || 0;
    for (const k of Object.keys(failures)) {
      failures[k] += Number(e.verifier && e.verifier.failures && e.verifier.failures[k]) || 0;
    }
    laneBlocking += Number(e.lanes && e.lanes.blocking) || 0;
    laneBacklog += Number(e.lanes && e.lanes.backlog) || 0;
    laneUnknownOrigin += Number(e.lanes && e.lanes.unknownOrigin) || 0;
    clustered += Number(e.clusters && e.clusters.clustered) || 0;
    collapsed += Number(e.clusters && e.clusters.collapsed) || 0;
  }
  return {
    agreementCounts, raised, raisedConfidence, kept,
    verifier: { sentToVerifier, refuted, refutedHighConfidence, dropped, errored, absenceRaised, absenceRefuted, unresolved, reusedPriorVerdicts, failures },
    lanes: { blocking: laneBlocking, backlog: laneBacklog, unknownOrigin: laneUnknownOrigin },
    clusters: { clustered, collapsed },
  };
}

/**
 * Detect blocking→clean flips per lens across an ORDERED list of review records
 * (one `kind:"review"` record per round). `listAllComments` returns GitHub issue
 * comments in created_at-ascending order, so the array is already chronological =
 * round order — do NOT re-sort it. A flip is a lens that was blocking (kept
 * critical/major > 0) in one valid round and clean (non-blocking) in a LATER
 * valid round, i.e. it requested changes and then approved. This is the
 * cross-round analogue of the intra-round sample-agreement signal and is exactly
 * the failure PR #521 exhibited (blocking finding silently dropped next round).
 *
 * Rounds where a lens hit an infra/quota error (`infraError` set or
 * `samplesOk === 0`, which carry a synthetic blocking finding) are skipped for
 * that lens — an infra recovery the next round is not a review flip. Only
 * *valid* consecutive rounds of a lens are compared.
 *
 * HONEST LIMITATION: with only per-round severity counts we cannot tell a flip
 * caused by a genuine fix (good) from one caused by judge inconsistency (bad).
 * This is therefore an ADVISORY heads-up flag, not a defect count. Tightening it
 * to "the finding-relevant code did not change" needs per-round diff/line data
 * the ledger does not carry today. (If GitHub's comment ordering ever stopped
 * being chronological, flip direction could invert — accepted risk.)
 *
 * Shape-safe: records without `lensStats` (pre-instrumentation) or a `kept`
 * missing a severity key are tolerated and contribute nothing.
 *
 * @returns {{ flips: {lens: string, fromRound: number, toRound: number}[], byLens: Record<string, number> }}
 */
export function detectFlips(reviewRecords) {
  const list = Array.isArray(reviewRecords) ? reviewRecords : [];
  // Per lens id, the ordered subsequence of that lens's VALID round states
  // (true = blocking, false = clean), tagged with the record index for
  // human-readable r<from>→r<to> traceability.
  const byLensStates = new Map();
  list.forEach((rec, round) => {
    const lensStats = rec && Array.isArray(rec.lensStats) ? rec.lensStats : [];
    for (const e of lensStats) {
      if (!e || typeof e !== "object" || e.id == null) continue;
      // Skip infra/quota rounds — their synthetic blocking finding and next-round
      // recovery are not a review flip.
      if (e.infraError || Number(e.samplesOk) === 0) continue;
      const kept = e.kept || {};
      const blocking = (Number(kept.critical) || 0) + (Number(kept.major) || 0) > 0;
      if (!byLensStates.has(e.id)) byLensStates.set(e.id, []);
      byLensStates.get(e.id).push({ blocking, round });
    }
  });
  const flips = [];
  const byLens = {};
  for (const [lens, seq] of byLensStates) {
    for (let i = 1; i < seq.length; i++) {
      // A flip = an adjacent pair of valid rounds going blocking → clean.
      if (seq[i - 1].blocking && !seq[i].blocking) {
        flips.push({ lens, fromRound: seq[i - 1].round, toRound: seq[i].round });
        byLens[lens] = (byLens[lens] || 0) + 1;
      }
    }
  }
  return { flips, byLens };
}

/**
 * Severity weights for collapsing a {critical,major,minor,nit} vector into one
 * scalar. Mirrors the report's DoorDash-style scheme mapped onto this scale.
 * A critical finding is worth 8× a nit, so a lens that catches real problems
 * isn't scored like one that only flags style.
 */
export const SEVERITY_WEIGHTS = { critical: 4, major: 2, minor: 1, nit: 0.5 };

/**
 * Weighted scalar for a severity-count vector. This is an EFFORT/NOISE proxy
 * (how much reviewer attention the lens generated), NOT recall — recall needs
 * ground truth, which this per-PR layer deliberately does not have. Always
 * reported alongside the raw per-severity vector, never as a replacement for it.
 * Null/shape-safe: a missing object and missing keys count as 0.
 */
export function weightSeverity(counts) {
  const c = counts || {};
  return (
    (Number(c.critical) || 0) * SEVERITY_WEIGHTS.critical +
    (Number(c.major) || 0) * SEVERITY_WEIGHTS.major +
    (Number(c.minor) || 0) * SEVERITY_WEIGHTS.minor +
    (Number(c.nit) || 0) * SEVERITY_WEIGHTS.nit
  );
}

/** S/M/L from total lines changed in the PR diff. */
export function scopeSize(additions = 0, deletions = 0) {
  const changed = (Number(additions) || 0) + (Number(deletions) || 0);
  if (changed <= 50) return "S";
  if (changed <= 300) return "M";
  return "L";
}

export function formatTokens(n) {
  const v = Number(n) || 0;
  if (v >= 1e6) return `~${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `~${Math.round(v / 1e3)}K`;
  return `~${v}`;
}

export function formatMinutes(ms) {
  return `${Math.max(1, Math.round((Number(ms) || 0) / 60000))}m`;
}

export function formatUsd(n) {
  const v = Number(n) || 0;
  if (v > 0 && v < 0.01) return "<$0.01";
  return `$${v.toFixed(2)}`;
}

/** Render the human-readable summary comment body. `panelAgg`/`panelStats`
 * are omitted when the PR has no review-panel ledger records yet (kept
 * separate from the code-fix agent's numbers — review-fix, the agent that
 * responds to what the panel found, and review, the panel's own compute, are
 * easy to conflate by name, so their costs are rendered in separate sections
 * rather than folded into one set of totals). */
/** Per-lens / detection-vs-verifier token table for the review panel, or [] when
 * the log carries no attribution (pre-instrumentation rounds, or a code-only PR).
 * Cost leads with weighted tokens alongside — the same measure the sections above
 * use. This is the split that answers "which lens / is it the verifier?". */
function renderAttribution(attr) {
  const byLens = attr && typeof attr === "object" ? attr : {};
  const cell = (b) => (b && b.calls > 0 ? `${formatUsd(b.costUsd)} · ${formatTokens(b.weightedTokens)}` : "—");
  const det = emptyBucket(), ver = emptyBucket(), other = emptyBucket();
  const add = (acc, b) => {
    if (!b) return;
    acc.costUsd += b.costUsd; acc.weightedTokens += b.weightedTokens;
    acc.tokens += b.tokens; acc.turns += b.turns; acc.calls += b.calls;
  };
  const rows = [];
  for (const lens of Object.keys(byLens).sort()) {
    const roles = byLens[lens] || {};
    add(det, roles.detection); add(ver, roles.verifier); add(other, roles.other);
    // A lens with only an `other` bucket is an un-instrumented round; fold it into
    // the note below rather than showing an empty detection/verifier row.
    if ((roles.detection?.calls || 0) + (roles.verifier?.calls || 0) > 0) {
      rows.push(`| ${lens} | ${cell(roles.detection)} | ${cell(roles.verifier)} |`);
    }
  }
  if (!rows.length) return []; // nothing attributed → skip the table entirely
  const out = [
    "",
    "#### Token attribution — cost · weighted tokens, all rounds",
    "",
    "| Lens | Detection | Verifier |",
    "| --- | --- | --- |",
    ...rows,
    `| **Total** | ${cell(det)} | ${cell(ver)} |`,
    "",
    `- Detection vs verifier: ${formatUsd(det.costUsd)} vs ${formatUsd(ver.costUsd)} ` +
      `(${formatTokens(det.weightedTokens)} / ${formatTokens(ver.weightedTokens)} weighted).`,
  ];
  if (other.calls > 0) {
    out.push(
      `- ${formatUsd(other.costUsd)} · ${formatTokens(other.weightedTokens)} weighted from rounds recorded before attribution existed.`,
    );
  }
  return out;
}

/** Session kinds the pipeline itself records. Everything else renders as
 * `other`: metric records are parsed from ANY comment on a public repo (the
 * append-only ledger has no author gate), so `kind` is the one free-text field
 * an outsider could steer into this bot-authored summary — allow-list it, and
 * keep every other cell numeric. */
const LEDGER_KINDS = new Set(["implement", "ci-fix", "review-fix", "review"]);

/** Row cap for the ledger table. The record list is open-ended (any comment
 * can carry a metric record), and an unbounded table could push the summary
 * past GitHub's comment-size cap — which fails the post and silences the
 * whole summary, a denial of the one surface this exists to keep alive. 30
 * covers double MAX_REVIEW_ROUNDS' worth of sessions with room for kickoff
 * and CI fixes; when it overflows, the NEWEST rows win (they are the ones a
 * reader is diagnosing) and the omission is stated rather than silent. */
export const MAX_LEDGER_ROWS = 30;

/** A number cell, or "—" when the record never measured it. NEVER coerce a
 * missing value to 0 — `Number(null) === 0`, and a legacy record with no
 * `turns` did not do zero turns, it did an unmeasured amount. */
function measured(v) {
  if (v == null) return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

/**
 * The per-session ledger: every record `cmdSummarize` aggregates, as one
 * chronological row each, folded so the totals stay the headline. This is the
 * answer to "what did round 3 cost" — the aggregates above it can only answer
 * "what did everything cost". Zero new data collection: `dedupRecords` already
 * yields these in ledger (chronological) order, and rendering happens before
 * any sweep, so `--final` summaries carry the full table too.
 *
 * `review` and `review-fix` get a round ordinal from their position in that
 * order — the nth panel record IS round n, the same reading `detectFlips`
 * depends on. `implement`/`ci-fix` rows stay bare; the `#` column orders them.
 */
export function renderLedger(records) {
  const list = (Array.isArray(records) ? records : []).filter((r) => r && typeof r === "object");
  if (list.length === 0) return [];
  const roundOf = { review: 0, "review-fix": 0 };
  // Every row is BUILT (round ordinals and the # column come from the full
  // chronological list) and then the table keeps only the newest
  // MAX_LEDGER_ROWS — so a surviving row reads identically whether or not
  // older ones were dropped, and the drop itself is stated.
  const rows = list.map((r, i) => {
    const kind = LEDGER_KINDS.has(r.kind) ? r.kind : "other";
    const label = kind === "review" || kind === "review-fix" ? `${kind} (round ${++roundOf[kind]})` : kind;
    const turns = measured(r.turns);
    const weighted = measured(r.weightedTokens) ?? measured(r.tokens);
    const cost = measured(r.costUsd);
    const durMs = measured(r.durationMs);
    // Seconds under a minute, same rule as renderFixEffort: a session that died
    // in 18 seconds did no work, and "1m" hides exactly that.
    const duration = durMs == null ? "—" : durMs < 60_000 ? `${Math.round(durMs / 1000)}s` : formatMinutes(durMs);
    return `| ${i + 1} | ${label} | ${turns ?? "—"} | ${weighted == null ? "—" : formatTokens(weighted)} | ${cost == null ? "—" : formatUsd(cost)} | ${duration} |`;
  });
  const omitted = rows.length - MAX_LEDGER_ROWS;
  return [
    "",
    `<details><summary>Per-session ledger (${list.length} session${list.length === 1 ? "" : "s"})</summary>`,
    "",
    ...(omitted > 0
      ? [`_${omitted} earlier session(s) omitted — showing the most recent ${MAX_LEDGER_ROWS}; the totals above cover everything._`, ""]
      : []),
    "| # | Kind | Turns | Tokens (weighted) | Cost | Duration |",
    "| --- | --- | --- | --- | --- | --- |",
    ...rows.slice(-MAX_LEDGER_ROWS),
    "",
    "</details>",
  ];
}

export function renderSummary({ agg, panelAgg, panelStats, panelAttribution, flips, scope, records }) {
  const hasPanel = !!panelAgg && panelAgg.sessions > 0;
  // An on-demand `@claude review` posts a review record but no code-fix session,
  // so `aggregate([])` yields an all-zero `agg`. Rendering a "Code-fix agent"
  // section of zeros there reads as broken, so a review-only summary omits it and
  // collapses the total to the review figure alone.
  const hasCodeFix = agg.sessions > 0;
  const panelTokens = hasPanel ? panelAgg.tokens : 0;
  const panelWeighted = hasPanel ? panelAgg.weightedTokens : 0;
  const panelCost = hasPanel ? panelAgg.costUsd : 0;
  const totalWeighted = agg.weightedTokens + panelWeighted;
  const totalRaw = agg.tokens + panelTokens;
  const totalCost = agg.costUsd + panelCost;
  // Cost leads (it prices cache reads and pricier panel models correctly);
  // tokens are shown weighted with the raw total in parens. Per-section cost
  // and tokens carry the code-fix vs review split.
  const lines = [
    SUMMARY_MARKER,
    "## 🤖 Agent effort",
    "",
    hasCodeFix
      ? `- Total-cost: ${formatUsd(totalCost)} (code-fix ${formatUsd(agg.costUsd)} + review ${formatUsd(panelCost)})`
      : `- Total-cost: ${formatUsd(totalCost)}`,
    `- Total-tokens: ${formatTokens(totalWeighted)} weighted (${formatTokens(totalRaw)} raw)`,
  ];
  if (hasCodeFix) {
    lines.push(
      "",
      "### Code-fix agent",
      "",
      `- Cost: ${formatUsd(agg.costUsd)}`,
      `- Tokens: ${formatTokens(agg.weightedTokens)} weighted (${formatTokens(agg.tokens)} raw)`,
      `- Agents: ${agg.agents.length ? agg.agents.join(", ") : "unknown"}`,
      `- Scope-size: ${scope}`,
      `- Attempt: ${agg.attempt}`,
      `- Sessions: ${agg.sessions}`,
      `- Total-time: ${formatMinutes(agg.durationMs)}`,
      `- Turns: ${agg.turns}`,
    );
  }
  if (hasPanel) {
    const ac = panelStats?.agreementCounts || {};
    const r = panelStats?.raised || {};
    const rc = panelStats?.raisedConfidence || {};
    const k = panelStats?.kept || {};
    const v = panelStats?.verifier || {};
  // Absent on every round recorded before this shipped, so `?? {}` is what keeps
  // those rendering byte-identically rather than gaining an all-zero line.
  const vf = v.failures || {};
    const l = panelStats?.lanes || {};
    const c = panelStats?.clusters || {};
    const sampledRounds = (ac.identical || 0) + (ac.partial || 0) + (ac.disjoint || 0);
    lines.push(
      "",
      "### Review panel",
      "",
      `- Cost: ${formatUsd(panelAgg.costUsd)}`,
      `- Tokens: ${formatTokens(panelAgg.weightedTokens)} weighted (${formatTokens(panelAgg.tokens)} raw)`,
      `- Agents: ${panelAgg.agents.length ? panelAgg.agents.join(", ") : "unknown"}`,
      `- Rounds: ${panelAgg.sessions}`,
      `- Total-time: ${formatMinutes(panelAgg.durationMs)}`,
      `- Turns: ${panelAgg.turns}`,
      // Reliability = whether the judge agrees with ITSELF across the N samples
      // of a single round (intra-round self-consistency), NOT whether it agrees
      // with the truth. High agreement here means a stable judge, not a correct
      // one — see the meta-eval's reliability≠validity finding.
      `- Reliability (intra-round self-consistency, not correctness): ${ac.identical || 0} identical, ${ac.partial || 0} partial, ${ac.disjoint || 0} disjoint across ${sampledRounds} lens-rounds`,
      `- Findings raised: ${r.critical || 0} critical, ${r.major || 0} major, ${r.minor || 0} minor, ${r.nit || 0} nit`,
      // Severity and confidence are separate axes; this row is the check that
      // the lenses are actually USING the second one. All-`high` (or all
      // `unknown`) means doubt has nowhere to go but severity, which is the
      // clamp the coverage-first rubrics exist to remove. Confidence gates
      // nothing — a low-confidence `critical` blocks exactly like any other.
      `- Confidence of raised (does NOT gate): ${rc.high || 0} high, ${rc.medium || 0} medium, ${rc.low || 0} low, ${rc.unknown || 0} unrated`,
      // Weighted scalar companion to the raw vector above — an effort/noise
      // proxy, not recall (no ground truth here). Shown alongside, not instead.
      `- Weighted raised (effort proxy, not recall): ${weightSeverity(r)}`,
      `- Sent to verifier: ${v.sentToVerifier || 0}`,
      // Why that number is lower on a round ≥ 2 than the finding count suggests.
      // Each of these would have been one more verifier session under the old
      // unconditional re-check, so this is literally the sessions not run — and
      // without it a falling `Sent to verifier` is ambiguous between "the reuse
      // is working" and "the lenses raised less", which call for opposite
      // actions. Omitted at zero: round 1 has no prior findings at all, and
      // rounds recorded before the reuse existed coerce to 0 and stay quiet.
      ...(v.reusedPriorVerdicts
        ? [`- Prior findings re-found this round: ${v.reusedPriorVerdicts} (verified once, not twice)`]
        : []),
      // Read this BEFORE the refute counts. A verifier session that threw keeps
      // its finding, so an outage looks identical in the output to a verifier
      // that confirmed everything — this line is the only thing that tells them
      // apart, and a large value invalidates every number under it.
      ...(v.errored
        ? [`- ⚠️ Verifier ERRORED on ${v.errored} of ${v.sentToVerifier || 0} — those findings are UNFILTERED, not confirmed`]
        : []),
      // WHY they errored, which decides what to do about it: `api-error` is
      // transient and already retried, `turn-limit` means a ceiling is too low
      // for the work (the #614 failure — 8 of 18 verifications died at exactly
      // the presence ceiling and read as an outage), `no-output` means the model
      // ran and produced nothing usable. Absent on rounds recorded before this
      // shipped, and omitted when nothing threw, so quiet rounds stay quiet.
      // Counted in SESSIONS, while `errored` above counts FINDINGS — say so, and
      // do not subtract them. One clustered finding can consume several verifier
      // sessions (the representative, then one per folded wording), so the two
      // move independently in both directions: three sessions can throw for one
      // errored finding, and a finding can end with no verdict having thrown
      // nothing. An earlier draft rendered `errored - total` as a "folded
      // wording" remainder; that is arithmetic across two different units and it
      // goes negative on exactly the cluster case above.
      ...(vf && vf.total
        ? [`- Verifier failures: ${vf.total} session(s) threw — ${vf.apiError || 0} api-error, `
           + `${vf.limit || 0} turn-limit, ${vf.noOutput || 0} no-output`
           + `${vf.unknown ? `, ${vf.unknown} unknown` : ""}`]
        : []),
      // `dropped` ≤ `refutedHighConfidence`: a confident refutation only drops
      // the finding when it names a ground and cites what it read. The GAP is
      // the signal — it counts refutations the gate declined to act on.
      `- Refuted: ${v.refuted || 0} (${v.refutedHighConfidence || 0} high-confidence, ${v.dropped || 0} dropped)`,
      // Absence claims are refuted by finding ONE counterexample, so a low
      // refute rate here is not reassurance — it is the shape of a claim nobody
      // could settle riding through as though it had been checked. `unresolved`
      // is that case counted honestly; it still blocks.
      `- Absence claims: ${v.absenceRaised || 0} raised, ${v.absenceRefuted || 0} refuted by counterexample, ${v.unresolved || 0} unresolved (still blocking)`,
      // All four severities shown (critical/major lead as the blocking ones)
      // so the raw counts reconcile with the weighted scalar below, which
      // weights every severity — mirrors the "Findings raised" pair above.
      `- Survived to gate: ${k.critical || 0} critical, ${k.major || 0} major, ${k.minor || 0} minor, ${k.nit || 0} nit`,
      `- Weighted survived-to-gate: ${weightSeverity(k)}`,
      // Novelty gate. Read `unknownOrigin` FIRST: it counts blockers git could
      // not place, so a zero `relocated` next to a high `unknownOrigin` means
      // the gate ran blind (no --base-sha, a shallow clone, findings with no
      // line), not that nothing was moved. Only `relocated` demotes.
      `- Novelty gate: ${l.backlog || 0} relocated (demoted), ${l.blocking || 0} gating, ${l.unknownOrigin || 0} unplaceable`,
      // Restatement, not defects. A persistently high `collapsed` means the
      // lenses are re-describing the same problems rather than that the PR has
      // that many — the count inflation #578's disposition called out.
      `- Restatement collapsed: ${c.collapsed || 0} wording(s) folded into ${c.clustered || 0} finding(s)`,
    );
    // Advisory heads-up (NOT a verdict): a lens that blocked then approved across
    // rounds — the PR #521 pattern. Can't distinguish a genuine fix from judge
    // inconsistency here (see detectFlips), so the wording says "review manually".
    const flipList = flips?.flips || [];
    lines.push(
      `- ⚠️ Cross-round flips (blocking→clean; advisory, review manually): ${flipList.length}` +
        (flipList.length ? ` — ${flipList.map((f) => `${f.lens} r${f.fromRound}→r${f.toRound}`).join(", ")}` : ""),
    );
    // Per-lens / detection-vs-verifier token split (empty until rounds carry
    // attribution, so pre-instrumentation PRs render exactly as before).
    lines.push(...renderAttribution(panelAttribution));
  }
  // Per-session ledger last: the aggregates are the headline, the fold is the
  // receipt. Callers that pass no `records` (older invocations, tests) render
  // byte-identically to before the table existed.
  lines.push(...renderLedger(records));
  return lines.join("\n");
}

/** One session's record as a self-contained HIDDEN comment (renders invisibly).
 * The record fields are machine-generated (no free text), so the JSON never
 * contains the ` -->` terminator the parser splits on. */
/**
 * The standalone effort comment for ONE fix-agent session.
 *
 * REPORTS THE OUTCOME, not just the spend, and that is most of the value. A fix
 * run that dies on a 429 session limit produces a result message with
 * `subtype:"success"`, `is_error:true` and `num_turns:1` — from the outside it is
 * indistinguishable from a cheap successful round, and the only signal the
 * pipeline emits is the generic "the branch head is unchanged" page. That page
 * sends a maintainer looking for a code defect when the real answer is "retry
 * later". `classifyResult` already knows the difference; this surfaces it at the
 * one moment someone is reading.
 *
 * `rec` may be null (no result message at all) — a run that produced no execution
 * log still gets a comment saying so, because silence there is what made the
 * failure above take an artifact download to diagnose.
 */
/**
 * Did this claude-code-action session succeed, and if not, how did it fail?
 *
 * NOT `classifyResult` alone, and the difference is the whole point. That
 * function was written for Agent SDK sessions that return a json_schema payload:
 * its success arm requires `m.structured_output`, which a claude-code-action
 * transcript never carries. Handing it one makes EVERY successful fix run come
 * back `{ok:false, kind:"no-output"}` — so the effort comment would tell a
 * maintainer that a fix which worked had failed, and to escalate it. Verified
 * against a realistic success message before this existed.
 *
 * So success is decided HERE, from the fields this log actually has, and
 * `classifyResult` is used only for the failure taxonomy — which is the part it
 * gets right, and the part worth reusing (it is what recognises the 429 that
 * `subtype:"success"` disguises).
 *
 * Fail direction: toward FAILED. Anything other than a clean
 * `subtype:"success"` with no error flag is reported as a failure, because
 * over-reporting a failure costs a glance at the log while under-reporting one
 * loses the whole signal.
 */
export function classifyFixResult(result) {
  if (!result || typeof result !== "object") return null;
  const clean = result.subtype === "success"
    && !result.is_error
    && !result.api_error_status
    && result.terminal_reason !== "api_error"
    && result.terminal_reason !== "max_turns";
  return clean ? { ok: true } : classifyResult(result);
}

export function renderFixEffort({ rec, outcome, head, runUrl }) {
  const lines = ["### 🧾 Fix agent effort", ""];
  if (rec) {
    const weighted = Number(rec.weightedTokens) || Number(rec.tokens) || 0;
    lines.push(
      "| | |",
      "| --- | --- |",
      `| Turns | ${rec.turns} |`,
      `| Tokens | ${formatTokens(rec.tokens)} (weighted ${formatTokens(weighted)}) |`,
      `| Cost | ${formatUsd(rec.costUsd)} |`,
      // Seconds under a minute, unlike the PR-wide summary. `formatMinutes` floors
      // at "1m", and for a fix run the sub-minute case is the whole diagnostic: an
      // agent that died in 18 seconds did no work, and "1m" hides exactly that.
      `| Duration | ${(Number(rec.durationMs) || 0) < 60_000 ? `${Math.round((Number(rec.durationMs) || 0) / 1000)}s` : formatMinutes(rec.durationMs)} |`,
      `| Model | ${(rec.models || []).join(", ") || "unknown"} |`,
      "",
    );
  } else {
    lines.push("The fix agent produced no execution log, so no cost could be measured.", "");
  }
  if (outcome && outcome.ok === false) {
    // Spelled out per kind: "limit" is a ceiling the run hit and will hit again
    // unchanged, while a retryable api-error is a transient the same command
    // clears. Telling a maintainer to retry a `max_turns` failure wastes a round.
    // `reason` FIRST — the closed-vocabulary sentence built by `classifyResult`,
    // which carries no upstream text at all. This block renders into a PR comment,
    // so `detail` is the fallback only for records written before the vocabulary
    // existed (persisted session logs outlive a deploy), and it is redacted.
    const detail = String(outcome.reason || redactSecrets(String(outcome.detail || ""))).slice(0, 500);
    // A SESSION LIMIT is neither of the other two, and calling it either sends a
    // maintainer the wrong way. `classifyResult` marks it non-retryable — correct,
    // because retrying inside the same run cannot help — but it clears on its own
    // once the window resets, so the right advice is "wait, then re-run", not "a
    // human should look at this". Both reruns on #632/#648 failed this way and the
    // pipeline's only message was a generic page about the branch head.
    // `code` is decided from the RAW upstream text before redaction, so it is the
    // authoritative signal; the prose test remains for pre-vocabulary records.
    const sessionLimited = outcome.code ? outcome.code === "USAGE_LIMIT" : /\b(?:session|usage)\s+limit\b/i.test(detail);
    const advice = sessionLimited
      ? "This is an account **session limit**, not a defect in the PR — no work was done. "
        + "It clears when the window resets (the message above says when); comment `@claude fix` again after that."
      : outcome.kind === "limit"
        ? "This is a hard ceiling, not a transient — re-running as-is will hit it again."
        : outcome.retryable
          ? "This looks transient. Comment `@claude fix` again to retry."
          : "Not retryable as-is; a human should take a look.";
    lines.push(
      `**Outcome: failed (${outcome.kind}${outcome.status ? ` ${outcome.status}` : ""})** — ${detail || "no detail reported"}`,
      "",
      advice,
      "",
    );
  } else if (outcome && outcome.ok) {
    lines.push("**Outcome: completed.**", "");
  }
  if (head) lines.push(`Findings were read from \`${String(head).slice(0, 8)}\`.`, "");
  if (runUrl) lines.push(`[Workflow run](${runUrl})`, "");
  // Separate from the review panel's effort summary by design — see
  // FIX_EFFORT_MARKER. Stated in the comment so the two are not read as
  // duplicates of each other.
  lines.push("_This covers the on-demand fix agent only. The review panel's own effort is reported separately._");
  return `${lines.join("\n")}\n${FIX_EFFORT_MARKER}`;
}

export function serializeRecord(rec) {
  return `${METRIC_PREFIX}${JSON.stringify(rec)} -->`;
}

/** Recover the record from a single metric comment body; null if not one. */
export function parseMetricComment(body) {
  const m = /<!-- agent-metric ([\s\S]*?) -->/.exec(body || "");
  if (!m) return null;
  try {
    return JSON.parse(m[1]);
  } catch {
    return null;
  }
}

/** Serialize the cumulative ledger into the summary's hidden data block. */
export function serializeSummaryData(records) {
  return `${SUMMARY_DATA_MARKER}${JSON.stringify(records ?? [])} -->`;
}

/** Records embedded in a summary body; [] if none / unparseable. */
export function parseSummaryData(body) {
  const m = /<!-- agent-metrics-data ([\s\S]*?) -->/.exec(body || "");
  if (!m) return [];
  try {
    const arr = JSON.parse(m[1]);
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

/**
 * Dedup records by `sessionId`, keeping first occurrence so the caller's order
 * (embedded/older first, then this round's standalone) is preserved — detectFlips
 * depends on chronological order. Records with no `sessionId` (legacy) are all
 * kept, since there is no key to collapse them on.
 */
export function dedupRecords(records) {
  const seen = new Set();
  const out = [];
  for (const r of Array.isArray(records) ? records : []) {
    const id = r && r.sessionId;
    if (id) {
      if (seen.has(id)) continue;
      seen.add(id);
    }
    if (r) out.push(r);
  }
  return out;
}

// --- gh-backed CLI ---------------------------------------------------------

export function gh(args) {
  return execFileSync("gh", args, { encoding: "utf8" });
}
export function ghJson(args) {
  return JSON.parse(gh(args));
}

export function resolvePrByIssue(issue) {
  // The kickoff creates a branch `agent/<issue>-<slug>`; find the open PR for it.
  // --limit well above the default 30 so a busy repo's PR list isn't truncated
  // before ours is seen.
  const prs = ghJson(["pr", "list", "--state", "open", "--limit", "500", "--json", "number,headRefName"]);
  const prefix = `agent/${issue}-`;
  const hit = prs.find((p) => (p.headRefName || "").startsWith(prefix));
  return hit ? String(hit.number) : "";
}

// ALL comment pages, not just the first 100 — a chatty PR exceeds one page, and
// missing pages would drop metric records or the summary marker. `--slurp` wraps
// the paginated responses in a JSON array of pages, which we flatten.
export function listAllComments(pr) {
  const pages = ghJson(["api", "--paginate", "--slurp", `repos/{owner}/{repo}/issues/${pr}/comments?per_page=100`]);
  return Array.isArray(pages) ? pages.flat() : [];
}

function postComment(pr, body) {
  gh(["api", "-X", "POST", `repos/{owner}/{repo}/issues/${pr}/comments`, "-f", `body=${body}`]);
}

// Best-effort delete: metrics must never fail the pipeline, so a comment we
// can't remove (already gone, permission) is logged and skipped, not fatal.
function safeDeleteComment(id) {
  try {
    gh(["api", "-X", "DELETE", `repos/{owner}/{repo}/issues/comments/${id}`]);
  } catch (e) {
    console.error(`metrics: could not delete comment ${id}: ${e.message}`);
  }
}

function parseArgs(argv) {
  const a = {};
  for (let i = 3; i < argv.length; i++) {
    if (!argv[i].startsWith("--")) continue;
    const key = argv[i].slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith("--")) {
      a[key] = true; // boolean flag (e.g. --final)
    } else {
      a[key] = next;
      i++;
    }
  }
  return a;
}

// Metrics must NEVER fail the pipeline: log and exit 0 on any problem.
// `consequence` is OPT-IN per call site, because bail() serves two different
// sentences: genuine failures ("could not post the summary") and normal
// no-ops ("no metrics recorded yet"). Warning on the no-ops would teach
// readers to ignore the annotation — precision is what makes it worth having.
function bail(msg, consequence) {
  console.error(`metrics: ${msg}`);
  if (consequence) emitBestEffortWarning(`metrics: ${msg} — ${consequence}`);
  process.exit(0);
}

function cmdRecord(args) {
  const pr = args.pr || (args.issue ? resolvePrByIssue(args.issue) : "");
  if (!pr) return bail("no PR resolved (need --pr, or --issue with an open agent/<issue>- PR)");
  const kind = args.kind || "implement";
  let messages;
  try {
    messages = JSON.parse(readFileSync(args.execution, "utf8"));
  } catch (e) {
    return bail(`cannot read execution log ${args.execution}: ${e.message}`, "this session's cost is missing from the effort ledger");
  }
  let rec;
  if (kind === "review") {
    // review-panel.mjs is one process making MANY internal SDK calls — sum
    // every result message, don't take the last (parseExecution's contract).
    rec = sumExecutions(messages, kind);
    if (rec.calls === 0) return bail("no result messages in the review execution log");
    delete rec.calls;
    // Prefer the panel's real wall-clock over the concurrency-inflated sum of
    // per-call duration_ms (see readWallMs). Falls back to the summed value when
    // the timing file is absent (older logs / a crash before it was written).
    const wallMs = readWallMs(args.execution, args.timing);
    if (wallMs != null) rec.durationMs = wallMs;
    // Per-lens / detection-vs-verifier token split from the SAME messages. Carried
    // on the record so `summarize` can aggregate it across rounds. Empty object on
    // an un-instrumented log — harmless, just yields no attribution table.
    rec.attribution = attributionBreakdown(messages);
    // Sample-agreement/verifier-outcome data is optional and best-effort: a
    // missing/malformed file must never block recording the cost that WAS
    // captured, so log and move on rather than bail.
    if (args["lens-stats"]) {
      try {
        rec.lensStats = JSON.parse(readFileSync(args["lens-stats"], "utf8"));
      } catch (e) {
        console.error(`metrics: could not read lens-stats ${args["lens-stats"]}: ${e.message}`);
      }
    }
  } else {
    rec = parseExecution(messages, kind);
    if (!rec) return bail("no result message in the execution log");
  }
  try {
    // Append-only: POST this session's own metric comment. No read-modify-write,
    // so concurrent sessions can't clobber each other's records.
    gh(["api", "-X", "POST", `repos/{owner}/{repo}/issues/${pr}/comments`, "-f", `body=${serializeRecord(rec)}`]);
  } catch (e) {
    return bail(`could not record metrics for PR #${pr}: ${e.message}`, "this session's cost is missing from the effort ledger");
  }
  console.log(`recorded ${rec.kind} for PR #${pr}: turns=${rec.turns} tokens=${rec.tokens} ${formatMinutes(rec.durationMs)}`);
}

/**
 * Post the standalone fix-agent effort comment.
 *
 * ALWAYS POSTS, even with no execution log and even on a failed run: the comment
 * is the record that a fix attempt happened and what it cost, and the runs worth
 * recording most are the ones that went wrong. `bail` here would reproduce the
 * silence this exists to end.
 */
function cmdEffort(args) {
  const pr = args.pr;
  if (!pr) return bail("effort needs --pr");
  let messages = null;
  try {
    messages = JSON.parse(readFileSync(args.execution, "utf8"));
  } catch (e) {
    console.error(`metrics: cannot read execution log ${args.execution}: ${e.message}`);
  }
  const rec = messages ? parseExecution(messages, args.kind || "review-fix") : null;
  // The last result message. Absent → no outcome line rather than a fabricated one.
  const result = Array.isArray(messages) ? [...messages].reverse().find((m) => m && m.type === "result") : null;
  const outcome = classifyFixResult(result);
  const body = renderFixEffort({ rec, outcome, head: args.head, runUrl: args["run-url"] });
  try {
    postComment(pr, body);
  } catch (e) {
    return bail(`could not post fix-effort comment for PR #${pr}: ${e.message}`, "the fix run's cost/outcome comment is missing from the PR");
  }
  console.log(
    `posted fix-agent effort for PR #${pr}`
    + (rec ? `: turns=${rec.turns} tokens=${rec.tokens} ${formatUsd(rec.costUsd)}` : " (no execution log)"),
  );
}

function cmdSummarize(args) {
  const pr = args.pr;
  if (!pr) return bail("summarize needs --pr");
  let comments, prInfo;
  try {
    comments = listAllComments(pr);
    prInfo = ghJson(["pr", "view", pr, "--json", "additions,deletions"]);
  } catch (e) {
    return bail(`could not read metrics/PR for #${pr}: ${e.message}`, "the agent-effort summary was not refreshed");
  }
  // Records live in two places: this round's fresh per-session <!-- agent-metric -->
  // comments (append-only, one per session), and the CUMULATIVE ledger embedded in
  // the prior summary's data block. Earlier rounds' standalone comments were swept
  // once folded into the summary, so the summary is now their only copy — read both
  // and dedup by sessionId (embedded first = older → keeps chronological order for
  // detectFlips).
  const embedded = comments.flatMap((c) => parseSummaryData(c.body || ""));
  const standalone = comments.map((c) => parseMetricComment(c.body || "")).filter(Boolean);
  const records = dedupRecords([...embedded, ...standalone]);
  if (records.length === 0) return bail(`no metrics recorded for PR #${pr}; skipping summary`);
  // Code-fix agent (implement/ci-fix/review-fix) and review panel (review) are
  // kept as separate aggregates — see renderSummary's doc comment for why.
  const codeFixRecords = records.filter((r) => r.kind !== "review");
  const panelRecords = records.filter((r) => r.kind === "review");
  const agg = aggregate(codeFixRecords);
  const panelAgg = panelRecords.length ? aggregate(panelRecords) : null;
  const panelStats = panelRecords.length
    ? aggregatePanelStats(panelRecords.flatMap((r) => (Array.isArray(r.lensStats) ? r.lensStats : [])))
    : null;
  // Per-lens / detection-vs-verifier token split, summed across every round.
  const panelAttribution = panelRecords.length
    ? aggregateAttribution(panelRecords.map((r) => r.attribution).filter(Boolean))
    : null;
  // `panelRecords` is in ledger (chronological = round) order — detectFlips
  // relies on that, so pass it as-is without re-sorting.
  const flips = panelRecords.length ? detectFlips(panelRecords) : null;
  const scope = scopeSize(prInfo.additions, prInfo.deletions);
  // Decide what raw ledger to persist inside the summary. On a non-final round we
  // embed the cumulative records so the per-session comments can be swept without
  // losing re-aggregation history; prior-embedded records are the summary's ONLY
  // copy, so they are always kept, and fresh standalone records fold in while the
  // block stays under the comment-size cap (any that don't fit keep their
  // standalone comment and fold in later — no data is dropped). On --final the
  // PR is terminal (no future re-aggregation), so we strip the block for a clean
  // artifact and sweep every remaining record.
  const isFinal = !!args.final;
  const priorIds = new Set(embedded.map((r) => r && r.sessionId).filter(Boolean));
  const embedList = [];
  if (!isFinal) {
    embedList.push(...embedded);
    for (const r of standalone) {
      if (r && r.sessionId && priorIds.has(r.sessionId)) continue; // already embedded
      if (serializeSummaryData([...embedList, r]).length > SUMMARY_DATA_BUDGET) break;
      embedList.push(r);
    }
  }
  const embeddedIds = new Set(embedList.map((r) => r && r.sessionId).filter(Boolean));
  try {
    // Post the summary FRESH (not upsert-in-place): a prior summary was pinned at
    // its original creation point (often an early paged hand-off), so editing it
    // leaves the up-to-date summary buried mid-thread. Posting new lands it at the
    // BOTTOM where a human looks; the old one is deleted just below.
    const summary = renderSummary({ agg, panelAgg, panelStats, panelAttribution, flips, scope, records });
    postComment(pr, isFinal ? summary : `${summary}\n${serializeSummaryData(embedList)}`);
  } catch (e) {
    return bail(`could not post summary for PR #${pr}: ${e.message}`, "the agent-effort summary was not refreshed");
  }
  // Cleanup is best-effort (never fail the pipeline). Delete the OLD summary
  // comment(s) so only the fresh bottom one remains.
  for (const c of comments) {
    if (isOwnComment(c, SUMMARY_MARKER)) safeDeleteComment(c.id);
  }
  // Sweep the hidden per-session agent-metric comments EVERY round: each renders
  // as an empty comment box that floods the thread. On --final, sweep them all
  // (terminal — the summary captured their totals). Otherwise sweep only the
  // records we actually folded into the summary's data block (embeddedIds), so a
  // record left out by the size cap keeps its standalone copy and is not lost.
  // (SUMMARY_MARKER "agent-metrics-" never matches METRIC_PREFIX "agent-metric ",
  // so this can't touch the summary just posted.)
  for (const c of comments) {
    if (!isOwnComment(c, METRIC_PREFIX)) continue;
    if (isFinal) {
      safeDeleteComment(c.id);
      continue;
    }
    const rec = parseMetricComment(c.body || "");
    if (rec && rec.sessionId && embeddedIds.has(rec.sessionId)) safeDeleteComment(c.id);
  }
  console.log(
    `posted agent-effort summary for PR #${pr} (sessions=${agg.sessions} turns=${agg.turns} tokens=${agg.tokens})` +
      (isFinal ? " [final: reposted at bottom, swept all records]" : ` [folded ${embedList.length} record(s), swept per-session comments]`),
  );
}

// Only run the CLI when executed directly (not when imported for tests).
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const cmd = process.argv[2];
  const args = parseArgs(process.argv);
  if (cmd === "record") cmdRecord(args);
  else if (cmd === "summarize") cmdSummarize(args);
  else if (cmd === "effort") cmdEffort(args);
  else {
    console.error(
      "usage: metrics.mjs <record|summarize|effort> [--pr N | --issue N] [--execution PATH] " +
        "[--kind implement|ci-fix|review-fix|review] [--lens-stats PATH] [--final] " +
        "[--head SHA] [--run-url URL]",
    );
    process.exit(2);
  }
}
