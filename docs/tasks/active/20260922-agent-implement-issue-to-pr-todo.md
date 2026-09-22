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
a **draft** PR back to `main`. A bare `@claude` with no verb on an issue gets a
`help` reply instead of silence.

## Scope

- [ ] `.github/workflows/agent-implement.yml`, ported from wafflebase and
      adapted to the Go layout
- [ ] `scripts/agent/classify.mjs` + tests, with the model call moved from the
      raw Messages API onto `ask.mjs` (OAuth, not an API key)
- [ ] The ten `agent:*` labels created deliberately rather than auto-created
- [ ] `docs/design/agent-command-verbs.md`: Non-Goal → a phase, with the
      reordering and its risk stated
- [ ] Tests: the ISSUE-only guard already waiting in `checks.test.mjs`, plus
      whatever the port needs

## Adaptations from upstream

| Upstream | Here | Why |
| --- | --- | --- |
| pnpm + Node toolchain | Go toolchain, Node only for `scripts/agent` | different repo |
| `pnpm verify:*` | `make lint`, `go test ./...` | `make test` needs MongoDB; CI owns it |
| `ANTHROPIC_API_KEY` + `x-api-key` | `ask.mjs` + `CLAUDE_CODE_OAUTH_TOKEN` | this repo has no API key, and `ask.mjs` already carries the read-only tool invariant and the credential pool |
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
