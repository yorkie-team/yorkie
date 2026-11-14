import "./fixtures/use-fixture-config.mjs";
import test from "node:test";
import assert from "node:assert/strict";
import { renderSessionSummary } from "./session-job-summary.mjs";

test("no execution log renders an explanation, not an empty table", () => {
  const md = renderSessionSummary({ rec: null, title: "Implement agent session" });
  assert.match(md, /### Implement agent session/);
  assert.match(md, /No execution log was produced/);
  assert.ok(!md.includes("| Turns |"));
});

test("sub-minute duration renders in seconds (a dead-in-18s agent must not read as 1m)", () => {
  const md = renderSessionSummary({
    rec: { turns: 2, tokens: 1200, weightedTokens: 1300, costUsd: 0.5, durationMs: 18_000, models: ["claude-opus-5"] },
    outcome: { ok: false, kind: "api-error", status: 429, detail: "rate limited", retryable: true },
  });
  assert.match(md, /\| 18s \|/);
  assert.match(md, /\*\*Outcome: failed \(api-error 429\)\*\* — rate limited/);
});

test("clean run renders completed with minutes", () => {
  const md = renderSessionSummary({
    rec: { turns: 78, tokens: 1_900_000, weightedTokens: 2_000_000, costUsd: 14.2, durationMs: 42 * 60_000, models: ["claude-opus-5"] },
    outcome: { ok: true },
  });
  assert.match(md, /\| 42m \|/);
  assert.match(md, /\*\*Outcome: completed\.\*\*/);
});
