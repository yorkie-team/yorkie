# Move the harness's repository-specific values into configuration

**Created**: 2026-11-13

## Problem

`scripts/agent/` and the `agent-*.yml` workflows are copied between
repositories and kept in step by hand. Most of what differs between the
copies is not behaviour but values written into the code: the paths that
define CI, the file-class rules, what the mechanical lanes prove, which
directories are source, the gate label, the bot logins. One such value makes
a whole file a different file, so every fix has to be carried across by hand.

## Goal

Every repository-specific value lives in `.github/agent-harness/` and is read
through `scripts/agent/config.mjs`, so the scripts can be the same bytes
everywhere. No behaviour change for this repository, with one stated
exception: `.github/agent-harness/**` becomes a CI-defining path.

The workflow prompts and toolchain steps are not rewired here.

## Plan

- [x] Record what the modules export today and pin it
      (`.github/agent-harness/repo.snapshot.json`, `repo.test.mjs`)
- [x] `config.mjs`: load, validate, throw on anything missing or unknown;
      `node config.mjs get <key>`
- [x] `config.json` and `coverage-note.md` with this repository's values
- [x] Scripts read the configuration; comments that narrated this
      repository's build describe the mechanism instead
- [x] The package's tests run on a fixture configuration; assertions about
      this repository move to `repo.test.mjs`, which `agent-scripts.yml` runs
- [x] Every trusted checkout and staged copy of `scripts/agent` brings
      `.github/agent-harness` from the same ref
- [x] `config-wiring.test.mjs`: walk every script invocation in the workflows
      and require a trusted tree at that step; count the sites; keep the
      trust-weakening mutations as cases
- [x] One glob dialect, validated; `$config:` references and
      `{{config:key}}` placeholders throw on anything malformed
- [x] `.github/agent-harness/**` is CI-defining and policy
- [x] `scripts/agent/README.md`: keys, which are wired, conventions assumed
- [x] `npm test`, `npm run test:repo`, actionlint, `make verify`, Node 22
- [ ] Review rounds on the pull request
- [ ] Merge with the pipeline paused (see the pull request's "Merging")

## Not in this change

- Workflow prompts and diff commands still carry `make verify`, the
  generated-code excludes and the reproducer convention as literals. Twelve
  configuration keys record those values and are read by nothing yet.
- The toolchain setup steps are unchanged.
- Bot logins written inline in workflow `github-script` steps stay; a test
  pins them equal to `repo.botLogins`.
