import { test } from "node:test";
import assert from "node:assert/strict";
import {
  isOwnComment,
  renderFixEffort,
  classifyFixResult,
  FIX_EFFORT_MARKER,
  parseExecution,
  sumExecutions,
  readWallMs,
  attributionBreakdown,
  aggregateAttribution,
  aggregate,
  aggregatePanelStats,
  detectFlips,
  scopeSize,
  formatTokens,
  formatMinutes,
  formatUsd,
  weightedTokensFor,
  weightSeverity,
  SEVERITY_WEIGHTS,
  renderSummary,
  renderLedger,
  MAX_LEDGER_ROWS,
  serializeRecord,
  parseMetricComment,
  serializeSummaryData,
  parseSummaryData,
  dedupRecords,
  METRIC_PREFIX,
  SUMMARY_MARKER,
  SUMMARY_DATA_MARKER,
} from "./metrics.mjs";

const resultMsg = (over = {}) => ({
  type: "result",
  num_turns: 27,
  duration_ms: 89 * 60000,
  total_cost_usd: 1.23,
  session_id: "s1",
  modelUsage: { "claude-opus-4-8": {} },
  usage: {
    input_tokens: 100,
    output_tokens: 34633,
    cache_creation_input_tokens: 136293,
    cache_read_input_tokens: 800000,
  },
  ...over,
});

test("parseExecution: pulls turns/tokens(incl cache)/time/model from the last result", () => {
  const rec = parseExecution([{ type: "assistant" }, resultMsg()], "implement");
  assert.equal(rec.kind, "implement");
  assert.deepEqual(rec.models, ["claude-opus-4-8"]);
  assert.equal(rec.turns, 27);
  assert.equal(rec.tokens, 100 + 34633 + 136293 + 800000); // raw total incl. cache
  // weighted: cache reads at 0.1x, cache writes at 1.25x, input/output at 1x
  assert.equal(rec.weightedTokens, Math.round(100 + 34633 + 136293 * 1.25 + 800000 * 0.1));
  assert.equal(rec.durationMs, 89 * 60000);
  // no result message → null (caller treats as nothing to record)
  assert.equal(parseExecution([{ type: "assistant" }]), null);
  assert.equal(parseExecution("garbage"), null);
});

test("sumExecutions: sums EVERY result message (not last-wins like parseExecution)", () => {
  const a = resultMsg({ num_turns: 3, duration_ms: 1000, total_cost_usd: 1.5, modelUsage: { "claude-opus-4-8": {} }, usage: { input_tokens: 10, output_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 } });
  const b = resultMsg({ num_turns: 5, duration_ms: 2000, total_cost_usd: 2.25, modelUsage: { "claude-sonnet-5": {} }, usage: { input_tokens: 20, output_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 }, session_id: "s2" });
  const rec = sumExecutions([{ type: "assistant" }, a, b], "review");
  assert.equal(rec.kind, "review");
  assert.deepEqual(rec.models, ["claude-opus-4-8", "claude-sonnet-5"]); // unique, sorted, across all calls
  assert.equal(rec.turns, 8); // 3 + 5, not just the last (5)
  assert.equal(rec.tokens, 30); // 10 + 20
  assert.equal(rec.weightedTokens, 30); // input-only usage → unweighted (summed, not last-wins)
  assert.equal(rec.durationMs, 3000);
  assert.equal(rec.costUsd, 3.75); // 1.5 + 2.25, not just the last
  assert.equal(rec.sessionId, "s2"); // last call's session id
  assert.equal(rec.calls, 2);
  // no result messages at all → calls:0, all-zero (caller treats as nothing to record)
  const empty = sumExecutions([{ type: "assistant" }]);
  assert.equal(empty.calls, 0);
  assert.equal(empty.turns, 0);
  assert.equal(sumExecutions("garbage").calls, 0);
});

test("readWallMs: reads wallMs from review-timing.json sibling of the execution log", async () => {
  const { mkdtempSync, writeFileSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const path = (await import("node:path")).default;
  const dir = mkdtempSync(path.join(tmpdir(), "wallms-"));
  const exec = path.join(dir, "review-execution.json");
  writeFileSync(exec, "[]");

  // No timing file yet → null (caller keeps the summed duration).
  assert.equal(readWallMs(exec), null);

  // Sibling present and sane → its wallMs.
  writeFileSync(path.join(dir, "review-timing.json"), JSON.stringify({ wallMs: 12 * 60000 }));
  assert.equal(readWallMs(exec), 12 * 60000);

  // Explicit timingPath overrides the sibling default.
  const explicit = path.join(dir, "other.json");
  writeFileSync(explicit, JSON.stringify({ wallMs: 5000 }));
  assert.equal(readWallMs(exec, explicit), 5000);

  // Non-positive / malformed / missing → null (fail-safe, never throws).
  writeFileSync(path.join(dir, "review-timing.json"), JSON.stringify({ wallMs: 0 }));
  assert.equal(readWallMs(exec), null);
  writeFileSync(path.join(dir, "review-timing.json"), "not json");
  assert.equal(readWallMs(exec), null);
  assert.equal(readWallMs(path.join(dir, "nope", "review-execution.json")), null);
  assert.equal(readWallMs(null), null);
});

test("aggregate: sums across sessions; attempt counts review-fix rounds + 1", () => {
  const recs = [
    { kind: "implement", models: ["claude-opus-4-8"], turns: 10, tokens: 500000, weightedTokens: 150000, costUsd: 2, durationMs: 600000 },
    { kind: "ci-fix", models: ["claude-opus-4-8"], turns: 5, tokens: 200000, weightedTokens: 60000, costUsd: 0.5, durationMs: 120000 },
    { kind: "review-fix", models: ["claude-sonnet-5"], turns: 8, tokens: 300000, weightedTokens: 90000, costUsd: 1, durationMs: 300000 },
  ];
  const agg = aggregate(recs);
  assert.deepEqual(agg.agents, ["claude-opus-4-8", "claude-sonnet-5"]); // unique, sorted
  assert.equal(agg.sessions, 3);
  assert.equal(agg.attempt, 2); // one review-fix → attempt 2
  assert.equal(agg.turns, 23);
  assert.equal(agg.tokens, 1000000);
  assert.equal(agg.weightedTokens, 300000);
  assert.equal(agg.costUsd, 3.5);
  assert.equal(agg.durationMs, 1020000);
  // pre-rollout records lack weightedTokens → fall back to raw tokens (not 0)
  assert.equal(aggregate([{ tokens: 500000 }]).weightedTokens, 500000);
  assert.equal(aggregate([{ tokens: 500000, weightedTokens: 120000 }]).weightedTokens, 120000);
  // no review-fix → attempt 1
  assert.equal(aggregate([{ kind: "implement" }]).attempt, 1);
  assert.equal(aggregate([]).sessions, 0);
});

test("aggregatePanelStats: rolls up lens/round entries — agreement, severity-weighted raised/kept, verifier tallies", () => {
  const entries = [
    {
      agreement: "identical",
      raised: { critical: 1, major: 0, minor: 2, nit: 0 },
      kept: { critical: 1, major: 0, minor: 2, nit: 0 },
      // pre-grounded-refute entry: no `dropped` field at all (see below)
      verifier: { sentToVerifier: 1, refuted: 0, refutedHighConfidence: 0 },
    },
    {
      agreement: "partial",
      raised: { critical: 0, major: 2, minor: 0, nit: 1 },
      raisedConfidence: { high: 1, medium: 1, low: 1, unknown: 0 },
      kept: { critical: 0, major: 1, minor: 0, nit: 1 },
      verifier: { sentToVerifier: 2, refuted: 1, refutedHighConfidence: 1, dropped: 1, reusedPriorVerdicts: 2 },
    },
  ];
  const rolled = aggregatePanelStats(entries);
  assert.deepEqual(rolled.agreementCounts, { identical: 1, partial: 1, disjoint: 0, single: 0 });
  assert.deepEqual(rolled.raised, { critical: 1, major: 2, minor: 2, nit: 1 });
  assert.deepEqual(rolled.kept, { critical: 1, major: 1, minor: 2, nit: 1 });
  // a ledger entry written before `dropped` existed contributes 0, not NaN — the
  // ledger is append-only, so a mid-PR upgrade always produces this mixed shape.
  // The absence/unresolved fields tell the same append-only story: entries
  // written before the claim-type split contribute 0, not NaN.
  assert.deepEqual(rolled.verifier, {
    sentToVerifier: 3, refuted: 1, refutedHighConfidence: 1, dropped: 1, errored: 0,
    absenceRaised: 0, absenceRefuted: 0, unresolved: 0,
    // Only the second entry carries it: 2 + a missing field, not 2 + NaN. Rounds
    // that predate the reuse really did re-verify every carried-forward finding,
    // so 0 is the honest contribution rather than merely the safe one.
    reusedPriorVerdicts: 2,
    // Same append-only story once more: neither entry carries `failures`, so it
    // rolls up all-zero rather than NaN. An all-zero total is what makes the
    // renderer omit the line — "no data yet", not "nothing failed".
    failures: { apiError: 0, limit: 0, noOutput: 0, unknown: 0, total: 0 },
  });
  // Same append-only story for confidence: the first entry predates the field
  // entirely, so it contributes nothing — NOT four `unknown`s. Rounds recorded
  // before the split are absent data, not findings a lens declined to rate, and
  // conflating the two would make the "is the lens using confidence?" signal
  // unreadable on exactly the PRs that straddle the change.
  assert.deepEqual(rolled.raisedConfidence, { high: 1, medium: 1, low: 1, unknown: 0 });
  // tolerant of junk/empty input — never throws, never blocks recording
  assert.deepEqual(aggregatePanelStats([]).verifier, {
    sentToVerifier: 0, refuted: 0, refutedHighConfidence: 0, dropped: 0, errored: 0,
    absenceRaised: 0, absenceRefuted: 0, unresolved: 0, reusedPriorVerdicts: 0,
    failures: { apiError: 0, limit: 0, noOutput: 0, unknown: 0, total: 0 },
  });
  // Junk in the nested field coerces per-key rather than poisoning the tally —
  // it is read from an append-only ledger, so a malformed entry must cost its
  // own counts and nothing else.
  assert.deepEqual(aggregatePanelStats([{ verifier: { failures: "junk" } }]).verifier.failures,
    { apiError: 0, limit: 0, noOutput: 0, unknown: 0, total: 0 });
  assert.deepEqual(aggregatePanelStats([{ verifier: { failures: { limit: 2, total: 2 } } }]).verifier.failures,
    { apiError: 0, limit: 2, noOutput: 0, unknown: 0, total: 2 });
  assert.deepEqual(aggregatePanelStats([]).raisedConfidence, { high: 0, medium: 0, low: 0, unknown: 0 });
  assert.deepEqual(aggregatePanelStats([{ raisedConfidence: "junk" }]).raisedConfidence, { high: 0, medium: 0, low: 0, unknown: 0 });
  assert.deepEqual(aggregatePanelStats(null).agreementCounts, { identical: 0, partial: 0, disjoint: 0, single: 0 });
  assert.deepEqual(aggregatePanelStats([null, "junk", {}]).raised, { critical: 0, major: 0, minor: 0, nit: 0 });
});

// One review record (a `kind:"review"` ledger comment) carrying the given lens
// states. `spec` maps lensId → per-round override; here we build a single round.
const reviewRound = (lenses) => ({
  kind: "review",
  lensStats: lenses.map((l) => ({
    id: l.id,
    samplesOk: l.samplesOk ?? 2,
    ...(l.infraError ? { infraError: l.infraError } : {}),
    kept: { critical: 0, major: l.blocking ? 1 : 0, minor: 0, nit: 0, ...(l.kept || {}) },
  })),
});

test("detectFlips: flags a lens that went blocking→clean across two valid rounds", () => {
  const records = [
    reviewRound([{ id: "correctness", blocking: true }]),
    reviewRound([{ id: "correctness", blocking: false }]),
  ];
  const { flips, byLens } = detectFlips(records);
  assert.deepEqual(flips, [{ lens: "correctness", fromRound: 0, toRound: 1 }]);
  assert.deepEqual(byLens, { correctness: 1 });
});

test("detectFlips: stays-blocking or stays-clean is not a flip; clean→blocking is not a flip", () => {
  const stayBlocking = [reviewRound([{ id: "sec", blocking: true }]), reviewRound([{ id: "sec", blocking: true }])];
  const stayClean = [reviewRound([{ id: "sec", blocking: false }]), reviewRound([{ id: "sec", blocking: false }])];
  const escalate = [reviewRound([{ id: "sec", blocking: false }]), reviewRound([{ id: "sec", blocking: true }])];
  assert.deepEqual(detectFlips(stayBlocking).flips, []);
  assert.deepEqual(detectFlips(stayClean).flips, []);
  assert.deepEqual(detectFlips(escalate).flips, []); // only blocking→clean counts
});

test("detectFlips: an infra/quota round between valid rounds is skipped, not a flip", () => {
  // blocking → (infra error) → blocking: the infra round is dropped, leaving
  // blocking→blocking on the valid subsequence, so NO flip.
  const withInfraError = [
    reviewRound([{ id: "design", blocking: true }]),
    reviewRound([{ id: "design", blocking: true, infraError: "429 quota" }]),
    reviewRound([{ id: "design", blocking: true }]),
  ];
  assert.deepEqual(detectFlips(withInfraError).flips, []);
  // samplesOk:0 is treated the same as infraError (both mark an infra round).
  const withZeroSamples = [
    reviewRound([{ id: "design", blocking: true }]),
    reviewRound([{ id: "design", blocking: true, samplesOk: 0 }]),
    reviewRound([{ id: "design", blocking: true }]),
  ];
  assert.deepEqual(detectFlips(withZeroSamples).flips, []);
});

test("detectFlips: lenses are independent — one flips, another does not", () => {
  const records = [
    reviewRound([{ id: "correctness", blocking: true }, { id: "tests", blocking: true }]),
    reviewRound([{ id: "correctness", blocking: false }, { id: "tests", blocking: true }]),
  ];
  const { flips, byLens } = detectFlips(records);
  assert.deepEqual(flips, [{ lens: "correctness", fromRound: 0, toRound: 1 }]);
  assert.deepEqual(byLens, { correctness: 1 });
});

test("detectFlips: empty / single-round / shape-junk inputs never throw and find nothing", () => {
  assert.deepEqual(detectFlips([]).flips, []);
  assert.deepEqual(detectFlips(null).flips, []);
  assert.deepEqual(detectFlips([reviewRound([{ id: "correctness", blocking: true }])]).flips, []); // one round
  // pre-instrumentation records (no lensStats) and a lens missing `kept` are tolerated
  assert.deepEqual(detectFlips([{ kind: "review" }, { kind: "review", lensStats: [{ id: "x" }] }]).flips, []);
  assert.deepEqual(detectFlips([null, "junk", { lensStats: "nope" }]).flips, []);
});

test("scopeSize: S/M/L thresholds on total diff lines", () => {
  assert.equal(scopeSize(10, 5), "S");
  assert.equal(scopeSize(50, 0), "S");
  assert.equal(scopeSize(60, 100), "M");
  assert.equal(scopeSize(300, 0), "M");
  assert.equal(scopeSize(400, 200), "L");
});

test("formatTokens / formatMinutes / formatUsd: human-friendly", () => {
  assert.equal(formatTokens(1_000_000), "~1.0M");
  assert.equal(formatTokens(6_200_000), "~6.2M");
  assert.equal(formatTokens(34_733), "~35K");
  assert.equal(formatTokens(0), "~0");
  assert.equal(formatMinutes(89 * 60000), "89m");
  assert.equal(formatMinutes(5000), "1m"); // floor at 1m
  assert.equal(formatUsd(4.1), "$4.10");
  assert.equal(formatUsd(0), "$0.00");
  assert.equal(formatUsd(0.004), "<$0.01"); // sub-cent, non-zero
});

test("weightedTokensFor: cache reads 0.1x, cache writes 1.25x, input/output 1x", () => {
  assert.equal(
    weightedTokensFor({ input_tokens: 100, output_tokens: 200, cache_creation_input_tokens: 1000, cache_read_input_tokens: 5000 }),
    Math.round(100 + 200 + 1000 * 1.25 + 5000 * 0.1), // 100 + 200 + 1250 + 500 = 2050
  );
  assert.equal(weightedTokensFor({}), 0);
  assert.equal(weightedTokensFor(undefined), 0);
});

test("weightSeverity: severity-weighted scalar, shape/null-safe", () => {
  // critical 4, major 2, minor 1, nit 0.5
  assert.equal(weightSeverity({ critical: 1, major: 1, minor: 1, nit: 1 }), 7.5);
  assert.equal(weightSeverity({ critical: 2 }), 8); // missing keys treated as 0
  assert.equal(weightSeverity({}), 0);
  assert.equal(weightSeverity(null), 0); // missing object treated as 0
  assert.equal(weightSeverity(undefined), 0);
  // non-numeric junk coerces to 0, never NaN
  assert.equal(weightSeverity({ critical: "x", major: 3 }), 6);
  // weights are the documented DoorDash-style scheme mapped to this scale
  assert.deepEqual(SEVERITY_WEIGHTS, { critical: 4, major: 2, minor: 1, nit: 0.5 });
});

test("renderSummary: matches the requested bullet format", () => {
  const md = renderSummary({
    agg: { agents: ["claude-opus-4-8"], sessions: 1, attempt: 1, turns: 27, tokens: 1_000_000, weightedTokens: 300_000, costUsd: 3.3, durationMs: 89 * 60000 },
    scope: "M",
  });
  assert.match(md, /### Code-fix agent/);
  assert.match(md, /- Cost: \$3\.30/);
  assert.match(md, /- Tokens: ~300K weighted \(~1\.0M raw\)/);
  assert.match(md, /- Agents: claude-opus-4-8/);
  assert.match(md, /- Scope-size: M/);
  assert.match(md, /- Attempt: 1/);
  assert.match(md, /- Sessions: 1/);
  assert.match(md, /- Total-time: 89m/);
  assert.match(md, /- Turns: 27/);
  // no review-panel records on this PR → no "Review panel" section at all,
  // and the top totals collapse the review side to zero
  assert.doesNotMatch(md, /### Review panel/);
  assert.match(md, /- Total-cost: \$3\.30 \(code-fix \$3\.30 \+ review \$0\.00\)/);
  assert.match(md, /- Total-tokens: ~300K weighted \(~1\.0M raw\)/);
});

// --- per-session ledger (Phase 2) -------------------------------------------

test("renderLedger: one chronological row per record, with round ordinals for panel kinds", () => {
  const lines = renderLedger([
    { kind: "implement", turns: 78, weightedTokens: 1_900_000, costUsd: 14.2, durationMs: 42 * 60000 },
    { kind: "ci-fix", turns: 31, weightedTokens: 600_000, costUsd: 4.15, durationMs: 18 * 60000 },
    { kind: "review", turns: 210, weightedTokens: 3_100_000, costUsd: 11.8, durationMs: 18 * 60000 },
    { kind: "review-fix", turns: 64, weightedTokens: 1_200_000, costUsd: 8.75, durationMs: 21 * 60000 },
    { kind: "review", turns: 195, weightedTokens: 2_800_000, costUsd: 9.87, durationMs: 17 * 60000 },
  ]);
  const md = lines.join("\n");
  assert.match(md, /Per-session ledger \(5 sessions\)/);
  assert.match(md, /\| 1 \| implement \| 78 \| ~1\.9M \| \$14\.20 \| 42m \|/);
  // The nth panel record IS round n — the same chronological reading
  // detectFlips depends on.
  assert.match(md, /\| 3 \| review \(round 1\) \| 210 \|/);
  assert.match(md, /\| 4 \| review-fix \(round 1\) \| 64 \|/);
  assert.match(md, /\| 5 \| review \(round 2\) \| 195 \|/);
  // Folded, so the totals stay the headline.
  assert.match(md, /<details><summary>/);
});

test("renderLedger: a missing value renders as —, never as 0", () => {
  // Number(null) === 0: a legacy record with no `turns` did not do zero turns,
  // it did an unmeasured amount. Same rule as guard-verdict's `n()`.
  const md = renderLedger([{ kind: "implement" }]).join("\n");
  assert.match(md, /\| 1 \| implement \| — \| — \| — \| — \|/);
  // …while a genuine zero is a real measured value and renders as one.
  const zero = renderLedger([{ kind: "ci-fix", turns: 0, weightedTokens: 0, costUsd: 0, durationMs: 0 }]).join("\n");
  assert.match(zero, /\| 1 \| ci-fix \| 0 \| ~0 \| \$0\.00 \| 0s \|/);
});

test("renderLedger: sub-minute durations render in seconds, and raw tokens back up missing weighted", () => {
  const md = renderLedger([{ kind: "review-fix", turns: 1, tokens: 50_000, costUsd: 0.4, durationMs: 18_000 }]).join("\n");
  assert.match(md, /\| 18s \|/); // an agent that died in 18 seconds did no work — "1m" hides that
  assert.match(md, /~50K/);
});

test("renderLedger: an unrecognized kind renders as `other`, never verbatim", () => {
  // Metric records are parsed from ANY comment (the append-only ledger has no
  // author gate), so `kind` is the one free-text field an outsider could steer
  // into this bot-authored summary. The #681 ledger-injection shape.
  const md = renderLedger([{ kind: "<!-- agent-review-paged -->", turns: 1, costUsd: 0.1, durationMs: 1000 }]).join("\n");
  assert.ok(!md.includes("agent-review-paged"), "attacker kind rendered verbatim");
  assert.match(md, /\| 1 \| other \| 1 \|/);
});

test("renderLedger: rows are capped at the newest MAX_LEDGER_ROWS, and the omission is stated", () => {
  // The record list is open-ended (any comment can carry a record), and an
  // unbounded table could push the summary past GitHub's comment-size cap —
  // failing the post and silencing the whole summary.
  const records = Array.from({ length: MAX_LEDGER_ROWS + 7 }, (_, i) => ({
    kind: "review", turns: i + 1, weightedTokens: 1000, costUsd: 0.1, durationMs: 60000,
  }));
  const md = renderLedger(records).join("\n");
  assert.match(md, new RegExp(`Per-session ledger \\(${MAX_LEDGER_ROWS + 7} sessions\\)`));
  assert.match(md, new RegExp(`_7 earlier session\\(s\\) omitted — showing the most recent ${MAX_LEDGER_ROWS};`));
  // The oldest rows are the dropped ones; the newest survive with their
  // original chronological # and round ordinals intact.
  assert.doesNotMatch(md, /\| 1 \| review \(round 1\) \|/);
  assert.match(md, new RegExp(`\\| 8 \\| review \\(round 8\\) \\|`));
  assert.match(md, new RegExp(`\\| ${MAX_LEDGER_ROWS + 7} \\| review \\(round ${MAX_LEDGER_ROWS + 7}\\) \\|`));
  assert.equal((md.match(/\| \d+ \| review /g) || []).length, MAX_LEDGER_ROWS);
  // At exactly the cap, nothing is omitted and no note renders.
  const exact = renderLedger(records.slice(0, MAX_LEDGER_ROWS)).join("\n");
  assert.doesNotMatch(exact, /omitted/);
});

test("renderLedger and renderSummary: no records → no table, byte-identical summary", () => {
  assert.deepEqual(renderLedger([]), []);
  assert.deepEqual(renderLedger(null), []);
  const args = { agg: { agents: [], sessions: 1, attempt: 1, turns: 1, tokens: 1, weightedTokens: 1, costUsd: 1, durationMs: 60000 }, scope: "S" };
  assert.equal(renderSummary({ ...args, records: [] }), renderSummary(args));
});

test("renderSummary: the ledger table renders when records are passed", () => {
  const md = renderSummary({
    agg: { agents: [], sessions: 1, attempt: 1, turns: 5, tokens: 10, weightedTokens: 10, costUsd: 0.1, durationMs: 60000 },
    scope: "S",
    records: [{ kind: "implement", turns: 5, weightedTokens: 10, costUsd: 0.1, durationMs: 60000 }],
  });
  assert.match(md, /Per-session ledger \(1 session\)/);
  // Last, after every aggregate section — the fold is the receipt, not the headline.
  assert.ok(md.indexOf("Per-session ledger") > md.indexOf("### Code-fix agent"));
});

test("renderSummary: with review-panel data, renders a separate section + combined total", () => {
  const md = renderSummary({
    agg: { agents: ["claude-opus-4-8"], sessions: 3, attempt: 2, turns: 31, tokens: 1_100_000, weightedTokens: 300_000, costUsd: 3.3, durationMs: 94 * 60000 },
    panelAgg: { agents: ["claude-opus-4-8", "claude-sonnet-5"], sessions: 2, turns: 15, tokens: 300_000, weightedTokens: 40_000, costUsd: 0.8, durationMs: 27 * 60000 },
    panelStats: {
      agreementCounts: { identical: 6, partial: 1, disjoint: 1, single: 0 },
      raised: { critical: 2, major: 5, minor: 3, nit: 1 },
      // Deliberately does NOT sum to the 11 raised above: one finding came from
      // a lens that emitted no confidence (→ unrated). The row is a distribution
      // over the same findings, not a second count of them.
      raisedConfidence: { high: 4, medium: 3, low: 3, unknown: 1 },
      kept: { critical: 1, major: 3, minor: 2, nit: 2 },
      verifier: { sentToVerifier: 7, refuted: 3, refutedHighConfidence: 2, dropped: 1 },
    },
    flips: { flips: [{ lens: "correctness", fromRound: 0, toRound: 1 }], byLens: { correctness: 1 } },
    scope: "M",
  });
  assert.match(md, /### Code-fix agent/);
  assert.match(md, /### Review panel/);
  assert.match(md, /- Rounds: 2/);
  assert.match(md, /- Total-time: 27m/);
  // reliability wording makes explicit this is self-consistency, not correctness
  assert.match(md, /- Reliability \(intra-round self-consistency, not correctness\): 6 identical, 1 partial, 1 disjoint across 8 lens-rounds/);
  assert.match(md, /- Findings raised: 2 critical, 5 major, 3 minor, 1 nit/);
  // Exact row: label, order, counts, and the "does NOT gate" disclaimer. The
  // disclaimer is the load-bearing part — without it a reader can reasonably
  // assume a low-confidence critical was filtered, which is the misreading the
  // severity/confidence split exists to prevent.
  assert.match(md, /- Confidence of raised \(does NOT gate\): 4 high, 3 medium, 3 low, 1 unrated/);
  // weighted companion is additional, not a replacement for the raw vector:
  // 2*4 + 5*2 + 3*1 + 1*0.5 = 21.5
  assert.match(md, /- Weighted raised \(effort proxy, not recall\): 21\.5/);
  assert.match(md, /- Sent to verifier: 7/);
  assert.match(md, /- Refuted: 3 \(2 high-confidence, 1 dropped\)/);
  // raw line shows all four severities so it reconciles with the weighted scalar
  assert.match(md, /- Survived to gate: 1 critical, 3 major, 2 minor, 2 nit/);
  // 1*4 + 3*2 + 2*1 + 2*0.5 = 13
  assert.match(md, /- Weighted survived-to-gate: 13/);
  // advisory cross-round flip line, with the offending lens + round transition
  assert.match(md, /- ⚠️ Cross-round flips \(blocking→clean; advisory, review manually\): 1 — correctness r0→r1/);
  // per-section cost + weighted/raw tokens carry the split
  assert.match(md, /### Review panel\n\n- Cost: \$0\.80\n- Tokens: ~40K weighted \(~300K raw\)/);
  // combined totals: cost $3.30 + $0.80 = $4.10; weighted 300K + 40K = 340K; raw 1.1M + 300K = 1.4M
  assert.match(md, /- Total-cost: \$4\.10 \(code-fix \$3\.30 \+ review \$0\.80\)/);
  assert.match(md, /- Total-tokens: ~340K weighted \(~1\.4M raw\)/);
});

// On-demand `@claude review`: a review record but NO code-fix session, so the
// code-fix aggregate is all zeros (aggregate([])). The summary must OMIT the
// Code-fix agent section entirely rather than render it full of $0.00 / 0, and
// the top total must collapse to the review figure (no misleading code-fix split).
test("renderSummary: review-only (no code-fix) omits the code-fix section", () => {
  const md = renderSummary({
    // The exact shape aggregate([]) returns for zero code-fix records.
    agg: { agents: [], sessions: 0, attempt: 1, turns: 0, tokens: 0, weightedTokens: 0, durationMs: 0, costUsd: 0 },
    panelAgg: { agents: ["claude-opus-5", "claude-sonnet-5"], sessions: 1, turns: 15, tokens: 300_000, weightedTokens: 40_000, costUsd: 0.8, durationMs: 27 * 60000 },
    panelStats: {
      agreementCounts: { identical: 6, partial: 0, disjoint: 0, single: 0 },
      raised: { critical: 0, major: 1, minor: 2, nit: 0 },
      raisedConfidence: { high: 2, medium: 1, low: 0, unknown: 0 },
      kept: { critical: 0, major: 1, minor: 2, nit: 0 },
      verifier: { sentToVerifier: 1, refuted: 0, refutedHighConfidence: 0, dropped: 0 },
    },
    flips: { flips: [], byLens: {} },
    scope: "M",
  });
  // No code-fix section, and none of its bullets leak in.
  assert.doesNotMatch(md, /### Code-fix agent/);
  assert.doesNotMatch(md, /- Scope-size:/);
  assert.doesNotMatch(md, /- Attempt:/);
  assert.doesNotMatch(md, /- Sessions:/);
  // The review panel section still renders in full.
  assert.match(md, /### Review panel/);
  assert.match(md, /- Cost: \$0\.80/);
  // Total collapses to the review figure — NOT the "(code-fix … + review …)" split.
  assert.match(md, /- Total-cost: \$0\.80\n/);
  assert.doesNotMatch(md, /code-fix .* \+ review/);
  assert.match(md, /- Total-tokens: ~40K weighted \(~300K raw\)/);
});

// A result message tagged the way ask.mjs tags it. No cache fields, so weighted
// tokens == raw tokens (input+output weight 1x), keeping the arithmetic obvious.
const attrMsg = (lens, role, { inp = 0, out = 0, cost = 0, turns = 0 } = {}) => ({
  type: "result",
  num_turns: turns,
  total_cost_usd: cost,
  usage: { input_tokens: inp, output_tokens: out },
  ...(lens || role ? { attribution: { lens, role } } : {}),
});
const cents = (n) => Number((n).toFixed(2));

test("attributionBreakdown: buckets by lens and detection/verifier role", () => {
  const bd = attributionBreakdown([
    attrMsg("correctness", "detection", { inp: 100, out: 20, cost: 0.3, turns: 5 }),
    attrMsg("correctness", "detection", { inp: 100, out: 20, cost: 0.3, turns: 5 }), // 2 samples
    attrMsg("correctness", "verifier", { inp: 400, out: 10, cost: 0.5, turns: 8 }),
    attrMsg("security", "detection", { inp: 80, out: 10, cost: 0.2, turns: 4 }),
    attrMsg(null, null, { inp: 5, cost: 0.01 }),   // no attribution → unattributed/other
    { type: "user" },                                // non-result → ignored
    null,                                            // junk → ignored
  ]);
  assert.equal(bd.correctness.detection.calls, 2);
  assert.equal(cents(bd.correctness.detection.costUsd), 0.6);
  assert.equal(bd.correctness.detection.tokens, 240);   // (100+20) × 2
  assert.equal(bd.correctness.detection.weightedTokens, 240);
  assert.equal(bd.correctness.detection.turns, 10);
  assert.equal(bd.correctness.verifier.calls, 1);
  assert.equal(bd.correctness.verifier.tokens, 410);
  assert.equal(bd.security.detection.calls, 1);
  // An un-attributed message is preserved, not dropped.
  assert.equal(bd.unattributed.other.calls, 1);
  assert.equal(bd.unattributed.other.tokens, 5);
  // Shape-safe on junk.
  assert.deepEqual(attributionBreakdown(null), {});
});

test("aggregateAttribution: sums per-round breakdowns", () => {
  const r1 = attributionBreakdown([attrMsg("correctness", "detection", { inp: 100, cost: 0.3 })]);
  const r2 = attributionBreakdown([
    attrMsg("correctness", "detection", { inp: 50, cost: 0.2 }),
    attrMsg("correctness", "verifier", { inp: 200, cost: 0.4 }),
  ]);
  const agg = aggregateAttribution([r1, r2]);
  assert.equal(agg.correctness.detection.calls, 2);
  assert.equal(cents(agg.correctness.detection.costUsd), 0.5);
  assert.equal(agg.correctness.detection.tokens, 150);
  assert.equal(agg.correctness.verifier.calls, 1);
  assert.deepEqual(aggregateAttribution(null), {});
});

test("renderSummary: renders the per-lens detection/verifier token table", () => {
  const bucket = (cost, weighted) => ({ costUsd: cost, weightedTokens: weighted, tokens: weighted, turns: 1, calls: 1 });
  const md = renderSummary({
    agg: { agents: [], sessions: 0, attempt: 1, turns: 0, tokens: 0, weightedTokens: 0, durationMs: 0, costUsd: 0 },
    panelAgg: { agents: ["claude-opus-5"], sessions: 1, turns: 8, tokens: 1000, weightedTokens: 1000, costUsd: 1.0, durationMs: 60000 },
    panelStats: {},
    panelAttribution: {
      correctness: { detection: bucket(0.3, 120), verifier: bucket(0.5, 400) },
      security: { detection: bucket(0.2, 90) }, // no verifier finding → "—"
    },
    flips: { flips: [] },
    scope: "M",
  });
  assert.match(md, /#### Token attribution/);
  assert.match(md, /\| correctness \| \$0\.30 · ~120 \| \$0\.50 · ~400 \|/);
  assert.match(md, /\| security \| \$0\.20 · ~90 \| — \|/);         // no verifier cell
  assert.match(md, /\| \*\*Total\*\* \| \$0\.50 · ~210 \| \$0\.50 · ~400 \|/);
  assert.match(md, /Detection vs verifier: \$0\.50 vs \$0\.50/);
});

test("renderSummary: no attribution table when the log carries none", () => {
  const md = renderSummary({
    agg: { agents: ["x"], sessions: 1, attempt: 1, turns: 1, tokens: 1, weightedTokens: 1, durationMs: 1, costUsd: 0.1 },
    panelAgg: { agents: ["claude-opus-5"], sessions: 1, turns: 8, tokens: 1000, weightedTokens: 1000, costUsd: 1.0, durationMs: 60000 },
    panelStats: {},
    panelAttribution: null, // pre-instrumentation round
    flips: { flips: [] },
    scope: "M",
  });
  assert.doesNotMatch(md, /Token attribution/);
});

test("metric comment round-trip: hidden, self-contained, parses back; junk → null", () => {
  const rec = { kind: "review-fix", models: ["claude-opus-4-8"], turns: 3, tokens: 2, durationMs: 1 };
  const body = serializeRecord(rec);
  assert.match(body, /^<!-- agent-metric /); // a hidden HTML comment (renders invisibly)
  assert.match(body, / -->$/);
  assert.deepEqual(parseMetricComment(body), rec); // round-trips
  // not a metric comment / unparseable → null (so summarize just skips it)
  assert.equal(parseMetricComment("a normal human comment"), null);
  assert.equal(parseMetricComment(""), null);
});

// Guard the --final sweep: it deletes per-session records by matching
// METRIC_PREFIX. That filter must NEVER match the SUMMARY comment we just
// posted, or promotion would delete its own summary. ("agent-metric " has a
// trailing space; "agent-metrics-summary" has an 's' — no substring overlap.)
test("sweep filter: METRIC_PREFIX matches a record but NOT the summary marker", () => {
  const record = serializeRecord({ kind: "review-fix", turns: 3, tokens: 2, durationMs: 1 });
  assert.ok(record.includes(METRIC_PREFIX)); // a per-session record IS swept
  assert.ok(!SUMMARY_MARKER.includes(METRIC_PREFIX)); // the summary is NOT
  // renderSummary output (which carries SUMMARY_MARKER) must not look like a record
  const summary = renderSummary({
    agg: { agents: ["claude-opus-4-8"], sessions: 1, attempt: 1, turns: 1, tokens: 1, durationMs: 1 },
    panelAgg: null, panelStats: null, scope: "S",
  });
  assert.ok(summary.includes(SUMMARY_MARKER));
  assert.ok(!summary.includes(METRIC_PREFIX));
});

test("renderSummary: verifier failures are broken down, and stay silent with no data", () => {
  // The pre-#614 shape, which is what motivated this line: eight sessions dead
  // on the presence turn ceiling, reported in the same words a quota outage
  // would have used. `turn-limit` is what tells those apart.
  const base = {
    agg: { agents: [], sessions: 0, attempt: 1, turns: 0, tokens: 0, weightedTokens: 0, costUsd: 0, durationMs: 0 },
    panelAgg: { agents: ["claude-opus-5"], sessions: 1, turns: 409, tokens: 1, weightedTokens: 1, costUsd: 11.71, durationMs: 60000 },
  };
  const stats = (verifier) => ({
    agreementCounts: { identical: 0, partial: 0, disjoint: 0, single: 1 },
    raised: { critical: 0, major: 2, minor: 0, nit: 0 },
    raisedConfidence: { high: 2, medium: 0, low: 0, unknown: 0 },
    kept: { critical: 0, major: 2, minor: 0, nit: 0 },
    verifier,
  });

  const withFailures = renderSummary({
    ...base,
    panelStats: stats({
      sentToVerifier: 18, refuted: 0, refutedHighConfidence: 0, dropped: 0, errored: 8,
      absenceRaised: 0, absenceRefuted: 0, unresolved: 0,
      failures: { apiError: 1, limit: 6, noOutput: 1, unknown: 0, total: 8 },
    }),
  });
  assert.match(withFailures, /Verifier ERRORED on 8 of 18/);
  // Units are stated, because `errored` above counts findings and this counts
  // sessions — nothing here may be derived from the difference.
  assert.match(withFailures, /- Verifier failures: 8 session\(s\) threw — 1 api-error, 6 turn-limit, 1 no-output/);
  assert.doesNotMatch(withFailures, /folded wording/);

  // MORE sessions than errored findings: one clustered finding whose two folded
  // wordings both threw. An earlier draft rendered `errored - total` here, which
  // is -1. The line must render cleanly, with no remainder and no negative.
  const clustered = renderSummary({
    ...base,
    panelStats: stats({
      sentToVerifier: 5, refuted: 0, refutedHighConfidence: 0, dropped: 0, errored: 1,
      absenceRaised: 0, absenceRefuted: 0, unresolved: 0,
      failures: { apiError: 0, limit: 2, noOutput: 0, unknown: 0, total: 2 },
    }),
  });
  assert.match(clustered, /- Verifier failures: 2 session\(s\) threw — 0 api-error, 2 turn-limit, 0 no-output/);
  assert.doesNotMatch(clustered, /folded wording/);
  // Scoped to the failures line: a repo-wide /-\d/ would match "claude-opus-5".
  const failuresLine = clustered.split("\n").find((l) => l.startsWith("- Verifier failures:"));
  assert.doesNotMatch(failuresLine, /-\s*\d|\(\+/, `no negative or remainder in: ${failuresLine}`);

  // FEWER sessions than errored findings — the other direction, also fine.
  const fewer = renderSummary({
    ...base,
    panelStats: stats({
      sentToVerifier: 5, refuted: 0, refutedHighConfidence: 0, dropped: 0, errored: 3,
      absenceRaised: 0, absenceRefuted: 0, unresolved: 0,
      failures: { apiError: 1, limit: 0, noOutput: 0, unknown: 0, total: 1 },
    }),
  });
  assert.match(fewer, /- Verifier failures: 1 session\(s\) threw — 1 api-error, 0 turn-limit, 0 no-output/);
  assert.doesNotMatch(fewer, /folded wording/);

  // A round recorded before this shipped carries no `failures` at all, and a
  // healthy round has nothing to report. Both must render byte-identically to
  // the same round without the field — an all-zero line would read as data.
  const clean = { sentToVerifier: 4, refuted: 1, refutedHighConfidence: 1, dropped: 1, errored: 0, absenceRaised: 0, absenceRefuted: 0, unresolved: 0 };
  const noField = renderSummary({ ...base, panelStats: stats({ ...clean }) });
  const zeroField = renderSummary({
    ...base,
    panelStats: stats({ ...clean, failures: { apiError: 0, limit: 0, noOutput: 0, unknown: 0, total: 0 } }),
  });
  assert.doesNotMatch(noField, /Verifier failures/);
  assert.equal(zeroField, noField, "an all-zero failures tally must not add a line");
});

test("renderSummary: reused prior verdicts are reported, and silent when there are none", () => {
  const base = {
    agg: { agents: [], sessions: 0, attempt: 1, turns: 0, tokens: 0, weightedTokens: 0, costUsd: 0, durationMs: 0 },
    panelAgg: { agents: ["claude-opus-5"], sessions: 1, turns: 409, tokens: 1, weightedTokens: 1, costUsd: 11.71, durationMs: 60000 },
  };
  const stats = (verifier) => ({
    agreementCounts: { identical: 0, partial: 0, disjoint: 0, single: 1 },
    raised: { critical: 0, major: 2, minor: 0, nit: 0 },
    raisedConfidence: { high: 2, medium: 0, low: 0, unknown: 0 },
    kept: { critical: 0, major: 2, minor: 0, nit: 0 },
    verifier,
  });
  const clean = { sentToVerifier: 10, refuted: 1, refutedHighConfidence: 1, dropped: 1, errored: 0, absenceRaised: 0, absenceRefuted: 0, unresolved: 0 };

  // Without this line a falling `Sent to verifier` is ambiguous between "the
  // reuse is working" and "the lenses raised less" — opposite conclusions.
  const reused = renderSummary({ ...base, panelStats: stats({ ...clean, reusedPriorVerdicts: 4 }) });
  assert.match(reused, /- Prior findings re-found this round: 4 \(verified once, not twice\)/);

  // Round 1 has no prior findings at all, and a round recorded before the reuse
  // existed carries no field. Both must stay byte-identical to today's output —
  // a `: 0` line every first round would be noise, not data.
  const noField = renderSummary({ ...base, panelStats: stats({ ...clean }) });
  const zeroField = renderSummary({ ...base, panelStats: stats({ ...clean, reusedPriorVerdicts: 0 }) });
  assert.doesNotMatch(noField, /re-found this round/);
  assert.equal(zeroField, noField, "zero reuse must not add a line");
});

// --- summary data block: fold-and-sweep round-trip -------------------------

test("summary data block: serialize → parse round-trips the raw ledger", () => {
  const records = [
    { kind: "implement", sessionId: "a", turns: 10, tokens: 100 },
    { kind: "review", sessionId: "b", turns: 20, tokens: 200 },
  ];
  const block = serializeSummaryData(records);
  // Rides inside a summary comment, so it must be an HTML comment (renders blank)
  // and must NOT collide with the per-session METRIC_PREFIX ("agent-metric ").
  assert.ok(block.startsWith("<!-- agent-metrics-data "));
  assert.equal(parseMetricComment(block), null); // not mistaken for a per-session record
  assert.deepEqual(parseSummaryData(`## 🤖 Agent effort\n...\n${block}`), records);
});

test("parseSummaryData: no block / junk → [] (never throws)", () => {
  assert.deepEqual(parseSummaryData(""), []);
  assert.deepEqual(parseSummaryData("## summary, no data block"), []);
  assert.deepEqual(parseSummaryData("<!-- agent-metrics-data {not json} -->"), []);
  assert.deepEqual(parseSummaryData("<!-- agent-metrics-data 7 -->"), []); // non-array
  for (const bad of [null, undefined, 7, {}]) assert.deepEqual(parseSummaryData(bad), []);
});

test("dedupRecords: collapses by sessionId, keeps first (chronological) order", () => {
  const embedded = [{ sessionId: "a", turns: 1 }, { sessionId: "b", turns: 2 }];
  const standalone = [{ sessionId: "b", turns: 2 }, { sessionId: "c", turns: 3 }];
  // embedded (older) first, then this round's standalone; the duplicate 'b' from
  // the standalone side is dropped so a round's spend is never double-counted.
  assert.deepEqual(dedupRecords([...embedded, ...standalone]), [
    { sessionId: "a", turns: 1 },
    { sessionId: "b", turns: 2 },
    { sessionId: "c", turns: 3 },
  ]);
});

test("dedupRecords: records with no sessionId are all kept (no key to collapse)", () => {
  const recs = [{ turns: 1 }, { turns: 2 }, { sessionId: "a" }, { sessionId: "a" }];
  assert.deepEqual(dedupRecords(recs), [{ turns: 1 }, { turns: 2 }, { sessionId: "a" }]);
  for (const bad of [null, undefined, "x", 7, {}]) assert.deepEqual(dedupRecords(bad), []);
});

// --- the standalone fix-agent effort comment --------------------------------

test("FIX_EFFORT_MARKER cannot be swept by the summary machinery", () => {
  // `summarize` deletes every comment containing SUMMARY_MARKER and every one
  // containing METRIC_PREFIX. The whole point of this comment is that it survives
  // that, so the two markers must not be substrings of it in either direction.
  assert.equal(FIX_EFFORT_MARKER.includes(METRIC_PREFIX), false);
  assert.equal(FIX_EFFORT_MARKER.includes(SUMMARY_MARKER), false);
  const body = renderFixEffort({ rec: { turns: 3, tokens: 100, weightedTokens: 50, costUsd: 1, durationMs: 1000, models: ["m"] } });
  assert.equal(body.includes(METRIC_PREFIX), false);
  assert.equal(body.includes(SUMMARY_MARKER), false);
  assert.equal(body.includes(SUMMARY_DATA_MARKER), false);
  assert.ok(body.includes(FIX_EFFORT_MARKER));
});

test("renderFixEffort: a SUCCESSFUL run reports success, through the real classifier", () => {
  // Hand-writing `{ok:true}` here is what hid the bug this test now covers:
  // `classifyResult` requires `structured_output`, which a claude-code-action
  // transcript never has, so every successful fix run was reported as
  // "failed (no-output) — subtype=success. Not retryable; a human should take a
  // look." Build the outcome the way production does.
  const outcome = classifyFixResult({
    type: "result", subtype: "success", is_error: false, num_turns: 42,
    duration_ms: 8 * 60_000, total_cost_usd: 3.5, session_id: "s1",
  });
  assert.deepEqual(outcome, { ok: true });
  const body = renderFixEffort({
    rec: { turns: 42, tokens: 1_200_000, weightedTokens: 300_000, costUsd: 3.5, durationMs: 8 * 60_000, models: ["claude-opus-5"] },
    outcome,
    head: "abcdef1234",
  });
  assert.equal(/Outcome: failed/.test(body), false);
  assert.match(body, /\| Turns \| 42 \|/);
  assert.match(body, /claude-opus-5/);
  assert.match(body, /Outcome: completed/);
  assert.match(body, /abcdef12/);
  assert.match(body, /review panel's own effort is reported separately/);
});

test("renderFixEffort: a 429 session limit is named, and named as NOT retryable", () => {
  // This is the case the comment exists for. The fixer that died on #632/#648
  // reported subtype:"success" with is_error:true at turn 1, which from outside
  // looks exactly like a cheap successful round — the pipeline's only signal was a
  // generic "the branch head is unchanged" page that sends a human looking for a
  // code defect.
  const outcome = classifyFixResult({
    type: "result", subtype: "success", is_error: true, api_error_status: 429,
    terminal_reason: "api_error", num_turns: 1,
    result: "You've hit your session limit · resets 12:10pm (UTC)",
  });
  const body = renderFixEffort({ rec: { turns: 1, tokens: 10, weightedTokens: 10, costUsd: 0, durationMs: 18_000, models: [] }, outcome });
  assert.match(body, /Outcome: failed/);
  assert.match(body, /session limit/);
  // Named as a quota window, not as a defect in the PR and not as a hard ceiling:
  // it clears on its own, so the advice is "wait, then re-run".
  assert.match(body, /not a defect in the PR/);
  assert.match(body, /after that/);
  assert.equal(/hard ceiling/.test(body), false);
  assert.equal(/a human should take a look/.test(body), false);
  // 18 seconds must not render as "1m" — the sub-minute duration IS the diagnostic
  // that the agent never got to work.
  assert.match(body, /\| Duration \| 18s \|/);
});

test("renderFixEffort: a transient API error says to retry", () => {
  const outcome = classifyFixResult({
    type: "result", subtype: "success", is_error: true, api_error_status: 529,
    terminal_reason: "api_error", num_turns: 4, result: "overloaded",
  });
  const body = renderFixEffort({ rec: null, outcome });
  assert.match(body, /Outcome: failed/);
  assert.match(body, /Comment `@claude fix` again to retry/);
});

test("renderFixEffort: a turn ceiling is reported as a ceiling, not a transient", () => {
  const outcome = classifyFixResult({ type: "result", subtype: "error_max_turns", num_turns: 200 });
  const body = renderFixEffort({ rec: null, outcome });
  assert.match(body, /hard ceiling/);
  assert.equal(/again to retry/.test(body), false);
});

test("renderFixEffort: no execution log still produces an honest comment", () => {
  const body = renderFixEffort({ rec: null, outcome: null });
  assert.match(body, /produced no execution log/);
  assert.ok(body.includes(FIX_EFFORT_MARKER));
});

test("classifyFixResult: the failure taxonomy still comes from classifyResult", () => {
  // Success is decided locally; everything else must keep the classification that
  // recognises a 429 hiding behind `subtype:"success"`. Both halves, so a future
  // simplification cannot collapse this into "subtype === 'success'".
  assert.deepEqual(classifyFixResult({ type: "result", subtype: "success", is_error: false }), { ok: true });
  const limit = classifyFixResult({ type: "result", subtype: "success", is_error: true, api_error_status: 429, result: "You've hit your session limit" });
  assert.equal(limit.ok, false);
  assert.equal(limit.kind, "api-error");
  assert.equal(classifyFixResult({ type: "result", subtype: "error_max_turns" }).kind, "limit");
  // Fail direction: anything not cleanly successful reads as a failure.
  assert.equal(classifyFixResult({ type: "result", subtype: "error_during_execution" }).ok, false);
  assert.equal(classifyFixResult({ type: "result", subtype: "success", terminal_reason: "max_turns" }).ok, false);
  assert.equal(classifyFixResult(null), null);
});

// --- the sweep must only delete OUR comments --------------------------------

test("isOwnComment: a comment that merely QUOTES a marker is not ours", () => {
  // The #681 failure, as a unit. The on-demand review's findings comment named
  // `<!-- agent-metrics-summary -->` while explaining the harness's comment
  // surfaces; `summarize` ran five seconds later and deleted the review. Nothing
  // failed — safeDeleteComment is best-effort — so the comment just vanished.
  const bot = { login: "yorkie-agent[bot]", type: "Bot" };
  const review = {
    user: bot,
    body: "<!-- agent-review:abc -->\n## Review\nThe doc enumerates every surface: "
      + `the paged latch, ${SUMMARY_MARKER}, and the rebuttal records.`,
  };
  assert.equal(isOwnComment(review, SUMMARY_MARKER), false);
  // ...and the real summary, whose body OPENS with the marker, still is.
  assert.equal(isOwnComment({ user: bot, body: `${SUMMARY_MARKER}\n## 🤖 Agent effort` }, SUMMARY_MARKER), true);
});

test("isOwnComment: CodeRabbit quoting metrics.mjs is not ours either", () => {
  // It reviews this very file, and quoting code is what it does.
  const body = `Consider documenting why \`${METRIC_PREFIX}\` records are swept.`;
  assert.equal(isOwnComment({ user: { login: "coderabbitai[bot]", type: "Bot" }, body }, METRIC_PREFIX), false);
});

test("isOwnComment: fails toward KEEPING on an unknown author", () => {
  // Deleting the wrong comment destroys work with no record; keeping a stale one
  // costs a duplicate. Only the second is recoverable.
  const body = `${SUMMARY_MARKER}\n## 🤖 Agent effort`;
  assert.equal(isOwnComment({ user: { login: "harrykim8672", type: "User" }, body }, SUMMARY_MARKER), false);
  assert.equal(isOwnComment({ body }, SUMMARY_MARKER), false);
  assert.equal(isOwnComment({ user: null, body }, SUMMARY_MARKER), false);
  assert.equal(isOwnComment(null, SUMMARY_MARKER), false);
  assert.equal(isOwnComment({ user: { type: "Bot" } }, SUMMARY_MARKER), false);
});

test("isOwnComment: both markers are emitted at position 0 by their writers", () => {
  // The position test is only sound because our own renderers put the marker
  // first. Assert that against the real writers rather than trusting it.
  const summary = renderSummary({
    agg: { agents: [], sessions: 1, attempt: 1, turns: 1, tokens: 1, weightedTokens: 1, durationMs: 1, costUsd: 1 },
    panelAgg: null, panelStats: null, panelAttribution: null, flips: [], scope: null,
  });
  assert.ok(summary.startsWith(SUMMARY_MARKER));
  assert.ok(serializeRecord({ kind: "review-fix", turns: 1 }).startsWith(METRIC_PREFIX));
});

test("both sweeps actually route through isOwnComment", async () => {
  // The tests above prove the predicate; they cannot prove `cmdSummarize` uses it,
  // because those loops live in the CLI and need `gh`. Reverting either call site
  // to a bare `includes` left every one of them green. Asserted at the source
  // level, the same way the workflow invariants are.
  const { readFileSync } = await import("node:fs");
  const src = readFileSync(new URL("./metrics.mjs", import.meta.url), "utf8");
  const deletions = src.split("\n").filter((l) => l.includes("safeDeleteComment(c.id)"));
  assert.ok(deletions.length >= 2, "both sweep loops still exist");
  // No sweep may test a marker with a bare `includes` — that is the #681 bug.
  for (const marker of ["SUMMARY_MARKER", "METRIC_PREFIX"]) {
    assert.equal(
      new RegExp(`includes\\(${marker}\\)\\s*\\)?\\s*safeDeleteComment`).test(src),
      false,
      `the ${marker} sweep must not delete on a bare includes()`,
    );
    assert.match(src, new RegExp(`isOwnComment\\(c, ${marker}\\)`), `the ${marker} sweep must use isOwnComment`);
  }
});
