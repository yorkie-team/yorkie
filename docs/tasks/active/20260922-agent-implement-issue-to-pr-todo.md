# Port `@claude fix` on an issue (issue → PR)

**Created**: 2026-09-22

## Why now, out of order

`docs/design/agent-command-verbs.md` listed this as a Non-Goal and deferred it
past every phase, on the grounds that it originates work rather than reviewing
it. That ordering is being changed deliberately: the surface should produce
something a person can look at, and issue → PR is the verb that does.

The design doc is updated in the same branch rather than left contradicting the
code — a design document the implementation has already overtaken is worse than
none.

## What it is

`@claude fix` on an ISSUE (never a PR — the PR-side `fix` is a different verb in
`agent-fix.yml`) dispatches an agent that plans, branches, implements, and opens
a **draft** PR back to `main`. Any `@claude` on an issue that is not `fix` — a bare
mention and a PR-only verb alike — gets a `help` reply instead of silence.

## Outcome: the workflow is NOT landed, the design is

The workflow was written, reviewed, and **withdrawn**. Five review lenses
returned blocking findings against it and none of them could be fixed: no
credential in this pipeline can write `.github/workflows/**`, which was measured
this round, not assumed —

```
! [remote rejected] refusing to allow a GitHub App to create or update workflow
  `.github/workflows/agent-implement.yml` without `workflows` permission
```

Deleting under that path IS permitted, which is what made withdrawal possible
where correction was not. Shipping a workflow whose own design doc said it was
"not safe to enable", with an unapplied patch beside it, was the alternative, and
two rounds of that had already failed.

What lands instead: `docs/design/agent-command-verbs.md` Phase I now specifies
the verb and carries **thirteen acceptance criteria**, one per defect the review
found, for the maintainer who writes and pushes the workflow. The three
workflow-specific guards in `checks.test.mjs` are kept and skip through
`workflow-presence.mjs`, so they re-arm by themselves the day the file exists.

## Scope

- [ ] `.github/workflows/agent-implement.yml`, ported from wafflebase and
      adapted to the Go layout — **WITHDRAWN**, see above; a maintainer lands it
      against the design doc's acceptance criteria
- [x] The `agent:*` labels created deliberately rather than auto-created —
      eight, matching `set-state.mjs`'s six states plus `agent:managed` and
      `agent:candidate`. Two upstream names (`agent:iterating`,
      `agent:needs-human-review`) are LEGACY: `set-state.mjs` strips them on
      every write, so creating them would make a stale label indistinguishable
      from one nobody set.
- [x] `docs/design/agent-command-verbs.md`: Non-Goal → a phase, with the
      reordering and its risk stated
- [x] Tests: the ISSUE-only guard already waiting in `checks.test.mjs`, plus
      the per-job PR-comment permission rule this verb is the first exception to
- [ ] NOT DOING: `scripts/agent/classify.mjs`. Eval-rig machinery the design
      doc excludes, and it needs an API key this repository does not have. If it
      returns, its model call goes through `ask.mjs` on OAuth.

## Adaptations from upstream

| Upstream | Here | Why |
| --- | --- | --- |
| pnpm + Node toolchain | Go toolchain, Node only for `scripts/agent` | different repo |
| `pnpm verify:*` | `make lint`, `go test ./...` | `make test` needs MongoDB; CI owns it |
| `ANTHROPIC_API_KEY` + `x-api-key` | the classifier is dropped | this repo has no API key; if it returns, it goes through `ask.mjs` on OAuth, not a second HTTP path |
| agent runs `gh issue view` itself | a trusted step writes the body to a file | the agent's own fetch pulled in every comment, including ones added mid-run |
| workflow-level `contents/pull-requests/issues: write` | job-level least privilege | matches every other agent workflow here |
| `create-github-app-token@v1` | pinned to a commit SHA | this action handles the App private key |
| SessionStart hook supplies the checklist | the prompt carries it | no such hook here |
| a hook blocks a commit missing the trailer | the prompt states the trailer | `.githooks/commit-msg` here checks format only |
| `WAFFLEBASE_AGENT_AUTONOMOUS` | dropped | nothing here reads it |

## Prerequisite (done)

The App needs `Administration: read` — the workflow refuses to run unless `main`
requires a human approving review, and reading that setting is what the
permission is for. Added to `yorkie-team-agent` and approved on the
installation (2026-09-22). `Workflows` stays at no access.

## Open

- The review panel has still never completed a real review, so a PR this verb
  opens is reviewed by `@claude review` (advisory) and a human, not the panel.
- Fork issues are not a case: the verb pushes a branch into this repository.
- The agent still pushes with `contents: write`, which also reaches the merge
  endpoint for an already-approved PR. Next: the agent commits, hands a git
  bundle across, and the trusted job pushes — then the agent job needs no
  write credential at all.
- The agent job still holds the App private key (to mint the narrow token) on a
  runner with passwordless `sudo`. Mint outside the agent job, or drop `sudo`
  before the agent starts.
