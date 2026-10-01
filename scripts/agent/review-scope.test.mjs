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
//
// `commits` overrides the PR's commit list (default: every sha with runs), and
// `forcePushedFrom` lists the heads force-pushes replaced, oldest first, as the
// GraphQL timeline returns them.
const apiFor = (runsBySha, { commits, forcePushedFrom = [] } = {}) => (args) => {
  if (args[1] === "graphql") {
    return { data: { repository: { pullRequest: { timelineItems: {
      nodes: forcePushedFrom.map((oid) => ({ beforeCommit: oid ? { oid } : null })),
    } } } } };
  }
  const p = args.find((a) => typeof a === "string" && a.startsWith("repos/"));
  assert.ok(p, `no path in gh args: ${args.join(" ")}`);
  if (p.includes("/commits?")) return (commits ?? Object.keys(runsBySha)).map((sha) => ({ sha }));
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

// --- carry, reuse and `@claude rerun review` ----------------------------------

const FP = "f".repeat(40);
const verdictRun = (name, id, sha, conclusion, extra = {}) => ({
  ...lensRun(name, id, serializeReviewState({ reviewed: sha, mode: "full", fp: FP, ...extra })),
  conclusion,
});
const approvedAt = (sha, extra) => LENS_NAMES.map((n, i) => verdictRun(n, 10 + i, sha, "success", extra));
const blockedAt = (sha) => LENS_NAMES.map((n, i) => verdictRun(n, 10 + i, sha, i === 0 ? "failure" : "success"));
const human = (body, at, login = "maintainer") => ({
  body, created_at: at, user: { login, type: "User" }, author_association: "MEMBER",
});
const trustAll = () => true;
const merged = runner({ "rev-list --merges": { ok: true, status: 0, stdout: `${B}\n` } });

test("decideScope: a diff-neutral merge of an approved head carries, and hands back the source (#1426)", async () => {
  const got = await decideScope({
    pr: "1426", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
    api: apiFor({ [A]: approvedAt(A) }), run: merged, comments: [], trusts: trustAll, log: quiet,
  });
  assert.equal(got.mode, "carry");
  assert.equal(got.sourceSha, A);
  assert.equal(got.carry, 1);
  assert.equal(got.reason, "pr-diff-unchanged");
  // A different fingerprint is a changed diff: the merge-in-range review stands.
  const changed = await decideScope({
    pr: "1426", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: "e".repeat(40),
    api: apiFor({ [A]: approvedAt(A) }), run: merged, comments: [], trusts: trustAll, log: quiet,
  });
  assert.equal(changed.reason, "merge-in-range");
});

test("decideScope: a rerun on the same head reuses its verdicts; `rerun review` forces a review", async () => {
  const base = {
    pr: "1426", head: A, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
    api: apiFor({ [A]: blockedAt(A) }), run: runner(), trusts: trustAll, log: quiet,
  };
  const reuse = await decideScope({ ...base, comments: [human("@claude rerun", "2026-07-21T00:00:00Z")] });
  assert.equal(reuse.mode, "reuse");
  assert.equal(reuse.sourceSha, A);
  // The rerun asked for a review, after the verdict it would otherwise reuse.
  const forced = await decideScope({ ...base, comments: [human("@claude rerun review", "2026-07-21T00:00:00Z")] });
  assert.equal(forced.mode, "full");
  assert.equal(forced.reason, "review-requested");
  // A `rerun review` OLDER than the verdict was already answered by it.
  const answered = await decideScope({ ...base, comments: [human("@claude rerun review", "2026-07-19T00:00:00Z")] });
  assert.equal(answered.mode, "reuse");
  // Only the LATEST rerun speaks: a plain rerun after a `rerun review` reuses.
  const latest = await decideScope({
    ...base,
    comments: [human("@claude rerun review", "2026-07-21T00:00:00Z"), human("@claude rerun", "2026-07-22T00:00:00Z")],
  });
  assert.equal(latest.mode, "reuse");
  // An untrusted commenter cannot force (or block) anything.
  const stranger = await decideScope({
    ...base, trusts: (login) => login === "maintainer",
    comments: [human("@claude rerun review", "2026-07-21T00:00:00Z", "drive-by")],
  });
  assert.equal(stranger.mode, "reuse");
  // A bot cannot either, whatever it writes.
  const bot = await decideScope({
    ...base, comments: [{ ...human("@claude rerun review", "2026-07-21T00:00:00Z", "x[bot]"), user: { login: "x[bot]", type: "Bot" } }],
  });
  assert.equal(bot.mode, "reuse");
});

test("decideScope: comments that cannot be read force a review — never a reuse on a guess", async () => {
  // A request we could not read may have been `rerun review`. Reusing on that
  // doubt would skip the review a human asked for; reviewing costs only tokens.
  const got = await decideScope({
    pr: "1426", head: A, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
    api: apiFor({ [A]: blockedAt(A) }), run: runner(), comments: null, trusts: trustAll, log: quiet,
  });
  assert.equal(got.mode, "full");
  assert.equal(got.reason, "review-requested");
});

test("reviewRequested: answered only by a round that STARTED after the request; an unresolved author fails toward reviewing", async () => {
  const { reviewRequested } = await import("./review-scope.mjs");
  const ask = human("@claude rerun review", "2026-07-21T00:00:00Z");
  // A panel already in flight when the request was made finished after it — it
  // did not answer it, so `after` is the newest START, and the request stands.
  assert.equal(reviewRequested([ask], { trusts: trustAll, after: "2026-07-20T23:00:00Z" }), true);
  assert.equal(reviewRequested([ask], { trusts: trustAll, after: "2026-07-21T00:05:00Z" }), false);
  // A permission lookup that FAILED (null) is not a "no": the request stands.
  assert.equal(reviewRequested([ask], { trusts: () => null }), true);
  // A definite "no" is still ignored.
  assert.equal(reviewRequested([ask], { trusts: () => false }), false);
});

test("reviewRequested: an unresolved author may force a review but never cancel a trusted `rerun review`", async () => {
  const { reviewRequested } = await import("./review-scope.mjs");
  const ask = human("@claude rerun review", "2026-07-21T00:00:00Z");
  const plain = human("@claude rerun", "2026-07-22T00:00:00Z", "unresolved");
  const trusts = (login) => (login === "maintainer" ? true : null);
  // A plain rerun from someone whose permission could not be looked up is not
  // the maintainer's own later word, so the maintainer's request stands.
  assert.equal(reviewRequested([ask, plain], { trusts, after: "2026-07-20T00:00:00Z" }), true);
  // A read-only commenter (a definite "no") cannot cancel it either.
  const noAccess = (login) => login === "maintainer";
  assert.equal(reviewRequested([ask, { ...plain, user: { login: "reader", type: "User" } }], { trusts: noAccess }), true);
  // The maintainer's own later plain rerun still does.
  assert.equal(reviewRequested([ask, human("@claude rerun", "2026-07-22T00:00:00Z")], { trusts }), false);
  // An unresolved `rerun review` newer than a trusted plain rerun still forces.
  const older = human("@claude rerun", "2026-07-21T00:00:00Z");
  const unknownAsk = human("@claude rerun review", "2026-07-22T00:00:00Z", "unresolved");
  assert.equal(reviewRequested([older, unknownAsk], { trusts }), true);
});

test("reviewRequested: resolves trust only for reruns newer than `after`, newest first, until one decides", async () => {
  const { reviewRequested } = await import("./review-scope.mjs");
  const asked = [];
  const trusts = (login) => {
    asked.push(login);
    return login.startsWith("m") ? true : login.startsWith("u") ? null : false;
  };
  const comments = [
    human("@claude rerun review", "2026-07-01T00:00:00Z", "old-1"),
    human("@claude rerun", "2026-07-02T00:00:00Z", "old-2"),
    human("@claude rerun", "2026-07-21T00:00:00Z", "m-older"),
    human("@claude rerun review", "2026-07-22T00:00:00Z", "m-newer"),
    human("@claude rerun", "2026-07-23T00:00:00Z", "reader"),
    human("not a command", "2026-07-24T00:00:00Z", "chatter"),
  ];
  assert.equal(reviewRequested(comments, { trusts, after: "2026-07-20T00:00:00Z" }), true);
  // Never the comments the last round already answered, never a non-command,
  // and nothing older than the first trusted rerun.
  assert.deepEqual(asked, ["reader", "m-newer"]);
});

test("notFoundIsNoAccess: a 404 permission lookup is a definite no; any other failure stays unknown", async () => {
  const { notFoundIsNoAccess } = await import("./review-scope.mjs");
  const { permissionResolver } = await import("./gh-checks.mjs");
  const failing = (stderr) => () => {
    const e = new Error(`Command failed: gh api x\n${stderr}\n`);
    e.stderr = `${stderr}\n`;
    throw e;
  };
  const notFound = permissionResolver({ api: notFoundIsNoAccess(failing("gh: ghost is not a user (HTTP 404)")), log: quiet });
  assert.equal(notFound("ghost"), false);
  for (const stderr of ["gh: Forbidden (HTTP 403)", "gh: Server Error (HTTP 502)", "error connecting to api.github.com"]) {
    const broken = permissionResolver({ api: notFoundIsNoAccess(failing(stderr)), log: quiet });
    assert.equal(broken("someone"), null, stderr);
  }
  // A successful lookup passes through untouched.
  const ok = permissionResolver({ api: notFoundIsNoAccess(() => ({ permission: "write" })), log: quiet });
  assert.equal(ok("maintainer"), true);
});

// --- carry across a rebase -----------------------------------------------------

const C = "c".repeat(40);
const later = (runs, at) => runs.map((r) => ({ ...r, id: r.id + 100, started_at: at, completed_at: at }));

test("decideScope: a clean rebase of an approved head carries from the head the force-push replaced", async () => {
  // After a rebase the PR's commit list holds only the rewritten commits, so the
  // approved head's runs are reachable only through the force-push that dropped it.
  const base = {
    pr: "1426", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
    run: runner({ "cat-file -e": { ok: false, status: 1, stdout: "" } }),
    comments: [], trusts: trustAll, log: quiet,
  };
  const got = await decideScope({ ...base, api: apiFor({ [A]: approvedAt(A) }, { commits: [B], forcePushedFrom: [A] }) });
  assert.equal(got.mode, "carry");
  assert.equal(got.sourceSha, A);
  assert.equal(got.carry, 1);
  assert.equal(got.reason, "pr-diff-unchanged");
  // A rebase that changed the diff (conflict resolution) is reviewed in full.
  const changed = await decideScope({ ...base, fingerprint: "e".repeat(40), api: apiFor({ [A]: approvedAt(A) }, { commits: [B], forcePushedFrom: [A] }) });
  assert.equal(changed.mode, "full");
  // Only an approval carries.
  const blocked = await decideScope({ ...base, api: apiFor({ [A]: blockedAt(A) }, { commits: [B], forcePushedFrom: [A] }) });
  assert.equal(blocked.mode, "full");
  // The cap still counts the carries recorded on the replaced head.
  const capped = await decideScope({ ...base, api: apiFor({ [A]: approvedAt(A, { mode: "carry", carry: 2 }) }, { commits: [B], forcePushedFrom: [A] }) });
  assert.equal(capped.reason, "carry-cap");
  // And `rerun review` still overrides it.
  const asked = await decideScope({ ...base, comments: [human("@claude rerun review", "2026-07-21T00:00:00Z")], api: apiFor({ [A]: approvedAt(A) }, { commits: [B], forcePushedFrom: [A] }) });
  assert.equal(asked.reason, "review-requested");
});

test("decideScope: a replaced head is consulted only when it holds the NEWEST verdicts", async () => {
  // An older approval on a replaced head must not outvote a newer verdict on the
  // branch: that would pick the sample it liked.
  const got = await decideScope({
    pr: "1426", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
    api: apiFor({ [A]: approvedAt(A), [C]: later(blockedAt(C), "2026-07-21T10:00:00Z") }, { commits: [C, B], forcePushedFrom: [A] }),
    run: merged, comments: [], trusts: trustAll, log: quiet,
  });
  assert.notEqual(got.mode, "carry");
  // A replaced head whose verdicts do not carry falls back to the branch's own
  // state, exactly as before: an amend after an older on-branch round narrows
  // from that round's pointer.
  const amended = await decideScope({
    pr: "581", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: "e".repeat(40),
    api: apiFor({ [A]: stampedAt(A), [C]: later(approvedAt(C), "2026-07-21T10:00:00Z") }, { commits: [A, B], forcePushedFrom: [C] }),
    run: runner(), comments: [], trusts: trustAll, log: quiet,
  });
  assert.equal(amended.mode, "incremental");
  assert.equal(amended.sinceSha, A);
});

test("decideScope: an unreadable force-push history or replaced head costs only the carry", async () => {
  const throwing = (what) => (args) => {
    if (what === "graphql" && args[1] === "graphql") throw new Error("gh: 502");
    if (what === "runs" && args.some((a) => String(a).includes(`/commits/${A}/`))) throw new Error("gh: 422");
    return apiFor({ [A]: approvedAt(A) }, { commits: [B], forcePushedFrom: [A, null, "junk"] })(args);
  };
  for (const what of ["graphql", "runs"]) {
    const got = await decideScope({
      pr: "1426", head: B, manifest: MANIFEST, changedFiles: CODE, fingerprint: FP,
      api: throwing(what), run: runner(), comments: [], trusts: trustAll, log: quiet,
    });
    assert.equal(got.mode, "full", what);
    assert.equal(got.reason, "no-prior-state", what);
  }
});
