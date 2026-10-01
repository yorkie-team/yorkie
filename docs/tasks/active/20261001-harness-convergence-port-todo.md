# Port the agent-loop convergence work from yorkie-js-sdk

**Created**: 2026-10-01

yorkie-js-sdk#1426 showed the agent loop re-litigating code nobody had
changed: an approved PR went to `agent:blocked` after a diff-neutral merge of
main, a rerun re-reviewed an identical head for about $10, and fix rounds that
died on the API were paged as "the fixer failed". yorkie-js-sdk fixed that in
two PRs. This task ports their harness parts here:

- yorkie-js-sdk#1428 (`898cd697`): carry approvals across diff-neutral heads
  (patch-id fingerprint), reuse verdicts on an identical-head rerun, probe the
  fixer credential before dispatch, and page an infra failure honestly.
- yorkie-js-sdk#1432 (`bc591151`): record tests a fix round removed as evidence
  for the adjudicator, tell design-fit when it has no spec (scope findings
  advisory), and count clean-to-blocking escalations in the metrics.

The design, the decisions and every review fix are in yorkie-js-sdk's
`docs/tasks/active/20261001-harness-convergence-{todo,lessons}.md`. This file
records only what the port needed and where yorkie differs.

## Scope

Only the harness: `scripts/agent/**`, `.github/workflows/agent-*`, the
maintainer-merge skill caution, and the convergence section of the design
doc (`docs/design/agent-command-verbs.md` here; `agent-harness.md` upstream).
None of the SDK source changes in those PRs.

## Plan

- [x] Measure the starting point. Byte-identical with js-sdk before #1428:
  fix-report, metrics, pick-fix-credential, rebuttal, review-scope,
  review-state (and their tests). Different: review-panel(.test),
  review-scope.test, lenses/design-fit.md, the maintainer-merge skill and the
  four Go-shaped workflows.
- [x] Apply #1428's harness diff. Every script and workflow hunk applied
  without conflict; only the skill hunk needed a hand merge.
- [x] Apply #1432's harness diff. Hand-merged: the D2 fixer-prompt rule in
  `agent-fix.yml` and `agent-review-panel.yml` (yorkie's prompts are Go-shaped),
  and two `review-panel.test.mjs` tests appended.
- [x] Adapt `test-removals.mjs` to Go test files, cases, subtests, skips and
  build constraints, keeping the JS rules for `scripts/agent/*.test.mjs`.
  Go fixtures in `test-removals.test.mjs`.
- [x] Adapt the D2 fixer rule to Go (no `it.fails`), and the wiring test that
  pins it (`evidence-wiring.test.mjs`).
- [x] Add the maintainer-merge caution for agent-managed PRs.
- [x] Record the convergence design in `docs/design/agent-command-verbs.md` §6.
- [x] Verify: `scripts/agent` suite, `scripts/test` suite, actionlint, doc
  gates, and a diff of every workflow against js-sdk's.
- [x] Address the independent review (see "Review fixes" below), each Red
  then Green.
- [x] Verify and fix the `/code-review high` findings (see "Code review"
  below), each Red then Green.

## Review

### What was verified

- `cd scripts/agent && npm test`: 994 tests, 994 pass, 0 fail (925 before the
  port; 1001 after the review fixes below, 1015 after the code review). This includes the structural workflow tests (`checks.test.mjs`,
  `carry-wiring`, `infra-wiring`, `evidence-wiring`) and `fingerprint.test.mjs`,
  which runs the workflow's exact `patch-id --verbatim` line on real
  repositories.
- `node --test 'scripts/test/**/*.test.mjs'`: 179 pass, 0 fail.
- `rhysd/actionlint:1.7.12` with the flags `agent-scripts.yml` uses: clean.
- `node scripts/verify-doc-links.mjs` and `node scripts/verify-doc-index.mjs`:
  pass.
- Workflow divergence. For each of the four workflows, the set of lines that
  differ between js-sdk and yorkie was compared before (`898cd697^` vs
  `origin/main`) and after (`bc591151` vs this branch). The ONLY new divergence
  is the D2 fixer-prompt rule, in `agent-fix.yml` and `agent-review-panel.yml`.
  `agent-rerun.yml` and `agent-review-on-demand.yml` diverge exactly as before.
- The probe step sits after "Stage the trusted agent scripts" and before
  "Generate the agent's narrow token" and the branch checkout, as upstream.
  The fix job reads the SDK artifact the `deps` job already builds. The
  fixer's model (`claude-opus-5`) is the one the probe is told to use.
- Scripts. Every ported module and test is byte-identical to js-sdk's
  `bc591151` except `test-removals.mjs`, `test-removals.test.mjs` and
  `evidence-wiring.test.mjs` (the Go adaptation), plus the files that already
  differed before the port.
- The Go detector was run over yorkie's last 400 non-merge commits from local
  history. 24 commits report something, and a sample checked by hand is right:
  deleted test files (`443d03694`, `923bd98d0`), `func Test…` removed by
  refactors, a conditional `t.Skip` in a table test (`bdc4060cc`), and a bench
  file whose `//go:build bench` became `linux && bench` (`7d84d17ed`).

### Adaptations and differences from js-sdk

- **`test-removals.mjs` reads Go.** `countCases(patch, { file, status })` picks
  the rules by extension. For `.go` (as first ported; "Review fixes" widens it):
  - cases are top-level `func TestXxx(t *testing.T)` / `func FuzzXxx(f
    *testing.F)` (the `go test` naming rule; `TestMain` and helpers excluded by
    the parameter type) and `x.Run(…, func(t *testing.T)` subtests;
  - `t.Skip`/`t.Skipf`/`t.SkipNow` count as switched off, apart from cases and
    netted only against removed skips (like a disabled suite upstream), because
    a skip is a new line inside an unchanged function;
  - a new or changed `//go:build` expression in an existing test file counts as
    the file switched off; a new file's constraint does not;
  - `isRunnableTest` also recognizes `_test.go`, so a Go test renamed out of the
    runner's reach is a deletion.
  - Not detected: rows removed from a table-driven test, and benchmarks (not run
    by `go test`).
  The record schema is unchanged: Go skips and constraints land in `suitesOff`,
  and the rendered line reads "test(s) or suite(s) switched off" instead of
  "suite(s) switched off". `fix-report.mjs` and `rebuttal.mjs` are untouched.
- **The D2 fixer rule uses `t.Skip`, not `it.fails`.** Go has no expected-
  failure marker. The fixer keeps the test, makes
  `t.Skip("still reproduces: <finding>")` its first statement with a comment
  naming the finding, and reports the item `--skipped`. Unlike upstream's
  `it.fails`, the skip IS recorded as a disablement. That is intended: beside a
  `--skipped` claim it is consistent, and beside a `--fixed` claim it is the
  contradiction the adjudicator should see. Leaving the test failing was
  rejected: a red test hands the PR to the CI-fix arm, which can make CI pass by
  deleting it. The rule's heading is "NEVER DELETE A TEST THAT SHOWS A FINDING
  STILL REPRODUCES" (upstream: "DELETE OR DISABLE"), since the Go form disables
  it on purpose.
- **Design doc.** yorkie has no `agent-harness.md`; the convergence section is
  §6 of `docs/design/agent-command-verbs.md`, with its risk, decision and
  alternative rows. It adds a row for the `t.Skip` choice and one for the skip
  being reported.
- **Maintainer-merge skill.** The caution is a new "Agent-managed PRs" section.
  It names rebases as well as merges, because yorkie's skill tells the
  maintainer to rebase a `BEHIND` branch, and qualifies the incident as
  yorkie-js-sdk#1426.
- **No `scripts/agent/README.md`.** yorkie indexes `scripts/agent/` as one row
  in `scripts/README.md`, not per module, so nothing was added there.
- **Issue numbers in ported comments** (`#1426`, `#1406`, `#876`) are
  yorkie-js-sdk's and were left as they are, as earlier ports did, so the shared
  modules stay byte-identical for the next sync. The design doc says so.

### Review fixes

An independent review found nothing blocking. Each fix below got a test that
failed first, then passed.

- **Shared Go suite bodies (major).** `server/backend/database/testcases/` and
  `server/rpc/testcases/testcases.go` hold the bodies of the database and RPC
  suites in non-`_test.go` files, so a `t.Skip` or a deleted `t.Run` there
  recorded nothing. A `.go` file under a `testcases/` directory is now a test
  file, and in it a top-level `func RunXxx(` counts as a case: gofmt puts its
  `t *testing.T` on the next line. The rule reads the PATH, not the patch:
  whether a signature shows in a hunk's context depends on the diff's context
  width, and the record must not change with it.
- **Switch-offs net across the round.** The per-commit clamp reported a skip
  added in one commit and removed in a later one, and D2 now tells the fixer to
  write skips. The tally is kept signed per commit (`offNet`, plus the
  build-constraint expression sets), summed or unioned per file across the
  round, and clamped once. Cases are still summed, so a test committed then
  deleted inside one round is still flagged. This applies to JS
  `describe.skip` too.
- **Go false negatives.**
  - A `_test.go` renamed into `testdata/`, to a `_`/`.`-prefixed path
    element, or to a `_GOOS`/`_GOARCH` suffix other than CI's linux/amd64 is a
    deletion. As in go/build, the first name element is never a constraint.
  - A new file born with `//go:build ignore` or `// +build ignore` counts as
    switched off. An ordinary constraint on a new file is still exempt.
  - Any receiver's `.Skip(`/`.Skipf(`/`.SkipNow(` counts (`b`, `tb`, `s.T()`).
- **Infra page needs both heads read.** "Read the branch head after the fix"
  swallows a failed `gh api` read (`|| echo ''`) and still succeeds with
  `advanced=false`, so `steps.after.outcome == 'success'` did not prove
  "nothing was pushed". The infra page now also requires a non-empty
  `steps.after.outputs.sha` and `needs.fix.outputs.before`, and
  `infra-wiring.test.mjs` pins it. **yorkie-js-sdk has the same bug, inherited
  from #1428, and needs the same fix.**
- **Documented limit, not changed.** The D2 skip record is never shown beside
  its own `--skipped` claim: skipped claims are upheld without an adjudication
  session. It reaches the adjudicator only through the same round's `--fixed`
  claims and disputes, and a maintainer reads it on the PR. Recorded in §6.
- After the fixes: `scripts/agent` 1001 of 1001 pass, `scripts/test` 179 of 179
  pass. Over the last 400 commits, 26 now report something (24 before), the
  difference coming from the `testcases/` files.

- **The fingerprint test wrote to the real repo under a git hook.** On the
  wafflebase port, the first `git push` ran `fingerprint.test.mjs` under the
  pre-push hook with `GIT_DIR` exported. The test's fixture commands set
  `core.bare` and a `t@t` identity in the shared config and committed over the
  working branch. yorkie's copy is the same file, and with an inherited
  `GIT_DIR` it wrote `user.email=t@t` into a decoy repo (Red). It now uses
  `fixtureGitEnv`, and `git-env.test.mjs` fails on any test that spawns git and
  runs `init` without `git-env.mjs`. yorkie's pre-push hook does not run
  `scripts/agent`, which is why this push was safe. **yorkie-js-sdk's copy
  needs the same fix.**
- After this fix: `scripts/agent` 1002 of 1002 pass.

### Code review (/code-review high)

`/code-review high` raised six findings, unverified. Each was checked against
the code; the real ones got a test that failed first, then passed.

- **1. Untrusted reruns in `reviewRequested` (partly confirmed).** The premise
  that GitHub answers 404 for a non-collaborator is wrong for a public repo:
  `collaborators/{login}/permission` returns 200 with `read` for an outside
  account (checked live), which is already `false`. A 404 comes only for a
  login that is not a user. The real defect stood: any `null` (a failed lookup)
  was believed, and only the latest believed rerun counted, so an unresolved
  plain `@claude rerun` could withdraw a maintainer's `@claude rerun review`.
  Fixed: the latest TRUSTED rerun decides; a `null` may force a review with
  `rerun review` but its plain `rerun` is skipped, so it can never cancel one.
  A 404 is now a definite "no": `notFoundIsNoAccess` wraps the `gh` caller in
  `review-scope.mjs` (from `gh`'s `(HTTP 404)` on stderr), so `gh-checks.mjs`
  stays as it is. Docstring rewritten to match.
- **7. A permission call per historical rerun (confirmed).** Fixed with 1:
  bots, non-commands and reruns older than `after` are dropped first, then
  trust is resolved newest first and the walk stops at the first rerun that
  decides. The test counts the lookups.
- **2. Carry does not survive a rebase (confirmed).** `decideScope` read lens
  states only from `pulls/{pr}/commits`; after a rebase the approved head is
  not listed, so the result was `no-prior-state` → full (Red test). Chose (a):
  the REST timeline's `head_ref_force_pushed` event carries only the new head,
  but GraphQL's `HeadRefForcePushedEvent.beforeCommit` names the replaced one,
  and check runs on a replaced SHA stay readable (both checked live on
  yorkie#2085). `replacedHeads` reads the last 10 force-pushes; their runs are
  merged in only to decide a carry, and only when a replaced head holds the
  NEWEST verdicts, so an older approval cannot outvote a newer verdict on the
  branch. The carry still compares that head's recorded `fp` with this head's,
  still needs every lens's approval, and the carry cap and `rerun review` still
  apply. A replaced head that does not carry falls back to the branch's own
  state, exactly as before (an amend still narrows from an older on-branch
  pointer). An unreadable timeline or replaced head costs only the carry. The
  pusher is irrelevant: promote still needs green CI on the new head. Design
  §6 says how the replaced head is found.
- **6. Last removal record wins (confirmed).** A second record for the same
  head dropped files only the first had, and an empty `rewritten` record
  replaced a real one with `[]`. `readFixReports` now unions every record for
  the head per file: each count is the max, `deleted` and `unreadable` stay set
  once set. The record has no `truncated` field; `rewritten` is per record and
  was never joined onto a report.
- **4. A drained pool still ran the setup (confirmed in
  `agent-review-panel.yml`; not applicable to `agent-fix.yml`, which has no
  probe).** The agent token, the branch checkout, `setup-go`, the linter
  install, "Set state → fixing" and "Record branch head before fix" now carry
  `steps.cred.outputs.available != 'false'`. The no-credential page needs only
  the scripts staged before the probe, `GITHUB_TOKEN` and the workflow-level
  `GH_REPO`; `fix-report` is already skipped on this path, so nothing reads the
  skipped `before`. `infra-wiring.test.mjs` pins every step between the probe
  and the page. `pick-fix-credential.test.mjs` counted exactly two gated steps;
  it now checks the dispatch record and the fixer by name and allows more.
- **5. AFTER blames later pushes on the round (confirmed).** Both report jobs
  read the live branch ref after the fixer. update-branch merges were already
  excluded by `roundCommits` (merge commits, and main's commits are not in the
  PR's list), but a human's commit was not. Neither suggested alternative is
  sound: a commit's author and committer are whatever the fixer's shell sets,
  so filtering on the bot identity lets a fixer commit as someone else and
  drop out of its own record. GitHub's activity log records WHO PUSHED, which
  the fixer cannot choose (it holds only the App token). `fixerHead` follows
  the push chain from BEFORE, newer than a start time the fix job records in
  "Record branch head before fix" (before the agent), and stops at the first
  push the App did not make. When the chain cannot be proven (no App push
  first, a fork branch, an unreadable log, a clock skew), the live head is used
  as before. The App's login is `app-slug` of the report job's own token. The
  activity endpoint and its `ref` filter were checked live on a js-sdk agent
  branch.

**yorkie-js-sdk needs the same fixes.** Its `review-scope.mjs` and
`fix-report.mjs` are byte-identical to the old copies here (1, 7, 2, 6), its
panel has the same probe placement (4), and its `test-removals.mjs` and both
report jobs read AFTER the same way (5; its `test-removals.mjs` is the JS-only
original, so `fixerHead` and the CLI flags port by hand).

- After these fixes: `scripts/agent` 1015 of 1015 pass, `scripts/test` 179 of
  179 pass, `rhysd/actionlint:1.7.12` clean, doc gates pass.

### Not verified

- Nothing ran on GitHub. No real yorkie PR has gone through carry, reuse, the
  probe, an infra page or a removal record. The structural tests pin step order
  and conditions; the end-to-end wiring is unproven here, as it was upstream at
  merge time.
- None of the code review fixes ran on GitHub either. The GraphQL force-push
  query, check runs on a replaced head, the activity log and its `ref` filter
  were each called live with `gh`; the workflow wiring is pinned structurally.
- The probe was not run against a real credential here. The module is
  byte-identical to the one upstream checked with a bogus token.
- The Go detector was checked against local `git show` patches, not GitHub's
  commit API `patch` fields. They are the same unified-diff format with more
  context.
