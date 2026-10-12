**Created**: 2026-10-05

# Keep the agent loop inside the PR's scope — lessons

## The pipeline already refused this demotion, for a good reason

`novelty.mjs`, `routeFinding`, the verifier schema and the adjudicator's
ground list all say the same thing: a finding on old code is not demoted for
being old, because the blast-radius lens exists to find defects a change
causes in code it did not touch (#583). The standstills that motivated this
task are the other half of that class — defects that exist without the change
— and the pipeline had no way to tell the two apart. So the demotion could not
be "anchor outside the diff"; it had to be "anchor outside the diff AND the
change did not cause it". Git can answer the first and not the second. The
second needs a model, and the adjudicator's own docblock says why a model must
never be handed scope as a free-floating argument. The compromise: git decides
eligibility, the model decides causation only inside that set, and must cite.

## The verifier could not ask the causation question

The verifier is kept away from the diff on purpose (it would inherit the
lens's misreadings). The revert test cannot be answered without the diff. So
the judge is a separate session, not a field on the verifier, and it gets the
whole PR diff from git — not `--diff-file`, which is the delta on an
incremental round.

## Carried findings would have undone the gate

`mergeCluster` keeps a cluster gating if any member gates, and carried
findings are never routed (their lines are stale). A fresh finding demoted
out of the diff would have been re-armed every round by its own carried twin —
the exact standstill. File-level anchoring is immune to line drift, and a
carried finding whose fresh twin was judged inherits that judgement.

## Two of the asks were already partly done

The bench lane got its 60-minute wall in #2108, after the two 6-hour runs.
What was left was the two other tag-gated lanes. And the `stalled` job cannot
be extended to catch a cancelled run: it is `!cancelled()` by design, so the
fix is an out-of-run sweep, not a new clause.

## Page, not strip

Stripping out-of-scope fixer commits from a trusted job looked like the more
autonomous answer. It needs a push path the trust model does not have yet, and
the in-scope half of a round often depends on the half it would strip, which
turns a scope violation into a red CI and hands the PR to a fixer that would
put the code back.

## Self review

- Round 1 (correctness/tests): the carried-twin inheritance first checked the
  carried finding's own anchor before the twin, so a twin judged out of the
  diff in a TOUCHED file was ignored (the carried copy is file-level only, so
  `unknown`). Fixed by checking the twin first; pinned by a test.
- The handoff-note guard in `rounds.test.mjs` caught a reworded no-credential
  page that dropped "The review panel will not run again on this PR".
