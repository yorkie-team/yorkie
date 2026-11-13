# Make the agent workflows read the repository's values from configuration

**Created**: 2026-11-14

## Problem

The `agent-*.yml` workflows are copied between repositories like the scripts
were. After the scripts moved their repository-specific values into
`.github/agent-harness/`, the workflows still carried them as text: the
toolchain setup steps, `make verify` and what it covers in three prompts, the
generated-code excludes at three diff sites, the App's name in five decline
messages, the bot allow-list in five inline scripts. One such line makes a
whole workflow a different file in each repository.

## Goal

A repository with another toolchain can take this repository's `agent-*.yml`
unchanged. Everything of this repository's lives in `.github/agent-harness/`
and in one composite action, `.github/actions/agent-toolchain`. No behaviour
change here beyond the ones listed in the pull request.

## Plan

- [x] `workflow-values.mjs`: the one way a workflow reads a configured value,
      as step outputs asked for by name; refuses an unknown name or a blank
      required value
- [x] `fixer.*` reshaped into the sentences the prompts actually need; `repo.appSlug`
- [x] Prompts, decline messages, allow-lists, `allowed_bots`, diff pathspecs
      and the lane-report switch read from it
- [x] `.github/actions/agent-toolchain`, used from `main`'s copy in a side
      directory wherever the workspace is a pull request's branch
- [x] `config-wiring.test.mjs`: a local action is checked like a script; a
      checkout with no `ref:` is not trusted under `workflow_dispatch`
- [x] `workflow-values.test.mjs`: every `outputs.<name>` read was asked for,
      by a step whose value can reach it
- [x] Port from the other repository: the protection check in its own
      directory, the generated-code rule in the three fixer prompts, the
      optional review-reply verify clause and todo tracking line
- [x] Rendered prompts recorded in `repo.snapshot.json`; compared with the
      base commit's prompts before recording
- [x] Repository-shaped assertions moved from the package's tests to
      `repo.test.mjs`
- [x] `npm test`, `npm run test:repo`, actionlint, `scripts/test`, `make verify`
- [ ] A live run of each of the five agent jobs after merge (the composite
      action and the multi-line step outputs have not run on a runner)
- [ ] Review rounds on the pull request
- [ ] Merge with the pipeline paused (see the pull request's "Merging")

## Not in this change

- `redact.extraPatterns` and `ask.permittedMcpTools` are still read by nothing.
- `agent-scripts.yml` cannot take path filters from configuration, so a change
  to a file only `repo.test.mjs` reads (the Makefile, `go.mod`) does not start
  that lane.
- Comments that cite this repository's incidents by number stay as history.
