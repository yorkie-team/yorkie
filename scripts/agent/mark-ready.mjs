// Ready gate for the autonomous contribution loop.
//
// Promotes an agent-authored draft PR to "ready for human review" ONLY when the
// hand-off preconditions all hold. Gates 1 and 2 read evidence a SEPARATE ACTOR
// produced, rather than anything the author agent writes:
//
//   1. The newest run of the CI workflow for the PR head SHA concluded
//      `success` (read via the Actions API — the author agent cannot create a
//      CI run; this replaces parsing the <!-- harness-verification --> comment,
//      which the author's issues:write could post). The runs are scoped to the
//      CI workflow FILE by the API itself (`/actions/workflows/ci.yml/runs`),
//      not matched on a run's display name — which is only a `name:` key any
//      second workflow file could claim — and not by parsing a path in JS. See
//      CI_WORKFLOW_FILE in checks.mjs for why that distinction is load-bearing.
//
//      Scoping by file proves WHICH file ran; it cannot prove WHAT was in it. A
//      `pull_request` run executes the PR MERGE REF's copy of `ci.yml`, so a
//      branch that can edit that file produces a genuinely green run at the
//      genuine path with no tests in it. Gate 1b closes that:
//   1b. The branch supplies NO part of the CI definition — none of
//      `CI_DEFINING_PATHS` (checks.mjs), which mirrors harness.config.json's own
//      `ci.ciConfig` gating surface: workflows, composite actions, the verify
//      scripts, the root and per-package manifests, the lockfile. If it touches
//      any of them, CI's verdict for this SHA is evidence about the BRANCH's CI
//      definition rather than about main's, so it is not the evidence gate 1
//      claims to read and auto-promotion is refused. Such a PR is not blocked,
//      only un-automated: a human reviews the change and flips it to ready by
//      hand, which is where "should this CI definition land" belongs anyway. The
//      agent App cannot push `.github/workflows/**` (the same boundary
//      agent-review-panel.yml's `workflow_run` trigger rests on) but CAN push
//      every other path on that list, and an `agent:managed` human PR is on this
//      promote path and is not restricted at all — which is why the gate exists.
//
//      Its EVIDENCE is pinned twice, because a file list is only meaningful
//      relative to a base and a head, and both are mutable:
//        - the diff must be against the repository's DEFAULT BRANCH. `pulls/N/files`
//          answers for the PR's CURRENT base, which the author may retarget at
//          will and which may itself already carry a gutted CI definition (a
//          stacked PR). A PR based anywhere else is refused rather than measured.
//        - the head must not have moved while the gate read it. Gate 1's evidence
//          is a CI run pinned to the SHA read at the top of this file; a push
//          landing between the two reads would otherwise let a green run for the
//          gutted SHA be paired with a clean file list for the repaired one.
//   2. Every required `agent-review-<lens>` check run on the PR head SHA
//      concluded `success` — an INDEPENDENT reviewer approved it. The check must
//      come from `CHECK_PRODUCER_APP_SLUG` as well as carry the right name: a
//      check-run name is not reserved, so without the producer filter any App on
//      the installation could manufacture the approval (checks.mjs::checkPassed).
//
// Gate 3 is a REQUIRED SELF-DISCLOSURE, not separate-actor evidence — the author
// agent writes the PR body. It is not adversary-proof (a truthful agent has no
// incentive to hide its own authorship; a dishonest one simply stays a draft).
// It is belt-and-suspenders with the commit-trailer hook:
//   3. The PR body discloses autonomous authorship.
//
// It NEVER merges. After promotion it flips draft → ready, sets the single
// `agent:ready` lifecycle label (via set-state's computeLabelSet), and posts a
// hand-off comment. The final review + merge stay human, enforced by branch
// protection.
//
// Usage:
//   node ./scripts/agent/mark-ready.mjs <pr-number> [--promote] [--require-checks a,b,c]
//     (default is a dry run that only reports gate status and exits non-zero if
//      the PR is not ready; pass --promote to actually flip the PR to ready)
//     --require-checks: comma-separated check-run names that must ALL be success
//      (defaults to the review-panel lens checks below).
//
// Requires the `gh` CLI authenticated via GH_TOKEN / GITHUB_TOKEN.

import { execFileSync } from "node:child_process";
import { allRequiredPassed, ciConclusion, CI_WORKFLOW_FILE, definesCi, DEFAULT_REVIEW_CHECKS } from "./checks.mjs";
import { computeLabelSet } from "./set-state.mjs";
import { disclosesAiAuthorship, HANDOFF_MARKER } from "./disclosure.mjs";

const prNumber = process.argv[2];
const promote = process.argv.includes("--promote");

if (!prNumber || !/^\d+$/.test(prNumber)) {
  console.error("Usage: node ./scripts/agent/mark-ready.mjs <pr-number> [--promote] [--require-checks a,b,c]");
  process.exit(2);
}

const rcIdx = process.argv.indexOf("--require-checks");
// Absent flag → defaults. Explicit `--require-checks ""` → empty set. Only the
// missing flag falls back to DEFAULT; an explicitly empty value must NOT (else
// an all-advisory / no-blocking-lens panel would be pinned against default
// checks it never posted and could never promote).
const REQUIRED_CHECKS =
  rcIdx === -1
    ? DEFAULT_REVIEW_CHECKS
    : (process.argv[rcIdx + 1] ?? "").split(",").map((s) => s.trim()).filter(Boolean);

// FAIL CLOSED on an empty required-check set. `allRequiredPassed(runs, [])` is
// vacuously true (`[].every` → true), so an empty set would satisfy gate 2 with
// ZERO review evidence — a fail-open in a component whose whole job is to fail
// closed. Treat it as a tooling error unless the caller OPTS IN explicitly.
//
// This used to note the case was unreachable because every lens was `**`-scoped.
// That stopped being true when design-fit, test-adequacy, and now blast-radius
// gained path globs. It is still unreachable — correctness and security remain
// blocking at `**`, which `review-panel.test.mjs` asserts against the real
// manifest — but the guarantee now rests on that invariant rather than on all
// lenses being unscoped.
if (REQUIRED_CHECKS.length === 0 && !process.argv.includes("--allow-no-checks")) {
  console.error(
    "Refusing to promote with an empty required-check set: gate 2 (review-panel " +
      "approval) would pass with no evidence. Pass --allow-no-checks only if a " +
      "no-blocking-lens panel is genuinely intended.",
  );
  process.exit(2);
}

function gh(args) {
  return execFileSync("gh", args, { encoding: "utf8" });
}

function ghJson(args) {
  return JSON.parse(gh(args));
}

// The promotion mutations (mark ready / labels / hand-off comment) need a token
// that can perform markPullRequestReadyForReview. The default GITHUB_TOKEN
// CANNOT — it returns "Resource not accessible by integration", which silently
// left gate-passing PRs stuck as drafts. Use GH_MUTATION_TOKEN (a GitHub App
// token) for these calls when provided; otherwise fall back to the default `gh`
// token (e.g. local dry runs).
function ghMutate(args) {
  const token = process.env.GH_MUTATION_TOKEN;
  const env = token ? { ...process.env, GH_TOKEN: token } : process.env;
  return execFileSync("gh", args, { encoding: "utf8", env });
}

// --- gather PR state -------------------------------------------------------

let pr;
try {
  pr = ghJson([
    "pr",
    "view",
    prNumber,
    "--json",
    "number,body,isDraft,labels,baseRefName,headRefName,headRefOid,url",
  ]);
} catch (err) {
  console.error(`Failed to read PR #${prNumber}: ${err.message}`);
  process.exit(2);
}

const body = pr.body ?? "";

// --- gate 1: CI passed (authoritative, not the author-writable PR comment) --

// Read the "CI" workflow-run conclusion for the PR head SHA via the Actions API.
// The author agent's workflows cannot create a CI workflow run or forge its
// conclusion, so this is evidence a separate actor (GitHub Actions) produced —
// unlike the <!-- harness-verification --> PR comment, which the author agent
// could post itself with issues:write. A workflow run concludes "success" only
// when every CI job (verify-self / verify-browser / verify-integration) passed.
function ciPassed(sha) {
  if (!sha) return false;
  let data;
  try {
    // Scoped to the CI workflow FILE by the API itself, not filtered in JS
    // afterwards. A run's `name` is only a `name:` key, so a second file
    // claiming `name: CI` would forge the one gate that means "the tests
    // passed" — and matching the run's `path` in JS is string parsing on an
    // attacker-influenced value, which is how `ci.yml@pwn.yml` slipped through
    // an earlier revision. GitHub resolves `ci.yml` here to the workflow that
    // file defines. It also bounds the response to CI's own runs, so a flood of
    // unrelated runs cannot push the CI run past `per_page`.
    data = ghJson([
      "api",
      `repos/{owner}/{repo}/actions/workflows/${CI_WORKFLOW_FILE}/runs?head_sha=${sha}&per_page=100`,
    ]);
  } catch (err) {
    // A TOOLING ERROR, not a red gate — the same distinction the promote job's
    // exit-1 branch exists to make. Scoping the query to a workflow FILE added
    // a 404 class the unscoped endpoint did not have: rename or delete
    // `ci.yml` and this throws, and `return false` would report it as "CI is
    // not green", leaving every PR a silent draft forever while the job
    // SUCCEEDED. `checks.test.mjs` asserts the file exists, so this fires for a
    // rename that lands between test and run — or for an API outage, where
    // failing the job is also right: nothing was learned about CI.
    console.error(
      `Failed to read runs of ${CI_WORKFLOW_FILE} for ${sha}: ${err.message}\n` +
        "This is a tooling error, not a gate refusal — the CI workflow may have been renamed.",
    );
    process.exit(2);
  }
  return ciConclusion(data.workflow_runs) === "success";
}

const ciGate = ciPassed(pr.headRefOid);

// --- gate 1b: the CI definition came from main, not from the branch ---------

// Which paths count is `CI_DEFINING_PATHS` in checks.mjs, mirrored from
// harness.config.json's `ci.ciConfig` — see `definesCi` there for why the two
// `.github` prefixes this gate started with were nowhere near enough.

// Paginated explicitly rather than with `gh api --paginate`, which concatenates
// one JSON array per page and does not parse. A PR too large to enumerate is a
// REFUSAL, not a pass: the whole point is that an unreadable file list cannot
// rule out a CI-definition edit.
function ciDefiningPathsChanged(number) {
  const hits = [];
  // `<=` and 100-per-page: 10 full pages is 1000 files enumerated, so the throw
  // below is reached only by a PR with MORE than that — an off-by-one here would
  // refuse a PR with exactly 1000.
  for (let page = 1; page <= 10; page++) {
    const files = ghJson(["api", `repos/{owner}/{repo}/pulls/${number}/files?per_page=100&page=${page}`]);
    if (!Array.isArray(files)) throw new Error("unexpected response from the PR files endpoint");
    for (const f of files) {
      // `previous_filename` too: a rename INTO or OUT OF a CI-defining path
      // changes what CI runs while only one of the two names is such a path.
      for (const name of [f?.filename, f?.previous_filename]) {
        if (definesCi(name)) hits.push(name);
      }
    }
    // Keep paging even once a hit is found: this loop's OTHER job is to prove
    // the list was fully enumerable, and returning early on page 1 would report
    // a definite refusal from a partially-read list. It is bounded anyway.
    if (files.length < 100) return hits;
  }
  throw new Error("more than 1000 changed files — cannot rule out a CI-definition edit");
}

function defaultBranch() {
  const name = ghJson(["api", "repos/{owner}/{repo}"])?.default_branch;
  if (typeof name !== "string" || name === "") {
    throw new Error("could not read the repository's default branch");
  }
  return name;
}

function ranBaseCiDefinition(number) {
  try {
    // (a) PIN THE BASE. `pulls/N/files` is the diff against the PR's CURRENT
    // base, an author-controlled field. Retargeting it onto a branch that
    // already carries a gutted `ci.yml` empties the list, and a PR stacked on
    // such a branch never showed the edit in the first place. Only a diff
    // against the default branch answers the question this gate asks.
    const base = defaultBranch();
    if (pr.baseRefName !== base) {
      throw new Error(
        `the PR targets '${pr.baseRefName}', not the default branch '${base}' — its file ` +
          "list is a diff against a ref that may already carry a CI definition of its own",
      );
    }

    const hits = ciDefiningPathsChanged(number);

    // (b) PIN THE HEAD. The list above is for whatever the head is NOW; gate 1's
    // evidence is a CI run for the SHA read at the top of this run. A push in
    // between could pair a green run for a gutted commit with a clean file list
    // for the commit that repaired it. Re-read last, so it covers the whole
    // window in which the evidence was gathered.
    const head = ghJson(["pr", "view", String(number), "--json", "headRefOid"])?.headRefOid;
    if (head !== pr.headRefOid) {
      throw new Error(
        `the head moved from ${pr.headRefOid} to ${head} while the gate was reading — CI's ` +
          "verdict and the changed-file list are no longer about the same commit",
      );
    }

    if (hits.length > 0) {
      console.error(
        `PR #${number} changes ${hits.length} path(s) that define what CI does, so its CI run ` +
          `is not evidence about main's CI definition: ${[...new Set(hits)].sort().join(", ")}`,
      );
      return false;
    }
    return true;
  } catch (err) {
    // FAIL CLOSED, and say why: a gate that cannot read its evidence has not
    // satisfied itself. Exit 1 (a gate said no) rather than 2 — deliberately
    // UNLIKE gates 1 and 2, which now exit 2 when their API read fails. The
    // difference is what a human has to do next. Gates 1 and 2 read evidence
    // that must exist for any PR, so a failure there is a broken pipeline and
    // should page. This gate answers a policy question — does the PR touch the
    // CI definition — where "could not tell" and "yes it does" call for the
    // same response: leave it a draft for a human to promote deliberately.
    console.error(`Could not establish PR #${number}'s CI definition: ${err.message}`);
    return false;
  }
}

const ciDefinitionGate = ranBaseCiDefinition(prNumber);

// --- gate 2: every required review-panel lens check passed -----------------

// Evidence-based: read the per-lens `agent-review-<lens>` check runs on the PR
// head SHA. Only the review-panel workflow (checks:write) can post them; the
// author agent cannot forge them. The pass logic lives in checks.mjs (tested).
function reviewChecks(sha) {
  if (!sha) return { allPassed: false, perCheck: {} };
  let data;
  try {
    data = ghJson(["api", `repos/{owner}/{repo}/commits/${sha}/check-runs?per_page=100`]);
  } catch (err) {
    // A TOOLING ERROR, exactly as in gate 1 above. Reporting an unreadable API
    // as "the reviewer did not approve" is the same silent-draft-forever bug:
    // the promote job would succeed, nothing would re-run the panel, and a PR
    // the reviewer HAD approved would sit as a draft on evidence nobody read.
    console.error(
      `Failed to read check runs for ${sha}: ${err.message}\n` +
        "This is a tooling error, not a gate refusal — no review verdict was read.",
    );
    process.exit(2);
  }
  return allRequiredPassed(data.check_runs ?? [], REQUIRED_CHECKS);
}

const { allPassed: reviewApproved, perCheck } = reviewChecks(pr.headRefOid);

// --- gate 3: AI disclosure -------------------------------------------------

const disclosure = disclosesAiAuthorship(body);

// --- report ----------------------------------------------------------------

const gates = [
  { name: "CI verification (verify:self ✅ + verify:integration ✅/skip)", ok: ciGate },
  { name: "CI's definition came from main (branch supplies no CI-defining path)", ok: ciDefinitionGate },
  { name: `Review panel approved (all lens checks ✅: ${REQUIRED_CHECKS.join(", ")})`, ok: reviewApproved },
  { name: "AI authorship disclosed in PR body", ok: disclosure },
];

console.log(`Ready-gate report for PR #${prNumber} (${pr.url})`);
for (const g of gates) {
  console.log(`  ${g.ok ? "✅" : "❌"} ${g.name}`);
}
if (!reviewApproved) {
  for (const c of REQUIRED_CHECKS) console.log(`      ${perCheck[c] ? "✅" : "❌"} ${c}`);
}

const allOk = gates.every((g) => g.ok);

if (!allOk) {
  console.log("\nNot promoting: one or more gates are not satisfied.");
  process.exit(1);
}

if (!promote) {
  console.log("\nAll gates satisfied. Re-run with --promote to flip the PR to ready.");
  process.exit(0);
}

if (!pr.isDraft) {
  console.log("\nPR is already marked ready — nothing to do.");
  process.exit(0);
}

// --- promote ---------------------------------------------------------------

// Flip draft → ready. This is the one mutation the default GITHUB_TOKEN can't
// do; a failure here is a permission/tooling problem, NOT a gate failure — exit
// 3 (distinct from the exit-1 "gates not satisfied") with a clear message so the
// workflow surfaces it loudly (and the stalled net pages a human) instead of
// silently leaving the PR a draft.
try {
  ghMutate(["pr", "ready", prNumber]);
} catch (err) {
  console.error(
    `\nAll ready-gates passed, but flipping PR #${prNumber} to ready FAILED: ${err.message}\n` +
      "This is a permission/tooling problem, not a gate failure. The promote job " +
      "must pass a GitHub App token via GH_MUTATION_TOKEN that can mark a PR ready — " +
      "the default GITHUB_TOKEN cannot (markPullRequestReadyForReview).",
  );
  process.exit(3);
}

// Single-value state → `agent:ready` (best-effort; a label hiccup must not abort
// the promotion or block the hand-off comment). REPLACE the whole label set so
// exactly one lifecycle label survives and non-agent labels (and the issue-side
// agent:candidate, if present) are preserved.
try {
  // Re-read labels immediately before the full-set PUT: the `pr.labels` fetched
  // at the top of this run is stale by now, and a REPLACE built from it could
  // drop a label added since. This shrinks (doesn't eliminate) that window; the
  // advisory label + set-state reconcile remain the backstop for a lost race.
  let current;
  try {
    current = ghJson(["pr", "view", prNumber, "--json", "labels"]).labels;
  } catch {
    current = pr.labels; // fall back to the initial snapshot
  }
  const labels = computeLabelSet(current || [], "ready");
  const args = ["api", "-X", "PUT", `repos/{owner}/{repo}/issues/${prNumber}/labels`];
  for (const l of labels) args.push("-f", `labels[]=${l}`);
  ghMutate(args);
} catch (err) {
  console.warn(
    `Could not set the 'agent:ready' label: ${err.message} — ensure the agent:* ` +
      "state labels exist in the repo's label settings.",
  );
}

const handoff = [
  HANDOFF_MARKER,
  "## 🤝 Ready for human review",
  "",
  "This PR was authored autonomously by Claude Code and has cleared the harness",
  "ready gate:",
  "",
  "- ✅ CI verification (`verify:self` and `verify:integration`) is green, for this",
  "  exact head SHA, on a PR based on the default branch — and the branch changes",
  "  none of the paths that define what CI does (`harness.config.json`'s",
  "  `ci.ciConfig` surface: workflows, composite actions, `scripts/verify-*.mjs`,",
  "  the root and per-package manifests, the lockfile), so the CI definition that",
  "  run executed came from `main` rather than from the branch.",
  "  This does NOT mean every assertion CI ran is main's: a branch can still weaken",
  "  its own tests, which is what the test-adequacy lens reads the diff for.",
  `- ✅ The review panel approved with no blocking findings (${REQUIRED_CHECKS.join(", ")}).`,
  "- ✅ Autonomous authorship is disclosed in the PR body.",
  "",
  "**No human has verified these changes yet — please review every line.** The",
  "approving reviewer is the accountable signer, exactly as `CONTRIBUTING.md`",
  "requires. Merge, release, and deploy remain manual.",
].join("\n");

// Best-effort: the PR is already flipped to ready; don't fail the promotion if
// the hand-off comment can't be posted.
try {
  ghMutate(["pr", "comment", prNumber, "--body", handoff]);
} catch (err) {
  console.warn(`PR flipped to ready, but posting the hand-off comment failed: ${err.message}`);
}

console.log(`\nPromoted PR #${prNumber} to ready and requested human review.`);
