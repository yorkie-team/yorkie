import test from "node:test";
import assert from "node:assert/strict";
import { renderPanelJobSummary, severityCell } from "./panel-job-summary.mjs";

test("severity cell drops zeroes and keeps blocking-first order", () => {
  assert.equal(severityCell({ critical: 1, major: 0, minor: 2, nit: 0 }), "1 crit, 2 min");
  assert.equal(severityCell({}), "0");
  assert.equal(severityCell(null), "0");
});

test("a missing panel.json renders the fail-closed explanation, not a table", () => {
  const md = renderPanelJobSummary({ panel: null, pr: "712" });
  assert.match(md, /no readable `panel\.json`/);
  assert.match(md, /fails closed/);
  assert.ok(!md.includes("| Lens |"));
});

test("renders one row per panel entry, joined with its stats", () => {
  const md = renderPanelJobSummary({
    pr: "712",
    scope: { mode: "incremental", reason: "ok", rounds: "2" },
    panel: [
      { id: "correctness", conclusion: "failure", applicable: true, valid: true },
      { id: "security", conclusion: "success", applicable: true, valid: true },
      { id: "docs", conclusion: "skipped", applicable: false, valid: true },
    ],
    stats: [
      {
        id: "correctness",
        raised: { critical: 0, major: 2, minor: 1, nit: 0 },
        kept: { critical: 0, major: 1, minor: 0, nit: 0 },
        verifier: { sentToVerifier: 2, refuted: 1, errored: 0 },
      },
    ],
    timing: { wallMs: 16 * 60_000 },
    totalCostUsd: 9.87,
    sdkCalls: 14,
  });
  assert.match(md, /PR #712/);
  assert.match(md, /scope: \*\*incremental\*\* \(`ok`, round 2\)/);
  assert.match(md, /\| correctness \| ❌ failure \| 2 maj, 1 min \| 2 \| 1 \| 1 maj \| 0 \|/);
  assert.match(md, /\| security \| ✅ success \| — \| — \| — \| — \| — \|/);
  assert.match(md, /\| docs \| ➖ not applicable \|/);
  assert.match(md, /wall time 16m/);
  assert.match(md, /\$9\.87/);
  assert.match(md, /14 SDK call\(s\)/);
});

test("verifier errors are flagged, infra failures labelled", () => {
  const md = renderPanelJobSummary({
    panel: [{ id: "security", conclusion: "failure", applicable: true, valid: false, infraError: "429" }],
    stats: [{ id: "security", raised: {}, kept: {}, verifier: { sentToVerifier: 1, refuted: 0, errored: 1 } }],
  });
  assert.match(md, /❌ failure \(infra\)/);
  assert.match(md, /⚠️ 1/);
});
