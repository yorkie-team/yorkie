# Lessons — agent-implement, issue → PR

**Created**: 2026-09-22

## BLOCKED: the fix round cannot push its own workflow fix

The panel's fix job holds an installation token with no `workflows`
permission, so `git push` is rejected outright:

```
! [remote rejected] refusing to allow a GitHub App to create or update
  workflow `.github/workflows/agent-implement.yml` without `workflows` permission
```

Both tokens available to that job (the App token and the ambient
`GITHUB_TOKEN`) are refused, and every commit on this branch that touched the
workflow was pushed by a human. That is the intended design — `Workflows` is
deliberately at no access for this App, and `agent-implement.yml`'s own prompt
tells the implement agent never to edit `.github/workflows/**` for the same
reason.

The consequence is structural, not incidental: **six of this round's eight
blocking findings are defects in a workflow file that the agent fixing them is
not allowed to push.** The fix exists and is verified locally; it has to be
applied by a maintainer. It is attached to the PR as a patch, and each blocked
finding has a rebuttal record so the standstill pages a human rather than
looking like a round that quietly did nothing.

What landed here: the `metrics.mjs` same-repo fix and the shared
`isAgentPrHead` rule, the test that runs the workflow's two inline lookups
against it, and the anti-short-circuit allow-list. All three are
workflow-independent.

**The lesson for the pipeline, not for this PR:** a review lens pointed at
`.github/workflows/**` produces findings the fix loop cannot close, so every
round on a workflow-touching PR ends in a standstill by construction. Either
the fix job needs a credential that can write workflows (which hands an agent
the ability to rewrite its own gates — almost certainly the wrong trade), or
the loop needs to recognise workflow-file findings up front and route them to a
human instead of spending a fix round discovering it cannot push.

## Review round: blast-radius / test-adequacy / design-fit / security / correctness

Five lenses, eight blocking findings. What they had in common is worth writing
down: every one of them was in code that no test executed. The workflow's
safety-critical logic lives in `github-script` blocks, and the guards written
for it were all *text* matches — so the branch-protection gate shipped with
`also` referenced and never declared, which means three of its four refusal
paths threw a `ReferenceError` instead of printing a diagnostic, and five
review rounds' worth of assertions about that step never once ran it.

The fix that mattered most is not any individual patch: it is that
`checks.test.mjs` now **extracts the gate out of the YAML and runs it** against
fourteen repository states. A mutation reinstating the `also` bug fails that
test. The technique was already in this file (the CI re-run selection is checked
the same way) and simply had not been applied to the newer step.

### What each finding actually taught

- **A guard written three times is a guard with three answers.** The
  `agent/<issue>-*` lookup was inline in the workflow twice with a same-repo
  filter, and a third time in `metrics.mjs::resolvePrByIssue` with none — and
  that third copy is the one this job shells out to, so the effort record could
  land on an outside contributor's fork PR. `isAgentPrHead` is now the one rule,
  and a fixture table runs all three copies through it.
- **`sameRepo` is a required parameter, not an option with a default.** A
  default of `true` would have reintroduced the defect at the first new caller.
- **An entry point that skips a guard is the entry point that needs it.**
  The collision pre-flight was gated on `issue_comment` while `workflow_dispatch`
  reached the same checkout, the same branch name and the same `git push` — and
  dispatch is the path with no concurrency group and no thread to complain on.
- **A gate that the gated party can satisfy is not a gate.** The App token must
  carry `pull-requests: write` to open a PR, and that is also the scope that
  submits an approving review. "main requires one approval" was therefore
  circular. `require_last_push_approval` is now part of the gate; the residual
  (the same token can approve and merge *somebody else's* PR) is recorded in the
  design doc as accepted rather than described as covered.
- **"Fetched in a trusted step" is not the same as "fetched at a trusted
  time."** `gh issue view` ran minutes after the verb was typed, behind a token
  mint, three API calls, a full checkout and `npm ci` — plenty of room for the
  author to swap the text the maintainer read. The title and body now come from
  the event payload, which *is* that snapshot. Dispatch has no payload issue and
  still asks the API; there the dispatcher is the reader.
- **Dedupe on presence re-created the silence it was written to end.** The
  "On it" acknowledgement is posted on every run and is never deduplicated, so a
  second failure of the same kind found the first report still on the page and
  returned — leaving a fresh claim that a run is in progress with nothing to
  withdraw it, reached through the report's own advice to retry. The reporter now
  compares `created_at`: suppress only when the newest report of that kind is
  newer than the newest acknowledgement. Pre-acknowledgement refusals post no
  "On it", so "three maintainers, one comment" still holds for them.
- **An assertion's window is part of the assertion.** The anti-short-circuit
  check sliced the reporter script at `const MARKER_FOR` and scanned what came
  before — six `const` declarations and not a single `if`. It could not fail. It
  is now an allow-list of the two sanctioned early returns over the whole
  script, pinned in both directions.

### Standing rule this round produced

If a workflow step contains more than a few lines of `github-script`, the test
for it extracts and executes it. A text match on that code asserts that somebody
typed the right characters, not that the code does anything.
