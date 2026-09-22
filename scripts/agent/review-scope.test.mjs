import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gitFacts, reviewingLensIds, decideScope } from "./review-scope.mjs";
import { serializeReviewState } from "./review-state.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const A = "a".repeat(40);
const B = "b".repeat(40);
const quiet = () => {};

// --- gitFacts ---------------------------------------------------------------

// A stubbed runner keyed by the first two argv words, so each branch is exercised
// without building repositories. `ok`/`status` are kept distinct because git uses
// the exit code as an ANSWER for `--is-ancestor` and as an ERROR elsewhere.
const runner = (over = {}) => async (args) => {
  const key = args.slice(0, 2).join(" ");
  if (key in over) return over[key];
  if (key === "cat-file -e") return { ok: true, status: 0, stdout: "" };
  if (key === "merge-base --is-ancestor") return { ok: true, status: 0, stdout: "" };
  if (key === "rev-list --merges") return { ok: true, status: 0, stdout: "" };
  if (key === "diff --numstat") return { ok: true, status: 0, stdout: "10\t5\tsrc/a.ts\n1\t0\tsrc/b.ts\n" };
  throw new Error(`unstubbed git ${key}`);
};

test("gitFacts: the happy path sums added+deleted across the numstat", async () => {
  assert.deepEqual(await gitFacts({ since: A, head: B, run: runner() }),
    { isAncestor: true, hasMergeInRange: false, deltaLines: 16 });
});

test("gitFacts: exit 1 from --is-ancestor is an ANSWER; any other failure is not", async () => {
  // 1 = "no", i.e. force-push / rebase / amend. This must be reported as a
  // confident false so resolveReviewMode can say force-push-or-rewrite.
  const forced = await gitFacts({ since: A, head: B, run: runner({ "merge-base --is-ancestor": { ok: false, status: 1, stdout: "" } }) });
  assert.equal(forced.isAncestor, false);
  // Any other status, or none (spawn failure / timeout), is a BROKEN LOOKUP.
  // Recording it as a confident "no" would name the wrong reason; null → unavailable.
  for (const status of [128, 2, null]) {
    const got = await gitFacts({ since: A, head: B, run: runner({ "merge-base --is-ancestor": { ok: false, status, stdout: "" } }) });
    assert.equal(got.isAncestor, null, `status ${status} must not be a confident answer`);
  }
});

test("gitFacts: an unresolvable pointer makes every fact unknown", async () => {
  // A pointer that no longer names a commit (rewritten or gc'd history) makes
  // every measurement below it meaningless, so none is attempted.
  const got = await gitFacts({ since: A, head: B, run: runner({ "cat-file -e": { ok: false, status: 1, stdout: "" } }) });
  assert.deepEqual(got, { isAncestor: null, hasMergeInRange: null, deltaLines: null });
  // Junk shas never reach git at all.
  for (const bad of [undefined, null, "", "abc", 7, A.slice(0, 39)]) {
    assert.deepEqual(await gitFacts({ since: bad, head: B, run: runner() }),
      { isAncestor: null, hasMergeInRange: null, deltaLines: null }, `since=${JSON.stringify(bad)}`);
    assert.deepEqual(await gitFacts({ since: A, head: bad, run: runner() }),
      { isAncestor: null, hasMergeInRange: null, deltaLines: null }, `head=${JSON.stringify(bad)}`);
  }
});

test("gitFacts: a merge in range is reported; a failed rev-list is not 'no merges'", async () => {
  const merged = await gitFacts({ since: A, head: B, run: runner({ "rev-list --merges": { ok: true, status: 0, stdout: `${B}\n` } }) });
  assert.equal(merged.hasMergeInRange, true);
  const broken = await gitFacts({ since: A, head: B, run: runner({ "rev-list --merges": { ok: false, status: 128, stdout: "" } }) });
  assert.equal(broken.hasMergeInRange, null, "a broken lookup must not read as 'no merges'");
});

test("gitFacts: a binary row makes deltaLines unknown, never zero", async () => {
  // numstat prints `-\t-` for binary files. Counting that as 0 lines UNDER-counts
  // the delta, and the delta size is an upper bound that PERMITS narrowing — so
  // under-counting is the fail-open direction. null → full.
  const got = await gitFacts({ since: A, head: B, run: runner({ "diff --numstat": { ok: true, status: 0, stdout: "-\t-\timg.png\n" } }) });
  assert.equal(got.deltaLines, null);
  // An empty numstat is a real zero, not a failure.
  const empty = await gitFacts({ since: A, head: B, run: runner({ "diff --numstat": { ok: true, status: 0, stdout: "\n" } }) });
  assert.equal(empty.deltaLines, 0);
  const failed = await gitFacts({ since: A, head: B, run: runner({ "diff --numstat": { ok: false, status: 128, stdout: "" } }) });
  assert.equal(failed.deltaLines, null);
});

// --- reviewingLensIds -------------------------------------------------------

test("reviewingLensIds: only lenses that will actually review, from the REAL manifest", () => {
  const manifest = JSON.parse(readFileSync(path.join(HERE, "lenses", "lenses.json"), "utf8"));
  // Requiring a pointer from a lens that is not running would report
  // `lens-state-gap` every round, so nothing would ever narrow and nothing would
  // say why. So this must track `appliesWhen` exactly — asserted here on the two
  // concrete directions rather than by re-deriving it, which would be tautological.
  const code = reviewingLensIds(manifest, ["pkg/document/crdt/tree.go"]);
  const docs = reviewingLensIds(manifest, ["docs/tasks/active/20260912-x-todo.md"]);

  // Wildcard lenses run on everything, so both sets are non-empty.
  for (const set of [code, docs]) {
    for (const id of ["correctness", "security"]) {
      assert.ok(set.includes(id), `${id} is wildcard-scoped and must always be reviewing`);
    }
  }
  // Each set EXCLUDES the other's scoped lenses — the property that makes the
  // pointer requirement match reality.
  for (const id of ["test-adequacy", "blast-radius"]) {
    assert.ok(code.includes(id), `${id} must review a code change`);
    assert.ok(!docs.includes(id), `${id} must not be required to have reviewed a docs-only change`);
  }
  assert.ok(docs.includes("docs"), "the docs lens must review a markdown change");
  assert.ok(!code.includes("docs"), "the docs lens must not be required for a .go-only change");
  // Both are strict subsets, so neither direction silently degenerates to "all".
  for (const [name, set] of [["code", code], ["docs", docs]]) {
    assert.ok(set.length < manifest.length, `${name} should be a strict subset, got ${set.join(",")}`);
  }
  // Junk in, empty out — never a throw.
  for (const bad of [null, undefined, "x", 7, {}]) assert.deepEqual(reviewingLensIds(bad, ["a.ts"]), []);
  assert.deepEqual(reviewingLensIds([{ id: "" }, {}, null], ["a.ts"]), []);
});

// --- decideScope: the two-phase composition ---------------------------------

const MANIFEST = [
  { id: "correctness", gating: "blocking", appliesWhen: ["**"] },
  { id: "security", gating: "blocking", appliesWhen: ["**"] },
];
const LENS_NAMES = ["agent-review-correctness", "agent-review-security"];
const CODE = ["packages/sheets/src/a.ts"];

const lensRun = (name, id, externalId) => ({
  name, id, status: "completed", app: { slug: "github-actions" },
  completed_at: "2026-07-20T10:00:00Z",
  external_id: externalId,
});

// A PR whose last round stamped `reviewed: A` for both lenses.
const stampedAt = (sha) =>
  LENS_NAMES.map((n, i) => lensRun(n, 10 + i, serializeReviewState({ reviewed: sha, mode: "full" })));

// Find the PATH argument rather than assuming a position: `prCommitsWithCheckRuns`
// puts `--paginate` before the path for the commits call and after it for
// check-runs. An earlier version of this stub indexed argv[1] for both, which made
// every call throw, every commit list come back empty, and four tests fail —
// including one that then "passed" for the wrong reason.
const apiFor = (runsBySha) => (args) => {
  const p = args.find((a) => typeof a === "string" && a.startsWith("repos/"));
  assert.ok(p, `no path in gh args: ${args.join(" ")}`);
  if (p.includes("/commits?")) return Object.keys(runsBySha).map((sha) => ({ sha }));
  const sha = p.split("/commits/")[1].split("/")[0];
  return [{ check_runs: runsBySha[sha] ?? [] }];
};

test("decideScope: narrows when every lens agrees and git is clean", async () => {
  const got = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE,
    api: apiFor({ [A]: stampedAt(A) }), run: runner(), log: quiet,
  });
  assert.equal(got.mode, "incremental");
  assert.equal(got.sinceSha, A);
  assert.equal(got.reason, "ok");
  assert.equal(got.rounds, 1);
});

// THE hazard this shape exists to prevent. `resolveReviewMode` is handed its git
// facts and cannot tell which range they describe, so the caller must measure the
// range it is about to narrow to. Assert it by capturing the argv git receives.
test("decideScope: git facts are measured over the AGREED range, not any other", async () => {
  const seen = [];
  const spy = async (args) => {
    seen.push(args.join(" "));
    return runner()(args);
  };
  const got = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE,
    api: apiFor({ [A]: stampedAt(A) }), run: spy, log: quiet,
  });
  assert.equal(got.sinceSha, A);
  assert.ok(seen.includes(`merge-base --is-ancestor ${A} ${B}`), `measured the wrong range: ${seen.join(" | ")}`);
  assert.ok(seen.includes(`rev-list --merges ${A}..${B}`));
  assert.ok(seen.includes(`diff --numstat ${A} ${B}`));
  assert.ok(seen.includes(`cat-file -e ${A}^{commit}`));
});

test("decideScope: git is not touched at all when there is no range to measure", async () => {
  // Phase 1 short-circuits. Measuring first would either measure a guessed range
  // or waste four git calls on every first round.
  const spy = async () => { throw new Error("git must not run without an agreed pointer"); };
  const got = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE,
    api: apiFor({ [A]: [lensRun(LENS_NAMES[0], 10, undefined), lensRun(LENS_NAMES[1], 11, undefined)] }),
    run: spy, log: quiet,
  });
  assert.equal(got.mode, "full");
  assert.equal(got.reason, "no-prior-state");
});

test("decideScope: every failure resolves to full, and names itself", async () => {
  const base = { pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE, run: runner(), log: quiet };
  const cases = [
    // one lens stamped, one not: a coverage hole, not a narrowing opportunity
    ["lens-state-gap", { api: apiFor({ [A]: [stampedAt(A)[0], lensRun(LENS_NAMES[1], 11, undefined)] }) }],
    // lenses disagree about what they last reviewed
    ["lens-state-divergence", {
      api: apiFor({ [A]: [stampedAt(A)[0], lensRun(LENS_NAMES[1], 11, serializeReviewState({ reviewed: "c".repeat(40), mode: "full" }))] }),
    }],
    // a re-run on the already-reviewed sha
    ["no-new-commits", { head: A, api: apiFor({ [A]: stampedAt(A) }) }],
    // a pointer that no longer resolves
    ["git-facts-unavailable", {
      api: apiFor({ [A]: stampedAt(A) }),
      run: runner({ "cat-file -e": { ok: false, status: 1, stdout: "" } }),
    }],
    ["force-push-or-rewrite", {
      api: apiFor({ [A]: stampedAt(A) }),
      run: runner({ "merge-base --is-ancestor": { ok: false, status: 1, stdout: "" } }),
    }],
    ["merge-in-range", {
      api: apiFor({ [A]: stampedAt(A) }),
      run: runner({ "rev-list --merges": { ok: true, status: 0, stdout: `${B}\n` } }),
    }],
    ["delta-too-large", {
      api: apiFor({ [A]: stampedAt(A) }),
      run: runner({ "diff --numstat": { ok: true, status: 0, stdout: "401\t0\tbig.ts\n" } }),
    }],
    // garbage from the API, and an unreadable manifest (main() passes null)
    ["no-prior-state", { api: () => { throw new Error("gh: 403"); } }],
    ["invalid-input", { manifest: null, api: apiFor({ [A]: stampedAt(A) }) }],
    ["invalid-input", { pr: "not-a-number", api: apiFor({ [A]: stampedAt(A) }) }],
    ["invalid-input", { head: "nope", api: apiFor({ [A]: stampedAt(A) }) }],
  ];
  for (const [reason, over] of cases) {
    const got = await decideScope({ ...base, ...over });
    assert.equal(got.mode, "full", `${reason}: must be full`);
    assert.equal(got.reason, reason, `wrong reason for ${JSON.stringify(Object.keys(over))}`);
    assert.equal(got.sinceSha, "", "full mode must not hand back a since-sha");
  }
  // Junk options object: still an answer, still full.
  for (const bad of [undefined, null, 7, "x", []]) {
    const got = await decideScope(bad);
    assert.equal(got.mode, "full");
    assert.equal(got.reason, "invalid-input");
  }
});

test("decideScope: the periodic rebaseline fires on the round count from the API", async () => {
  // Three commits each carrying lens runs = three rounds; fullEvery 3 forces full.
  const shas = [A, B, "c".repeat(40)];
  const api = apiFor(Object.fromEntries(shas.map((s) => [s, stampedAt(A)])));
  const got = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE, api, run: runner(), log: quiet,
  });
  assert.equal(got.rounds, 3);
  assert.equal(got.reason, "periodic-rebaseline");
  // Same PR with the cap raised narrows, which is what proves the round count —
  // not the pointer state — is what forced it above.
  const raised = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE, api, run: runner(), log: quiet, fullEvery: 4,
  });
  assert.equal(raised.mode, "incremental");
});
