// The key was moved out of `review-panel.mjs`, where it had already been copied
// once. So the tests that matter are not "does it build a string" — they are
// "does the merge still collapse exactly what the key says is the same finding",
// and "does the agreement metric still answer with the same rule". Both are
// asserted against the REAL `dedupeFindings` and `compareSampleAgreement`, so an
// extraction that drifted in either direction goes red here rather than in a
// number six weeks from now.

import { test } from "node:test";
import assert from "node:assert/strict";
import { findingKey } from "./finding-key.mjs";
import { compareSampleAgreement, dedupeFindings } from "./review-panel.mjs";

test("findingKey is file, then the case- and whitespace-insensitive summary", () => {
  assert.equal(findingKey({ file: "a/b.mjs", summary: "The Retry Loop" }), "a/b.mjs::the retry loop");
  assert.equal(findingKey({ file: "a/b.mjs", summary: "  the retry loop  " }), "a/b.mjs::the retry loop");
  // LEADING AND TRAILING only. Internal whitespace is preserved, and pinning
  // that is the difference between this suite catching a re-introduced copy and
  // not: the obvious way to write the expression from memory is
  // `.replace(/\s+/g, " ").trim()`, which is LOOSER than the merge and passes
  // every padding-only assertion. Caught here instead.
  assert.equal(findingKey({ file: "a/b.mjs", summary: "the  retry   loop" }), "a/b.mjs::the  retry   loop");
  assert.notEqual(findingKey({ summary: "the  retry loop" }), findingKey({ summary: "the retry loop" }));
  // The FILE is not lowercased. Paths are case-sensitive on the platforms this
  // runs on, and folding them would merge two real files on a case-insensitive
  // checkout — a looser key than the merge it has to agree with.
  assert.notEqual(findingKey({ file: "A/B.mjs", summary: "x" }), findingKey({ file: "a/b.mjs", summary: "x" }));
});

test("a missing file or summary keys as empty, not as undefined", () => {
  assert.equal(findingKey({ summary: "x" }), "::x");
  assert.equal(findingKey({ file: "a.mjs" }), "a.mjs::");
  assert.equal(findingKey({}), "::");
  // `String(undefined)` would be the string "undefined" — the `?? ""` is what
  // keeps two summary-less findings on the same file from keying apart.
  assert.equal(findingKey({ file: "a.mjs", summary: null }), "a.mjs::");
});

test("dedupeFindings collapses EXACTLY the findings this key calls identical", () => {
  // The relocation guard. `dedupeFindings` computes its collision key by calling
  // this function, so if the extraction had drifted — a trim added, the file
  // lowercased, similarity swapped in — the two sides would disagree here.
  const findings = [
    { severity: "major", file: "a.mjs", summary: "the retry loop can spin forever" },
    { severity: "critical", file: "a.mjs", summary: "The Retry Loop Can Spin Forever" }, // same key
    { severity: "major", file: "a.mjs", summary: "the retry loop can spin" }, // different key
    { severity: "nit", file: "b.mjs", summary: "the retry loop can spin forever" }, // different file
    { severity: "minor", summary: "no file at all" },
  ];
  const distinct = new Set(findings.map(findingKey));
  assert.equal(dedupeFindings(findings).length, distinct.size, "the merge kept a different number of findings than the key has distinct values");
  // And the collision resolved the way `dedupeFindings` documents — highest
  // severity wins — which is only checkable because the two really did collide.
  assert.equal(dedupeFindings(findings)[0].severity, "critical");
});

test("compareSampleAgreement answers by this key and no other", () => {
  const a = { severity: "major", file: "a.mjs", summary: "the retry loop can spin forever" };
  // Same key: different case and padding only.
  const sameKey = { severity: "nit", file: "a.mjs", summary: "  THE RETRY LOOP CAN SPIN FOREVER " };
  // A restatement of the same defect. It is a DIFFERENT key on purpose — this is
  // identity, not similarity, and `clusterFindings` is the mechanism for the
  // other question. If a future edit loosened the key, this assertion flips.
  const restated = { severity: "major", file: "a.mjs", summary: "the retry loop never terminates" };
  // Padding differs → same key. Internal spacing differs → DIFFERENT key, and
  // this pair is what fails if a looser copy of the expression comes back.
  const respaced = { severity: "major", file: "a.mjs", summary: "the retry loop can  spin forever" };
  assert.equal(compareSampleAgreement([[a], [sameKey]]), "identical");
  assert.equal(compareSampleAgreement([[a], [respaced]]), "disjoint");
  assert.equal(compareSampleAgreement([[a], [restated]]), "disjoint");
  assert.equal(compareSampleAgreement([[a], [a, restated]]), "partial");
  // Both samples finding nothing is agreement, not a missing answer.
  assert.equal(compareSampleAgreement([[], []]), "identical");
});

test("compareSampleAgreement still keys a malformed sample consistently", () => {
  // It maps through `coerceFindings` first, which rewrites junk into a real
  // object with a placeholder summary. Two junk entries therefore share a key —
  // which is what stops a lens that returned garbage twice from reading as two
  // samples that disagreed.
  assert.equal(compareSampleAgreement([[null], [42]]), "identical");
});
