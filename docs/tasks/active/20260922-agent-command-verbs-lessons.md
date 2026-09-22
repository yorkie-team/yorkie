# Lessons: installing the `@claude` command surface

**Created**: 2026-09-22

## The verbs are not the unit of adoption

The four commands this started from — `fix`, `loop`, `review`, `rerun` — read
like four independent features. Three of them are satellites of one review
panel: `loop` applies the label the panel's gate admits a PR on, `rerun` clears
a latch the panel sets, and `fix` is eligible only when the current head SHA
already carries the panel's lens check runs. Porting any of those without the
panel installs a no-op that looks installed.

`review` was the one separable verb, which is what made Phase 1 possible at all.

## The language barrier was not where it looked

A Go repository adopting a TypeScript monorepo's tooling sounds like the hard
part. It was not: `scripts/agent` is a standalone npm package with its own
lockfile that nothing in the Go build reaches, and this repository already runs
`node --test` in CI for the doc-link checker.

The advisory workflows carried exactly one repository-specific line between them
— a `git diff` pathspec excluding generated files. The gating half carried more,
because it runs a toolchain: the fixer jobs install Go and a linter instead of a
package manager, and the diagnosis the CI-fix arm feeds its agent had to be
rebuilt entirely, since it rendered an artifact written by a verify runner this
repository does not have.

What needed rewriting throughout was the knowledge, not the code: which paths
mean what, and what CI already proves.

## The coverage note is the dangerous file

`MECHANICAL_COVERAGE_NOTE` tells each lens what the mechanical lanes already
catch, so it does not spend turns re-deriving them. Its failure mode is silent
in one direction only: claim something is covered when it is not, and that whole
finding class stops being reported with nothing in the output to show for it.

Upstream's copy says formatting is unchecked (Prettier is write-only there).
Here `.golangci.yml` enables `gofmt` and `goimports` as formatters, so that
bullet is not merely stale — it is backwards. In the other direction, the same
file *disables* `staticcheck` and `unused`, so a bare "golangci-lint runs" would
have implied both. Two claims, two opposite errors, from one file translated
instead of re-read.

The test that pins each claim was rewritten alongside it, which is the only
thing that keeps the note honest as the lanes move.

## Skip a guard, do not delete it

Four ported tests assert that a module and a workflow carry the same literal —
the paged-latch marker, the inline finding-selection copy, the freeze-point
wiring, the upload steps that would silently upload nothing without
`include-hidden-files`. Their workflows arrive in later phases.

Deleting them would have been quieter and worse: every one of those guards
exists because the drift it catches is invisible, so removing them makes Phase 2
a silent regression of exactly the checks written to prevent one. They skip
through a single helper that names the reason, and re-arm the day the workflow
lands.

The same reasoning kept `rounds.mjs` and `fix-report.mjs` in the tree even
though no Phase 1 code path reaches them: `review-panel.mjs` imports both
directly, and surgery inside a ported 3,400-line module is what makes the next
sync from upstream expensive.

## An inherited claim that could not be checked here

Upstream mints a GitHub App token to post its comments, stating that a
fork-PR-triggered run's `GITHUB_TOKEN` is forced read-only. An `issue_comment`
run executes in the base repository with the permissions its job declares, so
`issues: write` should be enough — but "should" is not a test, and there was no
fork PR to try it on.

Rather than pick a side, the placeholder step is `continue-on-error` and the
publish step already creates a fresh comment when no placeholder id reaches it.
A wrong guess costs the "running…" note, not the review. Worth remembering as a
shape: when a ported claim cannot be verified in the new repository, find the
arrangement where being wrong is cheap instead of arguing about it.

## `for m in $VAR` does not split in zsh

Two copy loops silently did nothing, and one of them reported "no tests exist"
for files that were right there. Cost: a wrong conclusion about the upstream
tree that survived two follow-up commands before `ls` contradicted it. In zsh,
word splitting on unquoted parameters is off by default — use an explicit list
or an array.


## Removing a step is not the same as removing its setup

Swapping the fixer jobs from pnpm to Go meant deleting the pnpm and Node setup
steps. The script that did it matched on `setup-node` and removed all four in
the panel — but only one belonged to the fixer. The other three served the job
that runs `npm ci`, the job that runs the panel, and the job that runs the
promotion gate. Nothing failed locally, because none of it runs locally; the
suite stayed green and the YAML stayed valid.

Two rounds of review caught the rest of the same mistake, in the `fix` and
`stalled` jobs. The lesson is not "be careful with sed" — it is that a
mechanical edit across a 2,500-line workflow needs a mechanical check of the
property it could break. `grep -L setup-node` over every workflow that runs
`node` would have found all five in one command, and now does.

## The dependency you did not port is still a dependency

`agent-iterate-ci.yml` was excluded as out of scope: it is a separate component,
and the phase only promised the panel, the guard, the latch and the label. But
the panel's `promote` and `fix` jobs both require a green CI, and its `stalled`
job deliberately excludes a red one — all three commented with "the CI arm owns
that branch."

Leaving it out did not remove a feature. It removed the arm three other jobs
delegate to, and turned the single most common failure event — CI going red —
into the silent stall the whole design exists to prevent. Scope decisions about
a component graph have to be checked against what the components say about each
other, not only against what each one does.


## Every serious defect was a silent stall, and none of them failed a test

Two review rounds produced thirty findings across ~5,600 lines of ported
workflow. The ones worth the rounds all had the same shape: a path where the
pipeline stops and tells nobody.

- Red CI, because the arm three jobs delegate to was left out of scope.
- The panel hitting its own 45-minute wall, because GitHub reports a job wall as
  `cancelled` and the pager only listened for `failure` — while the check-run
  closer stamped the round "superseded by a newer commit", which was false and
  also refunded the round.
- A CI run concluding `cancelled` or `timed_out`, which is neither the `success`
  the promoter wants nor the `failure` the CI arm owns.
- Two throttle markers written before the work and never cleared, so one
  usage-limit hit or one declined environment approval swallowed every later
  request on that commit.
- The only pager on the no-credential path marked `continue-on-error`, which is
  the "a net that dies with the thing it catches" failure the same file names
  elsewhere.

None of these fails a test, a lint, or a YAML parse. They are all reachable only
by asking "what happens when this does not finish?" of every job, and the answer
is only visible in the interaction between a job's `timeout-minutes`, what
GitHub reports for it, and which clause of the pager's `if:` lists that word.

## The port inherited guarantees it did not inherit the mechanism for

`agent-fix.yml` prints, to the user, that the App token is minted without
`workflows: write` "so a fix agent can never rewrite the lanes that grade it".
That sentence is true of the four workflows that enumerate `permission-*` and
false of the two that were ported without them — the CI arm and the reply arm,
both of which check out untrusted branch code and run an agent beside the token.

A guarantee stated in one file and implemented in five is a guarantee only if
something checks the sixth. Worth a test next time the permission list changes.

## Fixing the guard is part of fixing the bug

The Node-pinning regression was fixed twice. The first fix asked "does this job
have a `setup-node`?" — and two jobs had one while still running scripts before
it, or under the opposite branch of an `if`. The property was never presence; it
was that the pin comes first and is unconditional.

A guard written from the symptom passes as soon as the symptom is gone. Writing
it from the property is what makes the next instance fail instead of the next
reviewer.


## The reviews found my fixes, not just my port

By round three the findings stopped being "you translated this wrong" and became
"the thing you added last round is wrong". Three fixes were themselves defects:
a toolchain swap that deleted every `setup-node` in a 2,600-line workflow, a
guard added to `rerun` "for symmetry" that dead-ended the one verb whose job is
to rescue a stuck PR, and a CI gate that asked "is CI red?" — so a run still
queued answered "clear", which is precisely the window the gate existed to
cover.

The pattern in all three: the fix was written from the symptom rather than from
the property. Presence of a `setup-node` instead of "the pin comes first";
symmetry between two branches instead of "what does the latch make true here";
the absence of red instead of "is it known green". Each time, the version
written from the property is the one a later round could not break.

## Two reviewers disagree, and that is the argument for both

CodeRabbit read the same diff and found two things four `/code-review` rounds
had not — including a promotion gate that accepted the sentence "This PR was NOT
authored autonomously with Claude". The lens panel found things CodeRabbit did
not: forged latches, a fork PR that could push to the base repo, a credential
pool materialised on an ineligible comment.

That is the Phase 1 exit criterion arriving early and unasked-for. The two
reviewers are not redundant, and the comparison the design document schedules
for twenty PRs already has its first data point.

## The line between fixing and recording

Roughly thirty findings landed in vendored code this repository did not write.
Fixing all of them would have made the next sync from upstream unaffordable;
fixing none of them would have shipped a known-exploitable pipeline.

The line drawn was: **a trust boundary gets fixed, a scoring defect gets
recorded.** Anything a third party can reach from outside — a forged paged
latch, an unauthenticated PR freeze, a token carrying more scope than its job
needs — was closed here, because those fire the day the surface is enabled and
no maintainer can be expected to audit for them. Provenance and demotion bugs
that make the panel score badly were written down instead: they degrade a review
rather than breach one, and they belong upstream where the rest of that logic
lives.

## The switch had two credential regimes, and only one was designed

`AGENT_PIPELINE_ENABLED` was built as *the* switch: unset, nothing runs. That
is true and it was tested. What no round asked was what the repository looks
like *between* the two states — the switch on, the App not yet created. It is
not a hypothetical window; it is the only order the setup can happen in, since
enabling the advisory verbs (`review`, `summarize`, which post with
`GITHUB_TOKEN` and need no App) is what makes it worth creating the App at all.

In that window every App-backed verb went straight to the token mint, which
fails the *step*, not the job. So the designed behaviour — "off means inert" —
became "on before the App exists means a red X on a contributor's PR with a
token error in it", and for the panel it was worse: a failed `promote` job made
`stalled` page a human and write `agent:blocked` on a clean PR. An absent
credential reported as the loop giving up.

The general form: a feature flag with one name and two underlying capabilities
has a partial state, and the partial state is not optional — it is the
migration path. Design the flag for the intermediate configuration, not just
for on and off.

## A trust list can be wrong in a way that reads as a name

The ported modules trusted `yorkie-agent[bot]`, and five review rounds plus
CodeRabbit read past it. It looks like configuration; it is actually the whole
authority model. That App belongs to the **wafflebase** organization — the
pipeline this code was vendored from — so this repository was configured to let
another organization's bot write paged latches, review ledgers and state
comments that gate merges here.

Nothing about the string announces that. The author gates added in rounds four
and five were reasoned about carefully, in the abstract ("trust is the comment
author, never the payload"), while the concrete list of who that author *is*
travelled in from outside unexamined. When vendoring, the values inside a
security check deserve the same read as the check — a correct predicate over a
foreign allow-list is not a control.

## A late guard gates the steps you were looking at

CodeRabbit's three findings this round were one defect in three places: the
App-presence check asked the right question and then stopped short of the thing
it protected. Most instructive was `agent-iterate-ci`, where the mint and the
checkout were gated and the *attempts guard* was not — and the attempts guard
is the step that decides everything, so the arm that had just announced it was
standing down could still write the terminal paged latch and `agent:blocked` on
a PR.

The existing test did not catch it, and the reason is the frame: it asserted
that every consumer of `steps.app-token.outputs.token` is conditional. Follow
the token, find the steps. But the attempts guard consumes no token — it
consumes nothing and *produces* the decision. A guard's blast radius is not the
set of steps that use the credential; it is the set of steps whose outcome
changes. Those overlap, and the difference is exactly where this hid.

Its half-registered sibling has the same shape one level down: the check read
`AGENT_APP_ID` and not `AGENT_APP_PRIVATE_KEY`, so the guard was answering "was
this configured?" with a test for "was the first of two secrets set?" — and the
key is the half that gets rotated.
