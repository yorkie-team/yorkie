// The out-of-diff gate's wiring through the panel, the check body, the deferred
// record and the round guard. The gate's own decisions are in out-of-diff.test.mjs.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  annotateFindings,
  gatingFindings,
  MAX_CAUSATION_JUDGES_PER_LENS,
  outOfDiffPass,
  routeFinding,
} from "./review-panel.mjs";
import { outOfDiffRecord } from "./out-of-diff.mjs";
import { renderSummaryMd } from "./severity.mjs";
import { buildDeferredCheck, deferredRecord } from "./deferred-findings.mjs";
import { exhaustedFindings, MAX_REBUTTAL_ROUNDS } from "./rebuttal.mjs";

const OUTSIDE = { anchor: "outside-diff" };
const INDEPENDENT = { causation: "independent", confidence: "high", reason: "same on main", groundedIn: ["server/packs/pushpull.go:636"] };
const demoting = () => outOfDiffRecord(OUTSIDE, INDEPENDENT);
const major = (over = {}) => ({ severity: "major", file: "server/packs/pushpull.go", line: 636, summary: "server accepts any actor", claimType: "presence", ...over });

test("routeFinding: a demoting out-of-diff record routes to backlog", () => {
  assert.equal(routeFinding(major(), { outOfDiff: demoting() }), "backlog");
});

test("routeFinding: no carve-out for critical — the defect is on main either way", () => {
  assert.equal(routeFinding(major({ severity: "critical" }), { outOfDiff: demoting() }), "backlog");
});

test("routeFinding: anything short of a grounded independent keeps blocking", () => {
  for (const v of [
    { ...INDEPENDENT, causation: "caused" },
    { ...INDEPENDENT, causation: "unresolved" },
    { ...INDEPENDENT, confidence: "low" },
    { ...INDEPENDENT, groundedIn: [] },
  ]) {
    assert.equal(routeFinding(major(), { outOfDiff: outOfDiffRecord(OUTSIDE, v) }), "blocking");
  }
  assert.equal(routeFinding(major(), { outOfDiff: outOfDiffRecord(OUTSIDE, null, { errored: true }) }), "blocking");
  assert.equal(routeFinding(major(), { outOfDiff: outOfDiffRecord({ anchor: "in-diff" }, INDEPENDENT) }), "blocking");
  assert.equal(routeFinding(major(), {}), "blocking");
});

test("routeFinding: a concrete refutation still outranks the out-of-diff gate", () => {
  const refuted = { verdict: "refuted", confidence: "high", refutationGround: "not-present", groundedIn: ["a.go:1"] };
  assert.equal(routeFinding(major(), { verdict: refuted, outOfDiff: demoting() }), "discarded");
});

test("annotateFindings: stamps the record and the lane; gatingFindings drops it", () => {
  const out = annotateFindings([major(), major({ summary: "other" })], [null, null], null, null, [demoting(), null]);
  assert.equal(out[0].lane, "backlog");
  assert.equal(out[0].outOfDiff.causation, "independent");
  assert.equal(out[1].lane, "blocking");
  assert.equal(out[1].outOfDiff, undefined);
  assert.equal(gatingFindings(out).length, 1);
});

test("annotateFindings: in-diff findings behave exactly as before (no 5th argument)", () => {
  const before = annotateFindings([major()], [null], null, null);
  const after = annotateFindings([major()], [null], null, null, [null]);
  assert.deepEqual(after, before);
});

function spies({ anchors = {}, verdict = INDEPENDENT, throws = false } = {}) {
  const judged = [];
  return {
    judged,
    anchorFor: async (f) => ({ anchor: anchors[f.summary] ?? "outside-diff" }),
    judge: async (f) => {
      judged.push(f.summary);
      if (throws) throw new Error("429");
      return verdict;
    },
  };
}

test("outOfDiffPass: judges only blocking, otherwise-gating, out-of-diff findings", async () => {
  const s = spies({ anchors: { inside: "in-diff", unknown: "unknown" } });
  const findings = [
    major({ summary: "outside" }),
    major({ summary: "inside" }),
    major({ summary: "unknown" }),
    { severity: "minor", file: "x.go", summary: "minor" },
    major({ summary: "relocated" }),
  ];
  const novelties = [null, null, null, null, { origin: "relocated" }];
  const out = await outOfDiffPass(findings, { novelties, anchorFor: s.anchorFor, judge: s.judge, budget: { left: 9 } });
  assert.deepEqual(s.judged, ["outside"]);
  assert.equal(out[0].demotes, true);
  assert.deepEqual(out.slice(1), [null, null, null, null]);
});

test("outOfDiffPass: an errored judge keeps the finding blocking", async () => {
  const s = spies({ throws: true });
  const [rec] = await outOfDiffPass([major()], { anchorFor: s.anchorFor, judge: s.judge });
  assert.equal(rec.causation, "errored");
  assert.equal(routeFinding(major(), { outOfDiff: rec }), "blocking");
});

test("outOfDiffPass: the per-lens cap is shared and never demotes over it", async () => {
  const s = spies();
  const budget = { left: MAX_CAUSATION_JUDGES_PER_LENS };
  const many = Array.from({ length: MAX_CAUSATION_JUDGES_PER_LENS + 2 }, (_, i) => major({ summary: `f${i}` }));
  const out = await outOfDiffPass(many, { anchorFor: s.anchorFor, judge: s.judge, budget });
  assert.equal(s.judged.length, MAX_CAUSATION_JUDGES_PER_LENS);
  assert.equal(out.filter((r) => r.demotes).length, MAX_CAUSATION_JUDGES_PER_LENS);
  assert.equal(out.filter((r) => r.causation === "not-judged").length, 2);
  // The carried pass that shares the budget gets nothing more.
  const more = await outOfDiffPass([major({ summary: "late" })], { anchorFor: s.anchorFor, judge: s.judge, budget });
  assert.equal(more[0].causation, "not-judged");
});

test("outOfDiffPass: a carried finding inherits its fresh twin's judgement, even in a touched file", async () => {
  const s = spies({ anchors: { carried: "unknown" } }); // file-level probe cannot place it
  const twin = demoting();
  const out = await outOfDiffPass([major({ summary: "carried" })], { anchorFor: s.anchorFor, judge: s.judge, inherit: [twin] });
  assert.equal(out[0], twin);
  assert.deepEqual(s.judged, [], "no second session for the same defect");
});

test("outOfDiffPass: gate off (no anchorFor) stamps nothing", async () => {
  assert.deepEqual(await outOfDiffPass([major()], {}), [null]);
});

test("check body: an out-of-diff demotion gets its own section with the judge's citation", () => {
  const [f] = annotateFindings([major()], [null], null, null, [demoting()]);
  const md = renderSummaryMd("Security review", gatingFindings([f]), "", { demoted: [f] });
  assert.match(md, /approved/);
  assert.match(md, /Out of diff — not caused by this change \(1, not blocking\)/);
  assert.match(md, /exists without this change: `server\/packs\/pushpull\.go:636`/);
  assert.doesNotMatch(md, /Relocated code/);
});

test("deferred record: carries the anchor, causation and the demotion flag", () => {
  const [f] = annotateFindings([major()], [null], null, null, [demoting()]);
  const rec = deferredRecord(f, "security");
  assert.equal(rec.lane, "backlog");
  assert.equal(rec.anchorScope, "outside-diff");
  assert.equal(rec.causation, "independent");
  assert.equal(rec.outOfDiffDemoted, true);
  const check = buildDeferredCheck({ lensFindings: [{ lens: "security", findings: [f] }] });
  assert.equal(check.conclusion, "neutral");
  assert.equal(check.total, 1);
});

test("deferred record: a model cannot forge the out-of-diff fields on a minor", () => {
  const rec = deferredRecord({ severity: "minor", file: "a.go", summary: "s", outOfDiff: { anchor: "outside-diff", demotes: true } }, "x");
  assert.equal(rec.anchorScope, undefined);
  assert.equal(rec.outOfDiffDemoted, undefined);
});

test("round guard: a deferred finding is never a standstill, however often it was upheld", () => {
  const upheld = { adjudication: { upheld: MAX_REBUTTAL_ROUNDS } };
  const [deferred] = annotateFindings([major(upheld)], [null], null, null, [demoting()]);
  const inDiff = major({ ...upheld, summary: "really blocking", file: "a.go" });
  const hits = exhaustedFindings([deferred, inDiff]);
  assert.equal(hits.length, 1);
  assert.match(hits[0], /really blocking/);
});
