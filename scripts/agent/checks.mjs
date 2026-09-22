// Pure check-run gate logic, shared by mark-ready.mjs and its tests.
// A required check "passes" iff its LATEST run on the SHA concluded success;
// a required check that never ran counts as NOT passed (fail closed).

/**
 * Review checks the ready gate requires when the caller passes no
 * `--require-checks` — i.e. the local preview `spec-to-pr.mjs` suggests. The
 * promotion path in agent-review-panel.yml always passes the manifest-derived
 * `blocking && applicable` set, so this list cannot let a real promotion through.
 *
 * It holds only the blocking lenses that apply to EVERY diff (`appliesWhen` of
 * `**`). This caller has no panel output, so it cannot know which lenses were
 * applicable, and `checkPassed` counts a `neutral` (skipped) check as NOT
 * passed — so a path-scoped lens listed here makes the gate unsatisfiable for
 * every PR that legitimately skips it (test-adequacy on a docs-only PR, docs on
 * a code-only PR). Requiring the always-on lenses keeps the fallback meaningful
 * while staying satisfiable on ordinary code PRs (mark-ready still fails closed
 * on an empty set).
 *
 * KNOWN GAP, pre-dating file-class routing and not fixed here: "always
 * applicable" is not the same as "always has something to review". A lens can
 * apply and still be skipped when nothing in its `scopeClasses` changed, so a
 * prose-only PR skips correctness and this default gate cannot be satisfied.
 * The same hole existed before routing (test-adequacy on a docs-only PR). The
 * real promotion path is unaffected — agent-review-panel.yml always passes the
 * manifest-derived `blocking && applicable` set. Fixing it properly means
 * teaching this path that a neutral required check is a skip rather than a
 * failure, which is a change to the gate's semantics, not to this list.
 *
 * It lives here rather than in `mark-ready.mjs` for one reason: mark-ready is a
 * CLI with top-level `process.exit`, so a test cannot import it, and this is the
 * ONE lens list that does not derive itself from `lenses/lenses.json`. Drift is
 * silent, so `checks.test.mjs` asserts it against the real manifest.
 */
export const DEFAULT_REVIEW_CHECKS = [
  "agent-review-correctness",
  "agent-review-security",
];

/**
 * The CI workflow's identity, as the Actions API reports it on a run.
 *
 * A run's `name` is only the workflow file's `name:` key, and NOTHING makes it
 * unique — a second file saying `name: CI` produces runs indistinguishable from
 * the real ones. That matters because `mark-ready.mjs` calls gate 1
 * "unforgeable": anyone able to push a branch to the base repo could otherwise
 * add `.github/workflows/anything.yml` with `name: CI` on `push`, get a green
 * run recorded against their own head SHA, and satisfy the one gate that is
 * supposed to mean "the tests passed". (The agent App cannot push workflow
 * files — that is the boundary agent-review-panel.yml's `workflow_run` trigger
 * rests on — but an `agent:managed` human PR is on the same promote path and
 * is not restricted.)
 *
 * `path` cannot be spoofed the same way: only one file can occupy it, so a
 * run's path names the file that produced it. Keep this in step with the
 * filename on disk — `checks.test.mjs` asserts the file exists.
 *
 * This constant is for the WORKFLOW gates, which read
 * `github.event.workflow_run.path` out of the event payload and compare it with
 * exact equality (there is nothing to scope server-side in a `workflow_run`
 * trigger — its `workflows:` filter matches display names only). JS readers use
 * `CI_WORKFLOW_FILE` below instead and never parse a path.
 */
export const CI_WORKFLOW_PATH = ".github/workflows/ci.yml";

/**
 * The CI workflow's file name, as the Actions API's `workflow_id` path
 * parameter accepts it. Readers scope their query with this
 * (`/actions/workflows/ci.yml/runs?head_sha=…`) rather than fetching every run
 * for the SHA and matching `path` in JS.
 *
 * That is not a convenience — it is the identity check. Matching in JS means
 * parsing an attacker-influenced string, and the first attempt at it was
 * exploitable: it stripped an `@ref` suffix (for called workflows, a shape CI
 * does not have) with `path.split("@")[0]`, so a file named
 * `ci.yml@anything.yml` — a legal filename, and one Actions will run because it
 * ends in `.yml` — matched the real CI workflow and re-opened the forgery the
 * check exists to close. Server-side scoping cannot be spoofed by a filename,
 * and it also bounds the response to CI's own runs, so a flood of unrelated
 * runs cannot push the CI run off the page.
 */
export const CI_WORKFLOW_FILE = "ci.yml";

/**
 * Paths whose CONTENTS decide what a CI run does — gate 1b's refusal surface.
 *
 * A `pull_request` run executes the merge ref's copy of everything CI reaches,
 * so a branch that edits any of these hands gate 1 a green run that proves
 * nothing about main's CI. `.github/workflows/ci.yml` is the obvious one and is
 * nowhere near sufficient: `ci.yml` contains almost no test logic. It runs
 * `pnpm verify:self` and `pnpm verify:integration`, both resolved from the merge
 * ref's root `package.json` into `scripts/verify-*.mjs`, whose lane selection
 * reads `harness.config.json`. Listing only the two `.github` prefixes — which an
 * earlier revision of this gate did — meant a branch could gut CI through
 * `package.json` and still auto-promote, while the hand-off comment told the
 * human reviewer the run had executed main's CI definition.
 *
 * This list MIRRORS `harness.config.json`'s `ci.ciConfig` — the repository's own,
 * CODEOWNER-ed definition of "files that decide how much CI runs" — plus two
 * entries that list does not need and this gate does:
 *   - `.github/actions/**` — a composite action is workflow content that happens
 *     to live elsewhere; `ciConfig` omits it only because no such action exists
 *     yet.
 *   - a per-package `package.json` (one wildcard segment under `packages/`) — a
 *     package's own `test`/`typecheck` script is what `verify:self` invokes for
 *     that package, so editing it edits a lane. Spelled in prose because the
 *     glob's middle wildcard followed by a slash would close this comment.
 * `checks.test.mjs` asserts the mirror covers `ciConfig` entry for entry, so the
 * two cannot drift silently.
 *
 * Hard-coded here rather than read from `harness.config.json` at runtime for a
 * provenance reason: the promote job checks out the DEFAULT BRANCH, so this
 * module is main's copy, whereas a file read would be whatever tree the caller
 * happens to be standing in. The drift test is what keeps the duplication honest.
 *
 * WHAT THIS STILL DOES NOT COVER, stated because the hand-off comment must not
 * overclaim it: a branch can always edit its own test files, and CI dutifully
 * runs the weakened tests. That residue is a REVIEW property (test-adequacy reads
 * the diff), not a gate property. Gate 1b's claim is therefore "the branch
 * supplied no part of the CI/harness definition", never "every assertion CI ran
 * came from main".
 */
export const CI_DEFINING_PATHS = [
  ".github/workflows/**",
  ".github/actions/**",
  ".github/CODEOWNERS",
  // The lanes themselves. `ci.yml` calls `make lint`, `make build` and a
  // `go test` line; what those resolve to lives in the Makefile and
  // `.golangci.yml`, so a branch editing either supplies part of the CI
  // definition exactly as if it had edited the workflow.
  "Makefile",
  ".golangci.yml",
  "codecov.yml",
  // The toolchain and the dependency graph CI resolves before it runs anything.
  "go.mod",
  "go.sum",
  // Codegen configuration: the `buf generate` freshness gate is only as strict
  // as the config it regenerates from.
  "buf.gen.yaml",
  "buf.work.yaml",
  "api/buf.yaml",
  "api/buf.gen.yaml",
  // The stack the integration lane is graded against.
  "build/docker/**",
  // Scripts a workflow invokes directly.
  "scripts/ci/**",
  "scripts/verify-*.mjs",
];

// `**` spans separators, `*` does not, everything else is literal. Deliberately
// tiny and deliberately anchored at both ends: this is a REFUSAL surface, so a
// pattern that accidentally matched less is a fail-open.
const CI_DEFINING_RE = CI_DEFINING_PATHS.map(
  (glob) =>
    new RegExp(
      `^${glob
        .split("**")
        .map((span) =>
          span
            .split("*")
            .map((lit) => lit.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
            .join("[^/]*"),
        )
        .join(".*")}$`,
    ),
);

/** Does this changed path define what CI does? Non-strings → false. */
export function definesCi(name) {
  if (typeof name !== "string" || name === "") return false;
  return CI_DEFINING_RE.some((re) => re.test(name));
}

/**
 * CI's verdict for a SHA: `"success"`, `"failure"`, or `null` for "not known
 * yet" — no run, or the newest one still in flight.
 *
 * THE NEWEST RUN WINS. A SHA *can* carry more than one CI run — closing and
 * reopening a PR files a second `pull_request` run for the same commit — and
 * newest-wins is the right answer there: the later run is a fresh execution of
 * the same file at the same tree, so it supersedes the earlier one exactly as a
 * re-run would. (A re-run does not even produce a second run; GitHub bumps
 * `run_attempt` on the existing one.)
 *
 * What must NOT happen is two runs from DIFFERENT triggers racing for the same
 * PR head, where "newest" is then arbitrary rather than superseding. Today none
 * can: `push` is restricted to `main`, and a `merge_group` run carries the
 * speculative merge commit rather than the PR head. `checks.test.mjs`'s
 * trigger test pins that set, so adding a trigger that fires on a PR head fails
 * loudly there instead of quietly turning this into "read one of several
 * unrelated runs".
 *
 * An earlier revision required EVERY run to be green, on the theory that
 * newest-wins would fail open if a SHA ever carried two runs. It closed nothing
 * reachable — the invariant above already held — and cost three real defects:
 * `@claude rerun` could no longer clear the gate (it re-ran one run), then
 * re-running all of them eroded agent-iterate-ci's append-only attempt bound
 * (a re-run REPLACES a run's `failure` conclusion) and fanned out into
 * `workflow_run` completions that cancelled the fixer mid-push. Fail-closed on
 * a hole that is not open is not free.
 *
 * Ordered by `id`, not `created_at`: ids are assigned in creation order, are
 * always present, and are integers — `new Date("nonsense")` is NaN, and a NaN
 * comparator does not order at all.
 */
export function ciConclusion(workflowRuns) {
  const run = newestRun(workflowRuns || []);
  if (!run || run.conclusion == null) return null;
  return run.conclusion === "success" ? "success" : "failure";
}

/** Newest by run id (creation order), or null. */
function newestRun(runs) {
  if (!runs.length) return null;
  return runs.reduce((newest, r) => ((r.id ?? 0) > (newest.id ?? 0) ? r : newest));
}

/**
 * The run `@claude rerun` / `@claude loop` must re-run for a head SHA, or null.
 *
 * Exactly one — the newest COMPLETED run. Re-running more would erode
 * agent-iterate-ci's attempt bound (which counts current `failure` conclusions,
 * and a re-run replaces one) and emit a completion per run into a
 * `cancel-in-progress` group, cancelling the fixer mid-push.
 *
 * COMPLETED because the API answers 422 for a run still in flight. Note this is
 * NOT always the run `ciConclusion` reads: with a newer run still in flight,
 * gate 1 already answers `null` ("not known yet") and this returns the finished
 * one underneath it. That is the right pair anyway — re-running the finished
 * one adds the `run_attempt > 1` event the panel re-engages on without waiting,
 * and gate 1 reads whichever ends up newest when it next runs.
 *
 * When NOTHING is completed, the answer is null and the caller must not stop
 * there — see `ciRunToAwait`.
 *
 * `workflowRuns` is expected to be already scoped to the CI workflow file by
 * the caller's `workflow_id: 'ci.yml'`, so this does not re-filter by path.
 *
 * MIRRORED INLINE at both call sites, because neither can import this file:
 * they are github-script steps in `.github/workflows/agent-rerun.yml` and
 * `.github/workflows/agent-loop.yml` that run before any checkout — the same
 * constraint that makes `agent-review-panel.yml` mirror `ciRunDecision` inline.
 * `checks.test.mjs` pins the copies against each other. Editing this function
 * means editing both mirrors, which only a maintainer can push.
 */
export function ciRunToRerun(workflowRuns) {
  return newestRun((workflowRuns || []).filter((r) => r?.status === "completed"));
}

/**
 * The run `@claude rerun` / `@claude loop` must WAIT for before it can re-run
 * anything, or null. Only consulted when `ciRunToRerun` found nothing.
 *
 * An in-flight run used to end the verb: "No CI run to re-run — the panel will
 * engage on the next CI run." There is no next CI run. The round's one
 * `requested` event was spent while the PR was still latched (the panel's
 * `gate` fires ~5s after CI is created, and the operator types the comment
 * minutes later), and the panel refuses a `completed` event on `run_attempt`
 * 1 — so the only thing that could re-engage the round is the attempt 2 this
 * verb declined to produce. #1047 and #1052 both sat unreviewed for five hours
 * on exactly that, while the comment said the loop was resuming.
 *
 * NOT COMPLETED, rather than an allow-list of `queued` / `in_progress`:
 * GitHub has added run statuses before (`waiting`, `pending`, `requested`), and
 * a status this pipeline has never heard of has to read as "still going" —
 * which makes the caller wait and then re-run — not as "nothing here", which is
 * the bug above. The two selections therefore partition the listing.
 *
 * MIRRORED INLINE at both call sites for the same reason `ciRunToRerun` is,
 * and pinned by the same test.
 */
export function ciRunToAwait(workflowRuns) {
  return newestRun((workflowRuns || []).filter((r) => r?.status !== "completed"));
}

/**
 * The App slug every check run this repo's gates will believe must carry.
 *
 * A check run's NAME is not a secret and is not reserved: ANY integration
 * installed on the repository may create a check run called
 * `agent-review-security` and conclude it `success`. Without a producer check,
 * gate 2 — the one that means "an independent reviewer approved this" — is
 * satisfiable by anything that can talk to the Checks API, which is the exact
 * forgery class gate 1 closes by scoping its query to a workflow FILE. `app.slug`
 * is set by GitHub from the installation, not by the payload the app sends, so it
 * cannot be chosen by the app that reports it.
 *
 * `github-actions` is the slug of a run created by a workflow in this repository
 * via `GITHUB_TOKEN`. It is NOT per-workflow identity: any workflow here that
 * declared `checks: write` could post under the same slug. Today only the review
 * panels do, and no author workflow can gain it — a branch cannot edit
 * `.github/workflows/**`, because the agent App has no `workflows` scope. So this
 * filter narrows the producer set from "every App on the installation" to
 * "workflows in this repository", and review maintains the rest. That is the same
 * posture `fix-eligible.mjs`, `fix-brief.mjs` and `review-state.mjs` already take;
 * gate 2 was the one reader that did not.
 */
export const CHECK_PRODUCER_APP_SLUG = "github-actions";

/**
 * Latest run of `name` from the trusted producer concluded success? Missing →
 * false. A run of the right name from ANOTHER App is not evidence and is not
 * even considered — dropping it before the sort matters, because a forged run
 * with a newer `started_at` would otherwise shadow the real verdict.
 */
export function checkPassed(checkRuns, name) {
  const runs = (checkRuns || []).filter(
    (r) => r?.name === name && r?.app?.slug === CHECK_PRODUCER_APP_SLUG,
  );
  if (runs.length === 0) return false;
  runs.sort((a, b) => new Date(b.started_at ?? 0) - new Date(a.started_at ?? 0));
  return runs[0].conclusion === "success";
}

/** { allPassed, perCheck } for a list of required check names. */
export function allRequiredPassed(checkRuns, requiredNames) {
  const perCheck = Object.fromEntries(requiredNames.map((n) => [n, checkPassed(checkRuns, n)]));
  return { allPassed: requiredNames.every((n) => perCheck[n]), perCheck };
}

/**
 * What should the panel's PUSHING jobs do with the CI run this round rode in on?
 *
 * The panel is triggered by `workflow_run: requested`, so it starts WHEN CI
 * starts rather than after it — that is where the ~13.5 min per round comes
 * from. The CI conclusion is still load-bearing, and it is read here instead.
 *
 * WHAT THIS PRESERVES. Two agent arms watch the same CI run and split on its
 * outcome: `agent-iterate-ci.yml` fires on `failure` and pushes a CI fix, the
 * review `fix` job runs on `success` and pushes a review fix. BOTH push to the
 * same branch, so the conclusion is a MUTEX, not merely a quality gate. Moving
 * the panel to `requested` does not weaken it — the discriminator is the same
 * one, on the same run, read later. For any given CI run exactly one arm still
 * proceeds, which is why no extra `concurrency` group is needed to make that
 * true.
 *
 * `wait` is not `proceed` and must never be treated as one. It means the run is
 * still going, and the caller polls. Everything else — a missing run, a junk
 * payload, a `cancelled`/`timed_out`/`neutral` conclusion — is `skip`: the
 * pushing jobs do not run, which leaves the PR exactly where the old
 * `conclusion == 'success'` trigger would have left it (untouched), and lets
 * `agent-iterate-ci` own the red-CI case as it already does.
 *
 * A `skip` is deliberately NOT a page. CI red is a state the pipeline has a
 * handler for; paging on it would fire on every ordinary CI failure. Only the
 * caller's own timeout — "we never found out" — pages, because that is the
 * state nothing else is watching.
 */
export function ciRunDecision(run) {
  // `Array.isArray` explicitly: `typeof [] === "object"`, so a list-shaped
  // response would otherwise read as a run with no `status` and be polled until
  // the deadline. Nothing that is not an object can BECOME one, so re-asking is
  // pointless — that is the line between this and the `{}` case below.
  if (!run || typeof run !== "object" || Array.isArray(run)) return "skip";
  // An object that is merely missing fields gets another poll: a partial or
  // rate-limited response is transient, and the caller's own deadline bounds
  // how long "no status yet" can go on before it pages.
  if (run.status !== "completed") return "wait";
  return run.conclusion === "success" ? "proceed" : "skip";
}
