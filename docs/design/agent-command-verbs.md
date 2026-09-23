---
title: agent-command-verbs
target-version: 0.7.24
---

# Agent Command Verbs

## Problem

A pull request here gets two reviews: CodeRabbit, automatically, and a human.
There is no machine reviewer a maintainer can *invoke* — no way to ask for a
second opinion on one PR, no way to hand a mechanical finding back to an agent,
and no read-only summary for a reviewer opening a 40-file diff cold.

Wafflebase built that surface as an `@claude <verb>` command set on issue and
PR comments. Adopting it wholesale is not a reasonable first step: the four
workflows behind the verbs are ~4,500 lines of YAML, they orbit a review panel
that is another 2,578, and the shared script package is ~9,500 lines. Three of
the five verbs are inert without the panel, and one of them needs a GitHub App
this organization does not have.

This document decides **which verbs, in what order, and what each one costs**.
It does not design any of them — the implementations are upstream's, and the
work here is porting plus the three repo-specific pieces named in §4.

### Goals

- Fix the verb subset and the order it lands in.
- Make each phase independently useful, independently revertible, and gated on
  a precondition that is stated rather than discovered.
- Keep every phase behind one switch that defaults to off.

### Non-Goals

- **The review panel's internal design** — lenses, rounds, the paged latch,
  promotion. That is upstream's design and this document treats it as a
  dependency, not a subject.
- **The hunters, the debug reporter, and the eval rig.** Separate subsystems
  that share only the script package.
- **Replacing CodeRabbit.** Phase 1 exists partly to measure whether a second
  machine reviewer says anything CodeRabbit does not.
- **Any change to merge policy.** No workflow in any phase *calls* the approve or
  merge endpoints, and a human approval remains required throughout. That is a
  statement about what the workflows do, not about what their credentials could
  do: a token holding `contents: write` and `pull-requests: write` together can
  approve and merge whether or not any workflow asks it to.

  **Four workflows that ship today hand an agent exactly that pair**, and saying
  otherwise would document an invariant this repository does not hold. Each mints
  one installation token with `contents: write` + `pull-requests: write` +
  `issues: write` and passes it to `claude-code-action` as `github_token`, with
  `Bash` in `--allowedTools`, in a job whose workspace is checked out from the
  untrusted PR branch:

  | Workflow | Token mint | Handed to the agent |
  | --- | --- | --- |
  | `agent-fix.yml` | lines 201-203 | line 536 |
  | `agent-iterate-ci.yml` | lines 156-158 | line 456 |
  | `agent-review-panel.yml` (fix job) | lines 1880-1882 | line 2075 |
  | `agent-review-reply.yml` | lines 158-160 | line 268 |

  So the human-approval invariant rests on **branch protection on `main`** and on
  the fact that nothing in these workflows asks the token to approve or merge — it
  does **not** rest on the credentials being unable to. A successful prompt
  injection in any of the four reaches a token that can submit an approving review
  on a PR the App did not author and then merge it. That gap is open and it is not
  closed by anything on this branch.

  Two things about that sentence are weaker than they read, and both are recorded
  here rather than left for the next reader to discover.

  **The compensating control is unverified in this repository.** Branch protection
  on `main` is a repository *setting*. It appears nowhere in the tree — no
  ruleset fixture, no `CODEOWNERS`, and no test in `scripts/agent/checks.test.mjs`
  asserts it. Nothing here would notice if it were relaxed tomorrow. Phase I's gate
  (below) is the only place in this design that reads the setting at run time, and
  Phase I is not landed. So the invariant currently rests on an assumption, checked
  by nobody.

  **Splitting the mint is necessary and not sufficient, as these four jobs are
  built.** The correction is Phase I's acceptance criterion 1 — the agent gets
  `contents` + `issues` and no `pull-requests`, and every step needing
  `pull-requests` becomes a trusted step in the workflow file. But "trusted step"
  is a claim about the *code the step runs*, not about where the step sits in the
  file, and in these jobs it does not hold yet: each copies `scripts/agent` to
  `$RUNNER_TEMP/agent-tools` **before** the agent runs (`agent-fix.yml:408`), then
  runs the agent with `Bash` and no filesystem confinement
  (`agent-fix.yml:524-634`), then executes those same staged files afterwards
  (`agent-fix.yml:657`, `:672`, `:770`). An agent that rewrites
  `$RUNNER_TEMP/agent-tools/metrics.mjs` mid-run is executed by a later step — so
  handing that step the `pull-requests` token would hand it to the branch. This is
  criterion 12 ("staged scripts in `$RUNNER_TEMP` are not a trust boundary")
  arriving in the four workflows that already shipped, and **both corrections have
  to land together**: the split alone relocates the capability without removing it.

  Neither can be landed from here. No credential in this pipeline may write
  `.github/workflows/**` (see Phase I below) — re-measured against this exact file
  on 2026-09-23, where a push carrying a one-byte change to `agent-fix.yml` came
  back `refusing to allow a GitHub App to create or update workflow
  .github/workflows/agent-fix.yml without 'workflows' permission`. A maintainer
  must land both, and until then this is the repository's largest known open gap.

## Design

### 1. The verb surface

Upstream parses every command in one place (`scripts/agent/command.mjs`), which
is what keeps the workflows from drifting. It recognizes five verbs plus two
fallbacks for a bare `@claude` mention:

| Verb | Surface | Writes code | Trigger authority |
|------|---------|-------------|-------------------|
| `fix` | issue | branch + draft PR | `write` or above |
| `fix` | PR | commit pushed to the branch | `write` or above |
| `summarize` (alias `summarise`) | PR | no | **PR author**, or `write` and above |
| `review` | PR | no | **PR author**, or `write` and above |
| `loop` | PR | label only | `write` or above |
| `rerun` | PR | no | `write` or above |
| *(bare mention)* → `reply` | PR | commit pushed to the branch | `write` or above |
| *(bare mention)* → `help` | issue | no | `write` or above |

Two properties of that table drive the phasing:

**`fix` reaches two different workflows and never both.** On an issue it means
*plan and implement this*; on a pull request it means *make one fix attempt
against the review panel's standing verdict*. The workflows gate on
`!github.event.issue.pull_request` and on its presence respectively. Upstream
calls this the most confusable fact in the loop, and this document keeps the
two apart by deferring the issue arm entirely.

**`review` and `summarize` are the only verbs a PR author can invoke.** Every
other verb requires `admin`, `maintain` or `write` on this repository, checked
with `repos.getCollaboratorPermissionLevel` rather than `author_association` —
organization membership is not repository permission. For a repository whose
contributions arrive mostly from forks, that is the whole difference between a
surface contributors can use and one only maintainers can.

### 2. The phases

Each phase lands on its own, is useful on its own, and is reverted by removing
its workflow files.

#### Phase 0 — `/self-review`, locally

A bounded review → fix → re-verify loop over the branch diff, run from Claude
Code before the PR exists. Three rounds maximum, exits early on the first clean
round, and a finding the author believes is wrong goes into a rebuttals file
with evidence rather than being silently ignored.

- **Lands:** one command definition under `.claude/commands/`.
- **Requires:** nothing. No secret, no workflow, no organization change.
- **Relation to today:** [CLAUDE.md](../../CLAUDE.md) step 3 already says to
  dispatch a code review before pushing. This replaces one prose line with a
  bounded loop that has an exit condition.
- **Exit criteria:** used on three real branches; the rounds it reports are
  legible in the task's `*-lessons.md`.

#### Phase 1 — `@claude review` and `@claude summarize`

Both are advisory and read-only toward code: they post a comment, create no
check runs, and gate no merge. Both are throttled to one run per head SHA —
pushing a commit is what re-arms them.

- **Lands:** two workflows, the shared script package under `scripts/agent/`,
  the six lens rubrics.
- **Requires:** `CLAUDE_CODE_OAUTH_TOKEN`, which this repository already holds
  — `ci.yml`'s `bench` job runs the Claude Code CLI with it today. A token
  *pool* (`CLAUDE_CODE_OAUTH_TOKEN_<n>`) is wanted but not required: six lenses
  run concurrently and a single token rate-limits.
- **Does not require:** a GitHub App. Comments post as `github-actions[bot]`
  under `GITHUB_TOKEN`, with `issues: write` declared on the jobs that post. The
  App becomes necessary in Phase 3 and can be introduced there.

  One inherited claim is worth recording, because it is the single thing most
  likely to fail first. Upstream mints an App token for these comments, its
  workflow stating that a fork-PR-triggered run's `GITHUB_TOKEN` is forced
  read-only and that `issues.createComment` answers 403. An `issue_comment` run
  executes in the base repository with the permissions its job declares, which
  is why `issues: write` is expected to be enough here — expected, not proven,
  and not proven cheaply without a fork PR to try it on. So the placeholder
  comment is `continue-on-error` and the publish step creates a fresh comment
  when no placeholder id reaches it: a refusal costs the "running…" note, not
  the review. If the findings comment itself 403s on a fork, the job goes red
  rather than silent, and the remedy is to bring the App forward from Phase 3.
- **Fork behavior:** works. Neither verb checks out or executes branch code;
  the diff and metadata are read through the API and handed to the model as
  data.
- **Exit criteria:** on twenty PRs, the panel's findings are compared against
  CodeRabbit's on the same diff. Phase 2 is justified only if the panel raises
  blocking-severity findings CodeRabbit did not.

#### Phase 2 — `@claude loop` and `@claude rerun`

This is where the review panel becomes *gating*: `loop` applies the
`agent:managed` label, which is the only thing the panel's gate admits a PR on;
the panel then records one check run per lens, dispatches bounded fix rounds,
and latches the PR for a human when the rounds are exhausted. `rerun` clears
that latch — deliberately, because pushing a commit does not.

- **Lands:** the panel workflow, the round guard, the latch, the label.
- **Requires:** a GitHub App (`AGENT_APP_ID`, `AGENT_APP_PRIVATE_KEY`), and the
  branch-protection conversation. The App is not optional the way it is in
  Phase 1, for two separate reasons.

  The first is the fix loop: a fix round pushes, and a `GITHUB_TOKEN`-authored
  push does not re-trigger workflows, so the commit would land and no round
  would ever re-review it — the loop stopping silently, in the direction that
  looks like success.

  The second used to be that Phase 2 did not degrade gracefully without it —
  `promote` minted the token unguarded, so with the secrets unset that job
  failed and `stalled` paged a human and latched `agent:blocked` on every
  otherwise-clean PR. That is fixed: every step that consumes an App token is
  now conditional on a presence check, the three commenter-facing verbs answer
  with "this needs the App, which is not configured" instead of dying, and the
  CI arm stands down with a notice. Two tests hold the line — one that no step
  consumes the token unconditionally, one that each verb still answers.

  So the phases can now be enabled independently after all, through the single
  switch: turn `AGENT_PIPELINE_ENABLED` on and Phase 1 works, while `loop`,
  `rerun` and `fix` refuse legibly until the App exists.

- **Requires:** a docs-only PR cannot use it. `ci.yml` carries `paths-ignore`
  for markdown, `api/docs`, `build/charts`, `design/` and `*.txt`, and the panel
  triggers only on a CI run, so a PR confined to those paths never starts a
  round — `@claude loop` labels it and nothing happens. `@claude review` is the
  path for those, and the loop's confirmation comment says so.
- **Requires:** a decision about the six new check runs that appear on a
  labelled PR. Whether any of them is *required* to merge is a repository
  setting, and should start as "no".
- **Also lands:** `agent-iterate-ci.yml`. It is not optional either: `promote`
  and `fix` both require a green CI and `stalled` deliberately excludes a red
  one, all three on the stated grounds that this workflow owns the red-CI
  branch. Without it an agent-managed PR whose CI goes red gets no fix, no page
  and no label change, and nothing re-triggers — the silent stall the whole
  design exists to avoid. Its failure diagnosis is rebuilt on `gh run view
  --log-failed`, because the artifact upstream renders is written by a verify
  runner this repository does not have.
- **Fork behavior:** `loop` is same-repo only and falls back to posting
  `@claude review` on a fork PR.
- **Exit criteria:** a PR that entered the loop reached *ready for review*
  without a maintainer touching it, and a second one latched for a human
  instead of retrying forever.

#### Phase 3 — `@claude fix` on a PR, and the `reply` fallback

The first phase in which a bot pushes commits to a contributor's branch.
`fix` is eligible only when the current head SHA already carries completed lens
check runs — a commit landing after the panel moves the head, so that one
question answers both "did the panel run?" and "has anything landed since?".
It fails toward ineligible on every unknown.

`reply` is the bare-mention fallback: a comment mentioning `@claude` with no
verb on an agent-authored PR is treated as review feedback to evaluate, act on
if warranted, and answer in-thread — pushing back with reasoning when the
finding is wrong.

- **Requires:** the same GitHub App as Phase 2, and an `agent` environment.
- **The untrusted-setup rule.** From the moment these jobs check out the PR
  branch they hold an App token, so no setup step may execute build
  instructions the branch wrote. `make tools` is exactly that shape — five `go
  install` lines a PR can rewrite — so the linter is installed from a version
  pinned in the workflow instead, and a test asserts no token-bearing job runs
  it. The agent itself does run branch code and does hold the token, because it
  has to push; what must not happen is the job arriving there having already
  run the branch's Makefile for its own convenience.
- **Cost note:** every fix commit re-runs `ci.yml`, which here means a MongoDB
  stack plus `-race` integration tests — materially more expensive per round
  than upstream's. Upstream's "the panel runs concurrently with CI, not after
  it" decision matters more in this repository than in the one it was made for.
- **Ordering within the phase:** `fix` first. `reply` is the only verb that
  also fires on `pull_request_review_comment`, and it triggers on a mention
  with no verb at all, so its misfire surface is the widest of the set.

#### Phase I — `@claude fix` on an issue (issue → PR) — DESIGNED, NOT LANDED

> **Nothing in this phase is installed.** `.github/workflows/agent-implement.yml`
> does not exist in this repository. This section is a specification for the
> maintainer who lands it, not a description of running code. Everything below
> that reads as present tense describes what the workflow **must do**, and the
> requirements list is the acceptance criteria.

**This was a Non-Goal, deferred past every phase; that was reversed on
2026-09-22, and the implementation was then withdrawn on 2026-09-23 without
reversing the decision.** The section is kept rather than rewritten out, because
the argument against it is still the argument to weigh, and a reader deciding
whether to adopt this elsewhere needs both halves.

**Why a written, reviewed draft was withdrawn rather than merged.** A full
`agent-implement.yml` was written and reviewed on this branch. Five review lenses
returned blocking findings against it — a token that satisfied its own gate, a
protection check that accepted an approval surviving the next push, a refusal
path that threw instead of explaining, an entry point that skipped the collision
guard, and issue text re-read long after the maintainer authorised it. None of
them could be corrected, because **no credential in this pipeline can write
`.github/workflows/**`**. That is measured, not assumed: a commit touching the
file, pushed to a scratch ref, came back

```
! [remote rejected] refusing to allow a GitHub App to create or update workflow
  `.github/workflows/agent-implement.yml` without `workflows` permission
```

and the Git Data API refuses the same tree. It is the intended design — the App
holds no `Workflows` permission, `checks.test.mjs` asserts that no agent token
may ever request one, and `agent-fix.yml` prints that property to contributors.
Granting it would hand an agent the ability to rewrite its own gates, which is a
worse trade than deferring the verb.

Two rounds tried to route around this by committing the correction as an
appliable patch under `docs/tasks/active/`, addressed to whoever read it. That is
worse than it looks: `docs/**` *is* writable by every agent token, so an
apply-me patch there launders arbitrary workflow content around the very
permission boundary above, and the first attempt was simply never applied. The
patch is gone. What survives is the requirements list below — prose a maintainer
implements from, which no `git apply` can turn into a workflow nobody read.

So the shape of this phase is: the design is settled and recorded here; the
implementation is a human's to write and push, against the acceptance criteria
below.

The case for deferring: issue → PR originates work rather than reviewing it, so
it sits on a different axis from every other verb here; it is the one verb whose
output nobody asked for at the moment it is produced; and it is the first place
a bot pushes commits and opens a pull request.

The case for doing it now, which won: the phases as numbered deliver nothing a
person can *look at* until Phase 3, and Phase 2's exit criteria cannot be met
without a stream of agent-managed PRs to observe. Issue → PR produces those. The
lettered name says it is off the numbered track, not that it replaces a phase.

What the reversal actually costs, stated plainly: a PR this verb opens is
reviewed by `@claude review` (advisory) and a human. The gating panel has not
completed a real review yet, so the machinery that was supposed to grade
agent-authored code is not yet grading it. That is the risk, and it is accepted
on the strength of the gate below rather than waved away.

- **Would land:** `agent-implement.yml` — `route`, `implement`, `help`. The `help`
  job is not incidental: today every verb on an issue is refused by the same
  `github.event.issue.pull_request` gate, with no comment, so a maintainer who
  types `@claude` on an issue gets a green tick and silence.
- **Requires:** the GitHub App **plus `Administration: read`**, which no other
  verb needs. The workflow refuses to run unless `main` requires at least one
  human approving review, and reading that setting is what the permission buys.
  It fails closed on every error including a permissions error — a repository
  where the check cannot be answered is one where the bot does not push.
- **One gate, and it bounds what LANDS — not what the run can reach.** Before
  anything is pushed, `main` is verified to require an approving review this App
  cannot bypass: a ruleset whose bypass list names actors, or is not visible to
  this token, or that reports `current_user_can_bypass` as anything but `never`,
  is not counted; nor is classic protection carrying a
  `bypass_pull_request_allowances` entry. Unverified protection is treated as
  none. It is a property of the **repository**, which is why it is checked at run
  time rather than asserted here.

  The PR opening as a **draft** is not a second gate, though two earlier drafts
  of this document said it was. In the withdrawn implementation it was a line in
  the prompt — a convention the agent could get wrong, verified by nothing.
  Criterion 9 below is what turns it into an argument the workflow passes.

- **Two things are known-unverified, and both are recorded rather than guessed.**
  The job's ceiling is 90 minutes and an installation token lives 60, so a run
  that passes the hour loses the ability to push — the failure is a 401 at the
  end of the expensive part. And `current_user_can_bypass` is documented as the
  bypass type of *the user making the request*; under an installation token there
  is no user, and what it returns is untested here because this repository has no
  rulesets. If it is anything but `never`, every ruleset-protected repository is
  refused. Both fail safe. Both should be settled by observation on the first
  runs rather than by argument.
- **Trusted authors only:** `OWNER`, `MEMBER`, `COLLABORATOR`, and a
  write-access check on top. An issue body is data, never instructions.
- **Not ported:** upstream's issue classifier. It labels issues into a category
  corpus, which is eval-rig machinery this document already excludes, and it
  calls the raw Messages API with an `ANTHROPIC_API_KEY` this repository does not
  have. If it is wanted later the model call goes through `ask.mjs`, which
  already authenticates with `CLAUDE_CODE_OAUTH_TOKEN` and carries the read-only
  tool invariant — not through a second, unshared HTTP path.
- **Exit criteria:** three issues turned into PRs a maintainer merged without
  rewriting the change, and one where the agent stopped and said it could not do
  it rather than opening a PR that looks finished.

##### Acceptance criteria for the implementation

Each of these is a defect the reviewed draft actually had. A maintainer landing
this workflow should treat the list as the review checklist, and should expect
the three `agent-implement.yml` guards in `scripts/agent/checks.test.mjs` — which
skip today via `workflow-presence.mjs` and re-arm the moment the file exists — to
run and to have to pass.

1. **The gate must not be satisfiable by the party it gates.** One App token
   carrying `pull-requests: write` (which opening a PR needs) beside
   `contents: write` is exactly the pair that submits an **approving review** and
   then **merges**; GitHub only blocks approving a PR the same identity authored,
   so an injected run could approve and merge somebody else's. Mint **two**
   tokens: the agent gets `contents` + `issues` and no `pull-requests`, the
   workspace checkout persists *that* narrower one (`persist-credentials` writes
   it into `.git/config`, which the agent reads with one `Bash` call), and every
   step needing `pull-requests` is a trusted step in the workflow file.
2. **The protection check must require `require_last_push_approval`** on both the
   classic-protection and the ruleset path. Without it an approval given on an
   agent PR stays valid across every later commit the bot pushes to the same
   branch — and the PR-side `fix` and `loop` verbs do push to agent branches.
3. **`contents: write` is not bounded by a check on `main` alone.** It also
   creates tags and publishes releases, and `docker-publish.yml` fires on
   `release: published` with Docker Hub credentials. Either the agent's token
   must not reach releases, or that escape is an accepted, written risk.
4. **Refusals must print their reason.** The draft's `setFailed` referenced an
   identifier declared nowhere, so three of four refusal paths raised a
   `ReferenceError` instead of the diagnosis. The gate failed closed and told
   nobody why — and that it shipped at all is proof no test ever executed it.
5. **The gate must be executed by a test, not matched as text.** More than a few
   lines of `github-script` get extracted and run against fixture repository
   states. A regex over the YAML asserts that somebody typed the right
   characters, which is how (4) survived five review rounds.
6. **Every entry point gets every guard.** The draft's duplicate-PR pre-flight
   was conditioned on `issue_comment` while `workflow_dispatch` reached the same
   checkout, the same branch name and the same `git push` — and dispatch is the
   path with no concurrency group and no thread to complain on.
7. **The pre-flight must also catch the orphan branch.** Looking only at open
   pull requests misses the documented failure — a run that died between
   `git push -u origin` and `gh pr create` leaves the branch with no PR, and the
   report tells the maintainer to retry straight into it.
8. **The authorising text must be the snapshot the maintainer read.** Take the
   issue title and body from the immutable event payload, not from a
   `gh issue view` that runs minutes later behind a token mint, three API calls,
   a checkout and two toolchain installs — the author can edit the body in
   between. (Dispatch has no payload issue and must ask the API; there the
   dispatcher is the reader.)
9. **The PR opens after the agent stops, from a trusted step, with `--draft`
   passed by the workflow.** `agent-review-panel.yml` and `agent-iterate-ci.yml`
   claim any `agent/`-prefixed head branch and dispatch fixers that push to it,
   and both are reached only through a CI run, which fires on `pull_request`.
   Landing the PR mid-run therefore gives the branch three writers while the
   implement agent is still committing. Draft-ness must likewise be an argument
   in the file, not a line in a prompt; note that `mark-ready.mjs --promote`
   un-drafts `agent/` PRs on its own schedule.
10. **Label the originating issue `agent:candidate`.** The panel's design-fit
    lens resolves a PR's spec from `Fixes #N` and only trusts an issue carrying
    that label, so without it every PR this verb opens is reviewed with no
    knowledge of what it was asked to build.
11. **The ambient `GITHUB_TOKEN` gets `issues: write` and `pull-requests: read`,**
    which is what its only consumer needs — not the `contents`/`pull-requests`
    write the draft granted it.
12. **Staged scripts in `$RUNNER_TEMP` are not a trust boundary.** The draft
    copied `scripts/agent` there and called it the trusted copy, then ran the
    agent with unrestricted `Bash` in the same job and executed those files
    afterwards with the App token. Either run trusted tooling from a path the
    agent cannot write, or stop describing it as trusted.
13. **The per-cause dedupe on the no-PR reporter must be acknowledgement-aware.**
    Suppressing on the mere presence of a prior report of the same kind
    re-creates the silence it exists to close: the "On it" acknowledgement is
    posted on every run and never deduplicated, so a second failure of the same
    kind leaves a fresh claim that a run is in progress with nothing to withdraw
    it — reached through the report's own advice to retry.

**What none of that covers.** Even with all thirteen satisfied, the agent runs
with an unrestricted `Bash` beside a live installation token and a model
credential, on text an arbitrary GitHub user wrote. The gate constrains merging;
it does not constrain network egress. A successful prompt injection does not need
to get code into `main` — it already has the token. Of the two narrowings, one is
mechanical and one is only a convention, and an earlier draft of this section
wrote both as though they were the first kind. **Mechanical:** the issue title and
body come from the event-payload snapshot (criterion 8), which is the text as it
stood when the maintainer's comment was created, written to a file the prompt
names as untrusted input. **A convention, enforced by nothing:** a prompt line
telling the agent not to run `gh issue view` and not to read the issue's comments
is unenforceable — `--allowedTools` includes `Bash` and the agent holds a token
`gh` authenticates with, so a comment posted mid-run is reachable by any agent
that decides to look. It narrows the honest agent's behaviour and bounds nothing.
Neither is a control on egress, and the residual risk is accepted knowingly: the
token is installation-scoped to this repository, expires in an hour, and carries
no `workflows` permission. The control that would actually close the channel is a
runner egress policy, and it is the next thing to add here.


### 3. The kill switch

Every agent workflow upstream opens with the same condition:

```yaml
if: vars.AGENT_PIPELINE_ENABLED == 'true' && ...
```

A repository variable that is **unset by default**, so a workflow can be merged
and reviewed while inert, and the whole surface can be switched off during an
incident without reverting anything. Every phase here adopts it. It is also
what makes the phase boundaries real: Phase 2's workflows can land before the
branch-protection conversation concludes.

### 4. What cannot be copied

Three places carry the upstream repository's shape and have to be rewritten
rather than ported. Everything else — the six lens rubrics included — is
repository-agnostic; the rubrics contain no framework, package or product
names.

**a. File classification.** The panel routes each changed path to one of
`code` / `code-adjacent` / `policy` / `design-spec` / `prose`, and the globs are
written against a pnpm monorepo. The Go equivalent has to place `api/yorkie/v1/`
(generated), build-tag-gated test files, `test/`, and this `docs/design/` tree.
The fail-safe direction is preserved: anything unmatched falls through to
`code` and is reviewed by every lens.

**b. What the mechanical lanes already prove.** Each lens is told once what CI
covers, so it does not spend turns re-deriving findings that a lane reports for
free. Upstream's copy stresses that every claim in it was read off the
repository rather than assumed, because the failure mode is silent — tell a
lens something is covered when it is not and that finding class stops being
reported. Read off this repository's lanes as of v0.7.23:

*Enforced on every code PR* — `golangci-lint run ./...` with `gofmt` and
`goimports` as formatters and `gosec`, `revive`, `lll`, `wrapcheck`, `gocyclo`,
`goconst`, `misspell`, `nakedret`, `goprintffuncname` as linters; `buf lint`;
`buf breaking` against the PR's own base commit; a codegen-freshness check that
fails if `buf generate` dirties `api/`; `make build`; `go vet -tags rgafuzz
./...`, which compiles the tag-gated reproductions without running them; and
`go test -tags integration -race ./...` against a real MongoDB.

*Enforced by nothing* — `staticcheck` and `unused` are explicitly disabled in
`.golangci.yml`. The Apache license header every Go file is required to carry
is a convention with no lane behind it. `complex-test`, `bench` and `load-test`
are path-gated and do not run on most PRs. And `ci.yml` carries
`paths-ignore: "**/*.md"`, so a documentation-only PR runs none of the above —
only the separate `docs.yml` link check.

**c. The verification command the fixer runs.** `pnpm verify:fast` becomes
`make lint` plus `go test ./...`; the integration lane needs the docker-compose
stack and is left to CI rather than run inside the fix job.

#### 2.1 What the port actually carried

All four phases landed on one branch, so the questions this section answered
per-phase are answered once.

The module set is the import closure of the workflows' entry points, which is
wider than the verbs: `review-panel.mjs` imports `rounds.mjs` and
`fix-report.mjs` directly, and the advisory panel reads the fix agent's reports
so that its verdict matches the gating one's. A module no verb reaches ships
unused rather than being cut out of a 3,400-line file, because surgery inside a
ported module is what makes the next sync from upstream expensive.

Four guards in the ported suites assert that a module and a workflow carry the
same literal. They skip when their workflow is absent, through one helper
(`workflow-presence.mjs`) that says why — and with every phase installed, none
of them skips today. The helper stays because `agent-implement.yml` (issue → PR)
is still deferred, and because the arrangement is what makes adding a phase
safe: a guard re-arms by itself when its workflow lands, whereas deleting it
would make that day a silent regression of a check written precisely because its
failure mode is invisible.

### 5. Where the scripts go

`scripts/agent/` as a standalone npm package with its own lockfile, outside any
workspace — which is how upstream ships it, and it fits what this repository
already does: [docs.yml](../../.github/workflows/docs.yml) sets up Node 22 and
runs `node --test` over `scripts/test/`. The package's only dependencies are
the Claude Agent SDK and `zod`. Nothing Go touches it, and it never enters
`make build`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A third reviewer adds noise rather than findings | Phase 1's exit criteria is a measured comparison against CodeRabbit on twenty PRs, not an impression |
| Prompt injection through PR bodies, comments and diffs | The panel treats `.github/**`, `CLAUDE.md`, `AGENTS.md` and `CONTRIBUTING.md` as `policy` — reviewed as executable instructions, not prose. Phases 0–2 push no code, so the blast radius until Phase 3 is a comment |
| The mechanical-coverage note goes stale and silences a finding class | It names mechanisms and lanes, not categories, so a lane that is removed leaves a claim that is checkably false. Re-read it whenever `.golangci.yml` or `ci.yml` changes |
| Fix rounds multiply CI cost (MongoDB + `-race` per round) | Rounds are bounded and the PR latches for a human when exhausted. Phase 3 lands last, after the cost of a round is known from Phase 2 |
| A workflow misfires on an unrelated comment | `AGENT_PIPELINE_ENABLED` is unset by default and turns the surface off without a revert |
| Two verb tables disagree | One table, in `CONTRIBUTING.md`, generated from nothing else. Upstream carries two and records the disagreement as a known risk |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Phase by verb, not by component | The components are one graph; the verbs are what a person types. A phase boundary users can see is one they can be told about |
| `summarize` in Phase 1 with `review` | Same trust level, same throttle, zero write authority, and both are invocable by the PR author — together they are the only combination that is useful to a fork contributor |
| `reply` last | It is the only verb with no verb: any bare `@claude` mention on an agent PR triggers it, and it also fires on inline review comments. Widest misfire surface in the set |
| Advisory before gating | Check runs interact with branch protection and with the merge queue. Landing the reviewer first separates "is it any good?" from "does it block merges?" |
| Keep the upstream kill-switch variable | Lets a workflow be merged inert, so review of the workflow and the decision to enable it are separate events |
| Defer issue → PR (Phase I), after reversing that deferral once | It originates work, where every phase here reviews work a human already decided to do — and when it was tried anyway, the corrections review demanded could not be pushed, because no agent credential may write `.github/workflows/**`. The design is specified; the implementation is a human's |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Port the whole pipeline in one PR | ~14,000 lines across workflows and scripts, a GitHub App, branch-protection changes, and a cost profile nobody has measured — in a single review |
| Adopt only `fix`, `loop`, `rerun` (skip `review`) | All three are inert without the panel: `loop` applies a label nothing reads, `rerun` clears a latch nothing sets, `fix` reads check runs nothing writes |
| Gate on the panel from Phase 1 | Makes the first phase a branch-protection change, which is the part that needs the most evidence and has the least at that point |
| Write our own lens rubrics | The upstream six are repository-agnostic and already measured. The repository-specific parts are §4's three, and those have to be written regardless |
| Drop CodeRabbit when the panel lands | Reverses the burden of proof. Phase 1 measures whether the panel adds anything; that question is unanswerable with only one reviewer running |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
