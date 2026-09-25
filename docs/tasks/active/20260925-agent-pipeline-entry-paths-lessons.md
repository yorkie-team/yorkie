# Lessons — agent pipeline entry paths

**Created**: 2026-09-25

## Claims in the brief that did not survive checking

- **"Three workflows read `agent:candidate`."** Two do:
  `agent-review-panel.yml` and `agent-review-on-demand.yml`, with the same
  `labelled && human` rule. `agent-implement.yml` only writes it.
  `set-state.mjs` and `mark-ready.mjs` mention it to *preserve* it through
  lifecycle relabelling, which is not reading its meaning. The consequence
  the brief describes is nonetheless real and worse than stated: the on-demand
  `@claude review` path has the same hole, and that is the verb a fork
  contributor is told to use.
- **"`make verify-license` announces SKIPPED when node is absent — check
  whether node is present."** It is. All three fixer jobs install Node 22.x as
  an ungated first step, for an unrelated reason (the pre-agent gate scripts
  are Node and must not run on whatever the runner image ships). So the licence
  gate really runs in the autonomous arm after the §4c switch; it does not
  quietly skip.

## Verified before acting

- **The `agent-fix.yml` carve-out is dead code.** Not by reading the comment —
  by following the two conditions. The step is `if: steps.eligible.outputs
  .eligible == 'true'`, and `fix-eligible.mjs::decideEligibility` returns
  `eligible: false` on `total === 0`. Lens check runs come only from the panel
  (the script's own comment says `agent-review-on-demand.yml` holds
  `checks: read`, never write), and the panel runs only from a CI
  `workflow_run`. So "no CI run" and "eligible" cannot both hold through that
  route.
- **`dorny/paths-filter` semantics, read from the action's source rather than
  assumed.** `isMatch` is `patterns.every(...)` under
  `predicate-quantifier: every` and `patterns.some(...)` by default; the filter
  is true if ANY changed file matches. `MatchOptions = { dot: true }`, so `**`
  covers `.gitignore`. Under the default quantifier, `['**', '!**/*.md']` is
  true for a `.md` file (it matches `**`) — the exact inversion that would
  have silently skipped CI on every PR. The quantifier is a per-STEP input, so
  the new filter is a second `dorny/paths-filter` step; putting `every` on the
  existing one would have broken `bench`, `complex-test` and `load-test`,
  whose lists are alternatives.

## Breaking each gate, and what its guard said

Recorded because a guard that does not fail when the thing it guards is wrong
is worse than no guard.

| Change | Broken how | Guard that failed |
| --- | --- | --- |
| `ci.yml` pull_request filter | re-added `paths-ignore` at workflow level | `ci.yml creates a CI run for every PR, so a docs-only PR reaches the panel` — "ci.yml must not filter `pull_request` at the workflow level" |
| `ci.yml` build filter | changed `'**'` to a positive list | same test — "the build filter must be `**` plus negations only" |
| `agent-fix.yml` ci-clear | restored `setOutput('clear', 'true')` on the no-run branch | `the on-demand fixer refuses when CI's state for the head is unknown` |
| fix wall | set `timeout-minutes: 90` back on the panel's `fix` | `the no-commit page fires on a timed-out fixer…` — page says 55, wall is 90; and the new under-60 assertion |
| fix wall, the other half | set only `agent-fix.yml` back to 90 | same test — "agent-fix.yml's fix wall must match the autonomous one" |
| `make verify` | left `make lint` in the panel prompt | `every fixer prompt runs the same verification target` |

## Patterns worth keeping

- **A dead carve-out is a decision waiting to be re-derived, not a line to
  delete.** The `no CI run → CLEAR` branch was written for a reason that was
  false, but the STATE still occurs (a deleted or expired run), and the right
  answer for that state is the opposite of the one the dead reason implied.
  Deleting the branch would have reached the safe answer by accident, through
  a `reduce` on an empty array throwing into the catch — correct behaviour
  with no comment and no test.
- **When a comment's argument is the thing that is wrong, replace the
  argument.** The 90-minute wall's comment says shortening "would refuse
  exactly the rounds the reasoning says to allow". The refutation is not a
  preference, it is arithmetic: those rounds hold a credential that is already
  dead, so they cannot push, so there was never anything there to refuse.
- **Moving a path filter from a workflow to a job changes who observes the
  run, not only what runs.** `workflow_run` consumers, required status checks
  and `mark-ready.mjs`'s `runs.length === 0` branch all read the existence of
  the run, and all three were written against the old mechanism.
