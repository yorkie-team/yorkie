import "./fixtures/use-fixture-config.mjs";
import test from "node:test";
import assert from "node:assert/strict";

import { infraSummary, INFRA_SUMMARY_PREFIX } from "./review-panel.mjs";
import { INFRA_SENTINEL, tagPriorFindings } from "./prior-findings.mjs";
import { publicInfraReason } from "./redact.mjs";

// The infra summary string is a WIRE FORMAT with two independent parsers, neither
// of which imports the producer. Breaking it does not fail loudly: it carries a
// stale "the review could not run" record forward as a CODE FINDING, so the next
// round tries to re-verify it, the verifier (biased to keep) cannot refute it on
// grounded evidence, and the loop sticks — one round later, on whatever pull
// request next hits a quota error. That is a long way from the edit that caused it.
//
// These tests are the thing that makes the string safe to refactor.

const REASONS = [
  publicInfraReason({ status: 401, detail: "authentication_error" }),
  publicInfraReason({ status: 429, detail: "You've hit your session limit" }),
  publicInfraReason({ status: null, detail: "Header 'Authorization' has invalid value: sk-ant-oat01-AbCd1234" }),
  publicInfraReason({ status: 529, detail: "overloaded_error" }),
];

test("infraSummary: consumer 1 — prior-findings.mjs INFRA_SENTINEL (startsWith)", () => {
  for (const reason of REASONS) {
    const summary = infraSummary({ reason });
    assert.ok(summary.startsWith(INFRA_SENTINEL), `sentinel broken by: ${summary}`);
  }
});

test("infraSummary: consumer 2 — rounds.mjs INFRA_SUMMARY (anchored regex)", () => {
  // rounds.mjs keeps its matcher module-private, so the pattern is restated here
  // rather than imported. If that regex is edited, this literal must be edited to
  // match — which is the point: the copy makes the coupling visible instead of
  // letting the producer drift away from a pattern nobody remembered existed.
  const INFRA_SUMMARY = /^\s*(Review could not run|Reviewer did not produce a valid verdict)/i;
  for (const reason of REASONS) {
    assert.match(infraSummary({ reason }), INFRA_SUMMARY);
  }
});

test("infraSummary: the prefix is exactly the sentinel both parsers agree on", () => {
  assert.equal(INFRA_SUMMARY_PREFIX, INFRA_SENTINEL);
});

test("prior-findings: a synthesised infra record is dropped, not carried forward", () => {
  // End to end through the real consumer: the record review-panel.mjs writes must
  // not come back as a finding to re-check.
  const summary = infraSummary({ reason: REASONS[0] });
  const carried = tagPriorFindings({
    "agent-review-bugs": { output: { text: JSON.stringify([{ severity: "major", summary, infra: true }]) } },
  });
  assert.deepEqual(carried, [], "an infra record was carried forward as a finding");
});

test("prior-findings: the legacy prefix fallback still recognises the new string", () => {
  // Records persisted before `infra: true` existed are matched on the prefix alone,
  // and only when they carry no `file` (the synthetic record's shape).
  const summary = infraSummary({ reason: REASONS[1] });
  const carried = tagPriorFindings({
    "agent-review-bugs": { output: { text: JSON.stringify([{ severity: "major", summary }]) } },
  });
  assert.deepEqual(carried, [], "the legacy fallback stopped matching");
});

test("infraSummary: publishes the code, and never upstream prose", () => {
  const leaky = "Header 'Authorization' has invalid value: sk-ant-oat01-AbCdEf1234567890 XyZ987654321";
  const summary = infraSummary({ reason: publicInfraReason({ status: null, detail: leaky }) });
  for (const fragment of ["sk-ant-oat01-AbCdEf1234567890", "XyZ987654321"]) {
    assert.ok(!summary.includes(fragment), `leaked ${fragment}: ${summary}`);
  }
  assert.match(summary, /\[AUTH_MALFORMED_CREDENTIAL\]/);
});
