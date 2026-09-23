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
  do — see the Phase I caveat, where a token holding both scopes is treated as a
  defect to fix rather than as covered by this line.

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

#### Phase I — `@claude fix` on an issue (issue → PR)

**This was a Non-Goal, deferred past every phase, and that was reversed on
2026-09-22.** The section is kept rather than rewritten out, because the
argument against it is still the argument to weigh, and a reader deciding
whether to adopt this elsewhere needs both halves.

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
  of this document said it was. It is a line in the prompt — a convention the
  agent could get wrong, verified by nothing.

  > **⚠️ The gate as shipped is weaker than the paragraph above, and the verb is
  > not safe to enable until that is fixed.** Two earlier drafts of this section
  > described properties the workflow did not have; describing them again would
  > be the same error a third time, so what is actually in
  > `.github/workflows/agent-implement.yml` today is listed here:
  >
  > - **The gate is satisfiable by the gated party.** The App token the job mints
  >   carries `pull-requests: write` (opening a PR needs it) beside
  >   `contents: write`, and that pair is exactly what submits an **approving
  >   review** and then **merges**. GitHub only blocks approving a PR the same
  >   identity authored, so a run injected through the issue body can approve and
  >   merge somebody else's open PR. "A human approving review this App cannot
  >   bypass" is therefore not established.
  > - **The gate does not require `require_last_push_approval`.** An approval
  >   given on an agent PR stays valid across every later commit the agent pushes
  >   to the same branch, and the PR-side `fix` and `loop` verbs do push to agent
  >   branches.
  > - **Its refusal path throws.** The `setFailed` call references an identifier
  >   declared nowhere, so three of the four refusal reasons raise a
  >   `ReferenceError` instead of printing the diagnosis. The gate still fails
  >   closed; it just tells nobody why.
  > - **`workflow_dispatch` skips the collision pre-flight,** which is gated on
  >   `issue_comment`, so the dispatch entry point reaches the same pushing agent
  >   with no duplicate-PR guard.
  > - **The issue text is read by `gh issue view` minutes after the verb was
  >   typed** — after the token mint, the protection calls, the checkout and two
  >   toolchain installs. The author can edit the body in between, so the agent
  >   can act on text no maintainer read.
  > - **The draft PR lands mid-run.** `agent-review-panel.yml` and
  >   `agent-iterate-ci.yml` treat any `agent/`-prefixed head branch as theirs and
  >   dispatch fixers that push to it, so the branch has three writers while the
  >   implement agent is still committing.
  >
  > The fix for all six is written, verified and committed as
  > `docs/tasks/active/20260922-agent-implement-issue-to-pr-blocked.diff`: two App
  > tokens so the agent holds no `pull-requests` scope, `require_last_push_approval`
  > on both protection paths, the missing declaration, the pre-flight on both entry
  > points, the issue text taken from the immutable event payload, and the PR opened
  > by a trusted step after the agent stops. It is not applied because **no
  > credential in this pipeline can push `.github/workflows/**`** — every agent App
  > token is minted without `workflows`, which `checks.test.mjs` asserts and which
  > is the property `agent-fix.yml` prints to contributors. A maintainer has to
  > apply it.

  **What it does not cover, even once that patch is applied.** The agent runs
  with an unrestricted `Bash` beside a live installation token and a model
  credential, on text an arbitrary GitHub user wrote. The gate constrains
  merging; it does not constrain network egress. A successful prompt injection
  does not need to get code into `main` — it already has the token. One narrowing
  is mechanical and one is only a convention, and they were previously written as
  though both were the first kind. **Enforced (once patched):** the issue title
  and body come from the event-payload snapshot, which is the text as it stood
  when the maintainer's comment was created, written to a file the prompt names
  as untrusted input. **Not enforced, ever:** the prompt tells the agent not to
  run `gh issue view` and not to read the issue's comments, and nothing stops it
  — `--allowedTools` includes `Bash` and the agent holds a token `gh`
  authenticates with, so a comment posted mid-run is reachable by an agent that
  decides to look. It narrows the honest agent's behaviour and bounds nothing.
  Neither is a control on egress, and the residual risk is accepted knowingly:
  the token is installation-scoped to this repository, expires in an hour, and
  carries no `workflows` permission. The control that would actually close the
  channel is a runner egress policy, and it is the next thing to add here.

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
| Defer issue → PR indefinitely | It originates work. Every phase here reviews work that a human already decided to do |

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
