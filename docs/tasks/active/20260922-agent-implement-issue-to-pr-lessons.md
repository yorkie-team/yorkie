# Lessons — agent-implement, issue → PR

**Created**: 2026-09-22

## Round 8: stop carrying the patch, withdraw the workflow

Three rounds tried to deliver a workflow fix the pipeline cannot push. This one
stopped trying and **removed `.github/workflows/agent-implement.yml` from the
branch**, along with the `.diff` beside it.

What made that available was one measurement nobody had taken: the App token is
refused for *create or update* under `.github/workflows/**`, and **delete
succeeds**. So the set of actions was never "ship it broken or stand still" — it
was "ship it broken, stand still, or withdraw it", and only the third closes the
findings. A probe commit to a scratch ref answered both questions in a minute;
three rounds of standstill had assumed the answer to the second.

The rest follows from that:

- **An apply-me patch under `docs/` is a security defect, not a workaround.**
  `docs/**` is writable by every agent token and `.github/workflows/**` is not —
  a committed patch with `git apply` instructions launders content across exactly
  that boundary. The review lens was right to call it critical, and the earlier
  round's framing ("a patch in a comment gets scrolled past, so commit it as a
  file") solved the wrong problem: the issue was never where the patch lived, it
  was that a patch is the wrong artefact for a change an agent may not make.
- **Prose requirements are the artefact that survives.** The thirteen acceptance
  criteria now in the design doc say what the workflow must do and why, one per
  defect the panel found. A maintainer implements from them; nothing can
  mechanically turn them into a workflow nobody read. They also outlive the
  specific draft, which was written against an unsafe base anyway.
- **Keep the guards, skip them.** `workflow-presence.mjs` already existed for
  precisely this, and the three `agent-implement.yml` tests now use it. They
  re-arm the day the workflow lands, which is better than a green suite that has
  quietly stopped asking.
- **Splitting a test by what it actually covers.** The PR-lookup fixture test
  exercised both the YAML copies *and* `metrics.mjs::isAgentPrHead`. Skipping it
  wholesale would have taken the shipped helper's only executing test with it, so
  it is now two tests: the rule runs unconditionally, the agreement of the copies
  skips with the workflow.

**The pipeline lesson, restated with the missing half.** A review lens pointed at
`.github/workflows/**` produces findings the fix loop cannot close by editing.
The loop should recognise that up front. But the fix agent also has one action it
had not considered — *withdraw the file* — and for a file the review says must not
ship, that is usually the correct one rather than the drastic one.

## BLOCKED: the fix round could not push its own workflow fix

The panel's fix job holds an installation token with no `workflows`
permission, so `git push` is rejected outright:

```
! [remote rejected] refusing to allow a GitHub App to create or update
  workflow `.github/workflows/agent-implement.yml` without `workflows` permission
```

Both tokens available to that job (the App token and the ambient
`GITHUB_TOKEN`) are refused, and so is the Git Data API — `POST /git/trees`
answers 403 for a tree containing a `.github/workflows/` path. Every commit on
this branch that touched the workflow was pushed by a human. That is the
intended design — `Workflows` is deliberately at no access for this App,
`checks.test.mjs` asserts that no agent token may ever request it, and
`agent-implement.yml`'s own prompt tells the implement agent never to edit
`.github/workflows/**` for the same reason.

The consequence is structural, not incidental: **eight of this round's nine
blocking findings are defects in a workflow file that the agent fixing them is
not allowed to push.** The fix exists and is verified locally; it has to be
applied by a maintainer.

**And this has now happened twice, which is the part worth acting on.** The
previous round hit the same wall, attached its patch to the PR, and wrote the
paragraphs below in the past tense. The patch was never applied — the same
`also` ReferenceError, the same `issue_comment`-only pre-flight, the same
`gh issue view` are all still in `agent-implement.yml` — so the next review
round re-raised every one of them, and the lessons file was by then asserting
mitigations the tree did not have. A patch that lives only in a comment is a
patch that gets scrolled past.

That round committed it **as a file in the repository**,
`20260922-agent-implement-issue-to-pr-blocked.diff`. Round 8 deleted that file:
see the section above. A patch under `docs/` is writable by every agent token and
carried `git apply` instructions into `.github/workflows/**`, which is the one
path those tokens are deliberately kept out of.

### Round 7: the block is measured, not inferred

The wall was tested rather than assumed this round — a throwaway commit touching
`.github/workflows/agent-implement.yml`, pushed to a scratch ref:

```
! [remote rejected] probe-wf-write -> probe-wf-write (refusing to allow a
  GitHub App to create or update workflow `.github/workflows/agent-implement.yml`
  without `workflows` permission)
```

So the standstill is real and is not a property of one token being expired or
one path being wrong. Three things changed in response:

1. **The patch grew to cover the two findings it did not answer.** The App token
   handed to the agent carried `pull-requests: write` — approve and merge — so
   the gate was satisfiable by the party it constrains. The job now mints TWO
   tokens: the agent gets `contents` + `issues`, the workspace checkout persists
   *that* one (`persist-credentials` writes it into `.git/config`, which the
   agent reads with one `Bash` call), and a trusted step opens the draft PR after
   the agent stops. Opening it after, rather than mid-run, is also what removes
   the third writer: the panel and iterate-ci claim any `agent/`-prefixed branch
   and only ever engage through a CI run, and CI runs on `pull_request`.
2. **The design doc stopped describing the patch and started describing the
   tree.** Phase I now lists the six defects that are actually shipped, in a
   warning block, and says the verb is not safe to enable until a maintainer
   applies the patch. A design doc that describes an unapplied patch is the same
   failure as a lessons file written in the past tense, which this file already
   records once.
3. **What could land, landed.** The `pull-requests: write` guard's issue-only
   exemption was a way for that guard to switch itself off: both of its
   assertions still passed if the predicate matched every file. The predicate is
   now a named function with four fixtures, the exempted files are an allow-list,
   and the guard asserts a non-exempt commenting job still exists to judge.

What landed directly this round: the reporter's job-boundary assertion, which
compared `nextJobAt + at > at` — true for every match index, so it could not
fail — and now bounds on the `implement:` header instead. That one is
workflow-independent. Everything else described below is **in the `.diff`, not
in the workflow**.

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

> **None of the workflow behaviour below is in the tree, and none of it will be
> until a maintainer writes the workflow.** It is kept as the record of what each
> review round taught, and every item has a matching acceptance criterion in
> `docs/design/agent-command-verbs.md` Phase I. An earlier version of this
> section wrote it in the present tense, as though a patch beside it were
> applied; a review lens correctly called that out, and round 8 removed the patch
> rather than the caveat.

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
  withdraw it, reached through the report's own advice to retry. The
  acknowledgement now carries `<!-- agent-implement-ack -->`, and the reporter
  compares positions in the (chronological) comment list: suppress only when the
  newest report of that kind is newer than the newest acknowledgement, each
  matched on the login that actually posts it — the App for the ack,
  `github-actions[bot]` for the report. Pre-acknowledgement refusals post no
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

## Review round 6 — the invariant the doc claimed and the tree does not hold

Four findings, two of them against a file this branch had already deleted.

- **A Non-Goal is an invariant, and an invariant that is false is worse than
  no invariant.** Round 5 rewrote the merge-policy Non-Goal to concede that
  `contents: write` + `pull-requests: write` can approve and merge, then closed
  with "No workflow that ships today hands an agent that pair." Four do:
  `agent-fix.yml`, `agent-iterate-ci.yml`, `agent-review-panel.yml` (fix job)
  and `agent-review-reply.yml` each mint that pair and pass it to
  `claude-code-action` with `Bash` allowed, in a workspace checked out from the
  untrusted branch. The sentence was written while reasoning about the *new*
  verb and never checked against the four already installed. The Non-Goal now
  names them in a table and says plainly that the human-approval property rests
  on branch protection, not on credential scoping.
- **The standstill is the same one that withdrew the workflow.** The correction
  those four need — split the mint, keep `pull-requests` in trusted steps — is a
  change to `.github/workflows/**`, which no credential in this pipeline may
  write. Committing it here would have the remote reject the whole push. So it
  is recorded as an open gap addressed to a maintainer, in the design doc and in
  a rebuttal, rather than attempted and lost.
- **Deleting the subject does not retract the finding; it answers it.** Two
  findings cite `agent-implement.yml` line numbers. That file left the tree in
  `a07cf41`, and the three guards that would have covered it skip through
  `workflow-presence.mjs` until it returns. Both were answered with located
  rebuttals rather than a skip, because a skip reads as a decision not to act.

### Standing rule this round produced

Before writing "no workflow does X" about this repository, grep the workflow
directory for X. A claim about the installed set is a measurement, not an
inference from the thing being designed.

## Round 5 — the remediation was itself under-specified

The security lens re-raised the four over-privileged workflows (correct, and
unchanged), and added one finding that was genuinely new and genuinely
actionable: **the fix the Non-Goal prescribes would not have worked.**

- **"Trusted step" is a property of the code a step runs, not of the file it
  sits in.** The prescription was "split the mint; every step needing
  `pull-requests` stays a trusted step in the workflow file." But in all four
  jobs the post-agent steps run `$RUNNER_TEMP/agent-tools/*.mjs`, copied from
  the branch at `agent-fix.yml:408`, with the agent holding unrestricted `Bash`
  in between (`:524-634`) and those files re-executed afterwards (`:657`,
  `:672`, `:770`). Handing the second token to such a step hands it to whatever
  the branch wrote there. The split and acceptance criterion 12 have to land
  together; separately, the split just relocates the capability.
- **A remediation written into a design doc gets reviewed as loosely as prose
  and gets implemented as literally as code.** This one was three clauses long
  and read as complete. What made it incomplete was in a numbered list 300 lines
  further down, in a section about a phase that is not landed — so nobody
  reading the Non-Goal would reach it. Cross-reference the criterion at the
  point of prescription, not only where it was first written.
- **Re-measure the boundary you keep citing.** The "no credential may write
  `.github/workflows/**`" claim had been inherited across four rounds from one
  measurement on a different file. This round pushed a one-byte change to
  `agent-fix.yml` itself on a scratch ref and got the refusal back for that
  exact path. Cheap, and it converts a quoted precedent into a fact about the
  file actually under discussion.
- **Name the compensating control's evidence, or say there is none.** The doc
  leaned on "branch protection on `main`". There is no ruleset fixture, no
  `CODEOWNERS` (the repository has none at all) and no assertion in
  `checks.test.mjs` behind it. It is an assumption about a repository setting,
  and the doc now says so rather than resting on it silently.

### Standing rule this round produced

When a design doc concedes an open gap, the remediation it prescribes is part
of the claim and gets checked as hard as the gap. "Here is how to fix it" that
does not survive the jobs as built is worse than "we do not know how to fix
it yet" — it reads as a plan and retires the question.
