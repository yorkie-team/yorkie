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

  **No agent is handed that pair, and no step the workflow runs after the agent
  holds it.** That is a claim about the workflow's steps; what the agent can
  reach by other means is listed under the residuals below. Until 2026-09-24 four workflows (`agent-fix.yml`, `agent-iterate-ci.yml`,
  the panel's `fix` job and `agent-review-reply.yml`) passed `claude-code-action`
  one token carrying `contents` + `pull-requests` + `issues`. Now every job that
  runs a pushing agent is built the same way, and `agent-implement.yml` too:

  | Where | Holds | Runs |
  | --- | --- | --- |
  | The agent job, before the agent | a pre-agent App token where a pre-agent step needs one (never `contents: write`), revoked by a step just before the agent; a GITHUB_TOKEN without `contents: write` | gates, the brief, the placeholder — all from `main` |
  | The agent step | a **narrow** App token, `contents: write` only, persisted into `.git/config` | the agent, with `Bash` |
  | The agent job, after the agent | no workflow step but the artifact upload — though the runner still runs the earlier actions' **post steps** here (see residuals) | the handoff of the agent's files, as data |
  | A separate job (`report`, `fix-report`, `finish`) on a fresh runner | a wide App token minted there | posting, disclosure, paging, metrics — `main`'s scripts |

  **Why a separate job and not a re-staged copy.** The previous correction
  re-checked-out `scripts/agent` after the agent stopped, and called the steps
  that ran it trusted. It protected the files and not the process: the agent's
  shell inherits `$GITHUB_ENV` and `$GITHUB_PATH`, which the runner applies to
  every later step of the same job, and it can rewrite `~/.gitconfig`,
  `~/.npmrc` and the workspace's `.git/config`, where a `remote.origin.url` of
  `ext::sh -c …` is command execution for the next `git ls-remote origin`. A
  step later in that job ran on the agent's terms whatever file it executed.
  `checks.test.mjs` ("nothing after the agent but the handoff") now fails any
  pushing agent job with another step after the agent.

  **What crosses is data, and it is read as data.** The agent writes its fix
  report, disputes, reply, PR description and execution log; the trusted job
  never posts any of them as written. `fix-report.mjs republish` and
  `rebuttal.mjs republish` parse the structured record and render every comment
  afresh — so a file reading `<!-- agent-review-paged -->` posts nothing — and
  `metrics.mjs` coerces the log's counts and identifiers before any of it reaches
  a comment. Free prose (the reply, the PR description) has every `<!--`
  neutralised. Heads are read from the API, never from a checkout.

  **What this does not close, stated so it is not mistaken for closed.**

  - **`contents: write` alone reaches the merge endpoint.** The narrow token
    cannot approve or comment, but it can merge a PR that already satisfies
    `main`'s protection — a human-approved PR can be merged early by an injected
    agent. `require_last_push_approval` (Phase I criterion 2) limits which
    approvals count; it does not stop the merge of one that does. The agent needs
    `contents: write` to push its own commits, so closing this means the agent
    stops pushing: it commits, hands a bundle across, and the trusted job pushes.
    That is the next change here. The two workflows that turn a push or release
    by the App into Docker Hub credentials refuse the App as the actor.
  - **The agent job still holds the App's private key, and still runs code
    after the agent.** The key is there to mint the narrow token, and the runner
    runs every earlier action's **post step** after the agent, in the same job:
    `create-github-app-token`'s (whose environment carries the private key as
    an input and each minted token as state) and `actions/checkout`'s (carrying
    the job's `GITHUB_TOKEN`). The agent can reach those without `sudo` — the
    actions' code under `_actions/` is owned by the runner user and can be
    rewritten, a process left running outlives the agent step and can read the
    post step's `/proc/<pid>/environ` as the same user, and `$GITHUB_ENV` can
    set `LD_PRELOAD` — and with the passwordless `sudo` a GitHub-hosted runner
    grants, it can read the runner's memory directly. With the key it can mint
    any token the installation allows, the approve+merge pair included; with
    the `GITHUB_TOKEN` it can post as `github-actions[bot]`, an author several
    markers are trusted by. What IS done: no agent job's `GITHUB_TOKEN` carries
    `contents: write`; the pre-agent App tokens carry no `contents: write` and
    are revoked by a step before the agent starts; `setup-go` runs without its
    cache, whose post step would otherwise save the agent's module and build
    caches under a key `main` restores. Closing the rest needs the narrow token
    minted by a plain `run:` step (no post step, the key only in that step's
    environment), the job's `GITHUB_TOKEN` read-only, and `sudo` dropped before
    the agent — all three; dropping `sudo` alone closes none of the channels
    above.
  - **Branch protection on `main` is a setting, not a checked property.** It
    appears nowhere in the tree — no ruleset fixture, no `CODEOWNERS`, no test.
    Phase I's gate is the only thing that reads it at run time, and only for that
    verb.

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

### 1.1 Who vouches for the spec

`design-fit` is a **blocking** lens and it grades a diff against the
originating issue's *outcome* and *acceptance criteria*. Both the autonomous
panel and the on-demand `@claude review` resolve that issue from the PR body's
`Fixes #N` — text the PR **author** writes — so neither can treat it as a spec
on the author's word alone. The rule both apply is *the issue carries
`agent:candidate` and was not filed by a Bot*; otherwise the lens reviews with
no spec and logs a warning.

That made the label the pipeline's provenance claim, and for the whole of
Phase I it had exactly one writer: `agent-implement.yml`, after it opened its
own PR. So a PR the pipeline opened was graded against a spec and a PR a human
opened was graded against nothing — the same blocking lens, silently doing
less work on half its input.

Two things close it, and the split between them is the whole design:

- **`.github/ISSUE_TEMPLATE/agent-task.yml`** collects the fields the lens
  reads (outcome, acceptance criteria, non-goals) and **applies no labels.** A
  form's `labels:` are applied to whatever any account submits and this
  repository is public; auto-labelling would let an arbitrary user author the
  text a blocking lens grades somebody else's PR against, reachable by filing
  one issue and writing `Fixes #N`. The label currently costs an attacker
  triage permission, and it has to keep costing that.
- **`agent-loop.yml` applies it**, to the issue the PR body names, when a
  maintainer opts the PR into the gating panel. That actor has already passed
  `repos.getCollaboratorPermissionLevel`, and choosing what a PR is graded
  against is the same decision as choosing to grade it.

What this does not make safe: the issue NUMBER still comes from the PR body,
so a maintainer running `@claude loop` on a PR whose `Fixes #N` points
somewhere unhelpful vouches for that issue. The verb's reply names the issue it
labelled for exactly that reason, and the label is visible and removable. The
alternative it replaces is no spec at all.

`@claude review` deliberately does **not** label: it is invocable by the PR
author, which is the trust level the label exists to be above. An advisory
review of an unlabelled human PR reviews without a spec, and that is the
correct answer for it.

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

- **Required, until 2026-09-25: a docs-only PR could not use it.** `ci.yml`
  carried `paths-ignore` for markdown, `api/docs`, `build/charts`, `design/`
  and `*.txt`; the panel triggers only on a CI run, so a PR confined to those
  paths never started a round — `@claude loop` labelled it and nothing
  happened, and the loop's confirmation comment had to say so.

  That filter now sits on `ci.yml`'s **`build` job** instead of on its
  `pull_request` trigger. The distinction is the whole fix: a trigger-level
  filter files **no run**, and three things here read the run's existence
  rather than its result — this workflow's `workflow_run`,
  `agent-iterate-ci.yml`'s, and `mark-ready.mjs`'s promotion gate, which
  refused an empty run list and told the operator to promote by hand. A
  job-level filter files the run and skips the Go lane inside it, so the cost
  is unchanged (one `ci-target-check` job, ~20s) and every PR reaches the
  pipeline.

  It also settles a question this document left open above: GitHub leaves a
  required check **Pending forever** when its workflow is skipped by path
  filtering, and reports **Success** when a job is skipped by a conditional. So
  making a lane required would have deadlocked every docs PR under the old
  arrangement and does not under this one.

  The filter fails toward RUNNING. Its only positive pattern is `**` and the
  rest are negations, so an unmatched path — a new directory, an unfamiliar
  extension — builds; `checks.test.mjs` pins that shape, and pins that
  `pull_request` carries no workflow-level filter. `dorny/paths-filter` needs
  `predicate-quantifier: every` for it, because under the default a lone
  `README.md` matches `**` and the negations do nothing.
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

A second gate sits behind that one and asks a different question: *could
`agent-iterate-ci.yml`'s fixer be pushing to this branch right now?* It answers
from CI's state on the head, and it carried a carve-out — *no CI run at all →
proceed* — justified as keeping the verb usable on a docs-only PR. That
justification was never reachable: the step runs only when `fix-eligible.mjs`
said yes, and `fix-eligible` refuses a head with no lens check runs, which only
a panel writes, which only a CI run starts. No CI run therefore meant no
verdict, refused one gate earlier. With Phase 2's filter change a docs-only PR
has a CI run anyway, so the branch now refuses: an invisible run is a CI
conclusion nobody can read, and this gate refuses every unknown.

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

#### Phase I — `@claude fix` on an issue (issue → PR)

> **This phase is installed.** `.github/workflows/agent-implement.yml` exists and
> is live behind `AGENT_PIPELINE_ENABLED`, with one operational prerequisite that
> is not yet met — see the gate below.

**This was a Non-Goal, deferred past every phase.** That was reversed on
2026-09-22; the implementation was then withdrawn on 2026-09-23 and restored on
the same day with the findings fixed. The section keeps the argument against it,
because that argument is still the one to weigh and a reader deciding whether to
adopt this elsewhere needs both halves.

**Why it was withdrawn once, and what that taught.** Five review lenses returned
blocking findings against the first draft, and none could be corrected *by the
pipeline*, because **no credential here can write `.github/workflows/**`**. The
fix agent carried the correction as an apply-me patch under `docs/**` for three
rounds instead, and the panel then called that a defect of its own — `docs/**`
is agent-writable, so a patch there launders workflow content across the boundary
the missing `workflows` permission exists to hold. That reasoning is right and is
why the withdrawal was the correct move for an agent to make.

Two things came out of it that outlive the episode. **The refusal covers create
and update only: delete succeeds.** The agent removed the workflow with the same
token that cannot edit it, which means "a fix agent can never rewrite the lanes
that grade it" — asserted in this document, in several workflow comments and in
the App's own description — was false. It can delete them. That is recorded here
rather than quietly corrected, because the invariant was load-bearing in the
argument for every phase.

And the findings themselves were real. They are fixed in the restored workflow,
each one by a mechanism rather than a promise:

- **Lands:** `agent-implement.yml` — `route`, `implement`, `help`. The `help`
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

- **One thing is known-unverified, and it is recorded rather than guessed.**
  `current_user_can_bypass` is documented as the bypass type of *the user making
  the request*; under an installation token there is no user, and what it returns
  is untested here because this repository has no rulesets. If it is anything but
  `never`, every ruleset-protected repository is refused. It fails safe, and it
  should be settled by observation on the first runs rather than by argument.

  The other entry that stood here — the job's 90-minute ceiling against a
  60-minute installation token, so a run past the hour loses the ability to push
  and 401s at the end of the expensive part — was not unverified at all, only
  unfixed, and `agent-fix.yml` and the panel's `fix` job carried the same
  ceiling. All three are now 55. The minutes between 60 and 90 held a credential
  that had already expired: they could spend model budget and a round from
  `MAX_REVIEW_ROUNDS` and could not push, which is the outcome raising the fixer
  wall from 45 to 90 was meant to prevent. Refreshing mid-round would need a step
  after the agent, which the trust model forbids; `checks.test.mjs` now fails any
  of the three whose wall reaches the token's hour.
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

Each of these is a defect the reviewed draft actually had, and each is held by
the installed workflow. Treat the list as the review checklist for any change to
it: the three `agent-implement.yml` guards in `scripts/agent/checks.test.mjs` run
today (they skip through `workflow-presence.mjs` only if the file is ever
withdrawn again) and have to pass.

1. **The gate must not be satisfiable by the party it gates.** One App token
   carrying `pull-requests: write` (which opening a PR needs) beside
   `contents: write` is exactly the pair that submits an **approving review** and
   then **merges**; GitHub only blocks approving a PR the same identity authored,
   so an injected run could approve and merge somebody else's. Mint **two**
   tokens: the agent gets `contents` alone, the workspace checkout persists
   *that* narrower one (`persist-credentials` writes it into `.git/config`,
   which the agent reads with one `Bash` call), and every step needing more runs
   in a separate job the agent never ran in (see Non-Goals). `contents: write`
   on its own still reaches the merge endpoint for a PR that is already
   approved — an accepted, written residual until the agent stops pushing.
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
    knowledge of what it was asked to build. Two workflows read the label —
    the panel and `agent-review-on-demand.yml`, with the same
    `labelled && human` rule — and until 2026-09-25 this job was its only
    writer, so the property held for agent-opened PRs and for nothing else.
    See §1.1.
11. **The ambient `GITHUB_TOKEN` gets `issues: write` and `pull-requests: read`,**
    which is what its only consumer needs — not the `contents`/`pull-requests`
    write the draft granted it.
12. **Nothing after the agent in its own job is a trust boundary.** The draft
    copied `scripts/agent` to `$RUNNER_TEMP` and called it the trusted copy,
    then ran the agent with unrestricted `Bash` in the same job and executed
    those files afterwards with the App token. Re-checking them out after the
    agent did not help either: the agent owns the job's environment
    (`$GITHUB_ENV`, `$GITHUB_PATH`, `~/.gitconfig`, `.git/config`), not just its
    files. Every post-agent step runs in the `finish` job instead.
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
`.golangci.yml`. `complex-test`, `bench` and `load-test` are path-gated and do
not run on most PRs. And `ci.yml`'s `build` job is filtered on `**/*.md` (plus
`api/docs`, `build/charts`, `design/` and `*.txt`), so a documentation-only PR
runs none of the above — only the separate `docs.yml`, which checks
documentation links and reads no Go behaviour. *Which lanes execute* is
unchanged by that filter's move off the trigger; *whether a run exists* is
not, and Phase 2 above says why that mattered.

The Apache license header was on this list, as a convention with no lane behind
it, and 17 of 486 `.go` files had drifted by the time anyone counted. It now has
one: `scripts/verify-license.mjs`, run by `ci.yml`'s `build` job and by `make
verify` locally. It sits in `ci.yml` rather than the unfiltered `docs.yml`
because `agent-iterate-ci.yml` subscribes to CI alone — a gate that reds in
another workflow stops an agent-managed PR with nothing watching it — and
the `build` job's documentation filter costs it nothing, since no filtered path
holds a `.go` file. This paragraph is the source `review-panel.mjs`'s
`MECHANICAL_COVERAGE_NOTE` was derived from, so the two move together — a stale
entry here becomes a lens instructed to hunt a class CI already reds.

**c. The verification command the fixer runs.** `pnpm verify:fast` becomes
`make verify` — `make lint` plus the licence check plus `go test ./...`. The
integration lane needs the docker-compose stack and is left to CI rather than
run inside the fix job.

The three fixer prompts (`agent-fix.yml`, `agent-iterate-ci.yml`,
`agent-review-panel.yml`) spelled out `make lint` and `go test ./...` rather
than calling the target, and switching them was **not** a pure rename: `make
verify` also runs the licence check, so until 2026-09-25 the autonomous arm
verified without that gate and CI's `build` job was the only thing catching a
missing Apache header — a full round, plus a red CI, plus a CI-fix round, for a
line the fixer could have added before pushing.

The one thing that could have made the switch cosmetic is ruled out by reading
the jobs rather than the target: `make verify-license` announces `SKIPPED`
where Node is absent, and Node is **not** absent — all three jobs run
`actions/setup-node@v4` with `node-version: 22.x` as an ungated first step,
because their pre-agent gate scripts are Node and must not run on whatever the
runner image ships. So the gate really executes in the fixers. (`ci.yml` still
invokes `node scripts/verify-license.mjs` directly rather than through the
target, and its comment says why: a CI gate must not be able to fail open.)

Naming a target rather than a list is also what makes the next lane free: one
line in the Makefile reaches all three prompts at once.

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
was withdrawn once and may be again, and because the arrangement is what makes
adding a phase safe: a guard re-arms by itself when its workflow lands, whereas deleting it
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
| Land issue → PR (Phase I) after deferring it, withdrawing it once, and restoring it | It originates work, where every phase here reviews work a human already decided to do — and when it was first tried, the corrections review demanded could not be pushed, because no agent credential may write `.github/workflows/**`. It is installed behind a gate a repository setting must satisfy (`require_last_push_approval` on `main`), and its workflow changes are a human's to push |

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
