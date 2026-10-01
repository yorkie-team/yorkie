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

## Review

### What was verified

- `cd scripts/agent && npm test`: 994 tests, 994 pass, 0 fail (925 before the
  port). This includes the structural workflow tests (`checks.test.mjs`,
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
  the rules by extension. For `.go`:
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

### Not verified

- Nothing ran on GitHub. No real yorkie PR has gone through carry, reuse, the
  probe, an infra page or a removal record. The structural tests pin step order
  and conditions; the end-to-end wiring is unproven here, as it was upstream at
  merge time.
- The probe was not run against a real credential here. The module is
  byte-identical to the one upstream checked with a bogus token.
- The Go detector was checked against local `git show` patches, not GitHub's
  commit API `patch` fields. They are the same unified-diff format with more
  context.
