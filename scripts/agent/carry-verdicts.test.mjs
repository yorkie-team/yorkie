import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { carryVerdicts, writeCarried } from "./carry-verdicts.mjs";
import { parseReviewState, serializeReviewState } from "./review-state.mjs";

const A = "a".repeat(40);
const B = "b".repeat(40);
const C = "c".repeat(40);
const FP = "f".repeat(40);
const quiet = () => {};

const MANIFEST = [
  { id: "correctness", title: "Correctness", gating: "blocking", appliesWhen: ["**"] },
  { id: "security", title: "Security", gating: "blocking", appliesWhen: ["**"] },
  { id: "docs", title: "Docs", gating: "advisory", appliesWhen: ["docs/**"] },
];
const CODE = ["packages/sdk/src/a.ts"];

const run = (lens, id, conclusion, text = "[]", over = {}) => ({
  name: `agent-review-${lens}`, id, status: "completed", conclusion,
  app: { slug: "github-actions" }, completed_at: "2026-09-30T12:09:00Z",
  external_id: serializeReviewState({ reviewed: A, base: C, mode: "full", fp: FP }),
  output: { title: `${lens}: ${conclusion}`, summary: `## ${lens}\nverdict body`, text },
  ...over,
});

// `commits/{sha}/check-runs` returns pages of `{ check_runs }`; `check-runs/{id}`
// returns the full run. Both shapes are what gh-checks.mjs consumes.
const apiFor = (runs) => (args) => {
  const p = args.find((a) => typeof a === "string" && a.startsWith("repos/"));
  if (p.includes("/commits/")) return [{ check_runs: runs }];
  const id = Number(p.split("/check-runs/")[1]);
  return runs.find((r) => r.id === id);
};

test("carryVerdicts: an approved source carries to the new head, stamped as a carry", () => {
  const got = carryVerdicts({
    mode: "carry", source: A, head: B, base: C, fingerprint: FP, carry: 1,
    manifest: MANIFEST, changedFiles: CODE,
    api: apiFor([run("correctness", 10, "success"), run("security", 11, "success")]), log: quiet,
  });
  assert.equal(got.ok, true);
  const [correctness, security, docs] = got.panel;
  assert.equal(correctness.conclusion, "success");
  assert.equal(correctness.valid, true);
  assert.equal(correctness.applicable, true);
  // The pointer moves to the NEW head, records that it was carried and how many
  // times in a row, and keeps the fingerprint the next round compares against.
  assert.deepEqual(parseReviewState(correctness.reviewState), {
    v: 1, reviewed: B, base: C, since: "", mode: "carry", fp: FP, carry: 1,
  });
  assert.equal(security.conclusion, "success");
  // An inapplicable lens is skipped, as the panel would have skipped it.
  assert.equal(docs.applicable, false);
  assert.equal(docs.conclusion, "skipped");
  assert.equal(docs.reviewState, undefined);
  // The summary says where the verdict came from, ahead of the original body.
  assert.match(got.files.correctness.summary, /^> Carried from `aaaaaaaaaaaa`/);
  assert.match(got.files.correctness.summary, /verdict body/);
});

test("carryVerdicts: a reuse keeps the findings text and the original pointer", () => {
  const findings = JSON.stringify([{ severity: "major", file: "a.ts", summary: "s", evidence: "e" }]);
  const got = carryVerdicts({
    mode: "reuse", source: A, head: A, base: C, fingerprint: FP,
    manifest: MANIFEST, changedFiles: CODE,
    api: apiFor([run("correctness", 10, "failure", findings), run("security", 11, "success")]), log: quiet,
  });
  assert.equal(got.ok, true);
  assert.equal(got.panel[0].conclusion, "failure");
  // The fixer's work list is read from output.text, so it must survive verbatim.
  assert.equal(got.files.correctness.text, findings);
  // Same head: the pointer is the one already there, untouched.
  assert.equal(parseReviewState(got.panel[0].reviewState).reviewed, A);
  assert.equal(parseReviewState(got.panel[0].reviewState).mode, "full");
  assert.match(got.files.correctness.summary, /^> Reused the verdict already recorded on `aaaaaaaaaaaa`/);
});

test("carryVerdicts: anything it cannot stand behind returns ok=false, so the panel runs instead", () => {
  const ok = [run("correctness", 10, "success"), run("security", 11, "success")];
  const base = { mode: "carry", source: A, head: B, base: C, fingerprint: FP, carry: 1, manifest: MANIFEST, changedFiles: CODE, log: quiet };
  const cases = [
    ["a lens with no run on the source", { api: apiFor([ok[0]]) }],
    ["a lens whose verdict is not a verdict", { api: apiFor([ok[0], run("security", 11, "cancelled")]) }],
    ["a carry of a blocking verdict", { api: apiFor([ok[0], run("security", 11, "failure")]) }],
    ["unparseable findings text", { mode: "reuse", head: A, api: apiFor([ok[0], run("security", 11, "failure", "{nope")]) }],
    ["a run from another app", { api: apiFor([ok[0], run("security", 11, "success", "[]", { app: { slug: "other" } })]) }],
    ["the API failing", { api: () => { throw new Error("gh: 502"); } }],
    ["a bad mode", { mode: "sideways", api: apiFor(ok) }],
    ["a bad head", { head: "nope", api: apiFor(ok) }],
    ["a reuse that is not the same head", { mode: "reuse", api: apiFor(ok) }],
  ];
  for (const [what, over] of cases) {
    const got = carryVerdicts({ ...base, ...over });
    assert.equal(got.ok, false, `must refuse ${what}`);
    assert.ok(got.reason, `must say why for ${what}`);
  }
});

test("writeCarried: lays the files out exactly where the post step reads them", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "carry-"));
  const got = carryVerdicts({
    mode: "carry", source: A, head: B, base: C, fingerprint: FP, carry: 1,
    manifest: MANIFEST, changedFiles: CODE,
    api: apiFor([run("correctness", 10, "success"), run("security", 11, "success")]), log: quiet,
  });
  writeCarried(dir, got);
  const panel = JSON.parse(readFileSync(path.join(dir, "panel.json"), "utf8"));
  assert.deepEqual(panel.map((p) => p.id), ["correctness", "security", "docs"]);
  assert.match(readFileSync(path.join(dir, "correctness", "summary.md"), "utf8"), /Carried from/);
  assert.equal(readFileSync(path.join(dir, "correctness", "carried-text.json"), "utf8"), "[]");
  // Nothing is written for a skipped lens's findings: the post step's own
  // fallback (`[]`) is the right value there.
  assert.equal(existsSync(path.join(dir, "docs", "carried-text.json")), false);
});

test("carryVerdicts: a blocking verdict whose findings could not be read is refused, not reused empty", () => {
  // review: the full-output fetch failed, the list copy carried no output.text,
  // it became "[]", and a reused failure sent the fixer an empty work list.
  const noText = run("correctness", 10, "failure", "[]", { output: { title: "t", summary: "s" } });
  const got = carryVerdicts({
    mode: "reuse", source: A, head: A, base: C, fingerprint: FP, manifest: MANIFEST, changedFiles: CODE,
    api: apiFor([noText, run("security", 11, "success")]), log: quiet,
  });
  assert.equal(got.ok, false);
  assert.match(got.reason, /findings/);
});
