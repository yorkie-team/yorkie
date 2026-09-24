# Scripts

Repository automation: the task-doc tooling, the one-time clone setup, and the
CI helpers. None of it is part of the Go module — these run by hand, from
`.githooks/`, or from `.github/workflows/ci.yml`.

## Task docs

| Script | Invoked as | Role |
|---|---|---|
| `tasks-archive.sh` | `bash scripts/tasks-archive.sh` | Moves finished todos from `docs/tasks/active/` into `docs/tasks/archive/YYYY/MM/`, bucketed by each todo's `**Created**` line. A todo has to clear two bars: no unchecked boxes, and a parseable `**Created**` date — one missing the date is warned about and left alone. A matching `-lessons.md` rides along if it exists; a todo without one still moves. Neither bar reads the prose, so check a todo's Review section before trusting the result. |
| `tasks-index.sh` | `bash scripts/tasks-index.sh` | Regenerates `docs/tasks/README.md` and `docs/tasks/archive/README.md`. Never hand-edit those two. `docs/tasks/active/README.md` is hand-written prose and is left alone. |

Both take an optional tasks directory argument, defaulting to `docs/tasks`.

## Verification

| Script | Invoked as | Role |
|---|---|---|
| `direct-run.mjs` | imported, not run | `isDirectRun(import.meta.url)` — the predicate both verify scripts use to tell "run as a CLI" from "imported by the suite". Shared because it has to realpath both sides: `import.meta.url` is resolved through symlinks by the loader and `process.argv[1]` is not, so a plain string comparison makes a script invoked through a symlinked path exit 0 having checked nothing. |
| `verify-license.mjs` | `node scripts/verify-license.mjs`, or `make verify-license` | Fails on any `.go` file that does not carry the Apache 2.0 grant clause within its first 40 lines. Matches the clause alone, not the copyright year or the comment style, since the tree has years from 2020 on. Generated files are in scope — `buf generate` reproduces the header, so a plugin change that dropped it is exactly what this should catch. It also fails when it could not read part of the tree — an unlistable directory or an unopenable file is reported as the gap it is, never as a missing header. `make verify` runs it, announcing a skip if Node is absent; the `Docs` workflow runs it unconditionally. |
| `verify-doc-links.mjs` | `node scripts/verify-doc-links.mjs` | Walks the documentation graph from `CLAUDE.md`, `AGENTS.md`, and `README.md`, and fails on a link that resolves to nothing. Archived task records are reached but not walked — a finished task's citations are a record of what was true then. Run by the `Docs` workflow, which exists separately from `ci.yml` because that one ignores `**/*.md`. |

## Setup

| Script | Invoked as | Role |
|---|---|---|
| `setup.sh` | `bash scripts/setup.sh` | Installs both hook systems for this clone. Copies `.githooks/` into `$GIT_DIR/githooks` and points `core.hooksPath` there — `commit-msg` (message shape), `pre-commit` (`make lint`), `pre-push` (`make verify`) — then runs `hooks/install.mjs` for the Claude Code hooks. Both are snapshots rather than the worktree, so a branch cannot supply code that runs on a reviewer's machine; re-run it to pick up hook changes. |

## Directories

| Directory | Contents |
|---|---|
| [`test/`](test/) | `node --test` suites for the scripts above, run by the `Docs` workflow. Invoke with the glob — `node --test 'scripts/test/**/*.test.mjs'` — since passing the directory makes Node try to load it as a module. The verify-script cases plant their tree under the OS temp directory and shell out to nothing. `harness-hooks.test.mjs` is the exception and says why: it pins facts about *this* tree — that the guard hook refuses every generated file actually present, that every script `install.mjs` wires resolves, that no `.claude/settings.json` is tracked, that `docs.yml` still runs both unfiltered checks — and a planted copy of those would assert only that the test agrees with itself. It stays read-only. |
| [`hooks/`](hooks/) | Claude Code hooks, wired per clone by `install.mjs` (run from `setup.sh`) — a different layer from `.githooks/`, which git runs. The wiring is NOT tracked: Claude Code runs what a project settings file names without confirming, so a tracked `.claude/settings.json` would execute a pull-request branch's hooks on any checkout of it. `install.mjs` snapshots the scripts into `$GIT_DIR/agent-hooks/` and writes the gitignored `.claude/settings.local.json`, so neither the wiring nor the code a session runs comes from the branch; re-run `setup.sh` to pick up changes. `session-prime.sh` (SessionStart) states the workflow so it does not depend on CLAUDE.md being read to the end. `guard-generated-files.sh` (PreToolUse on Edit/Write) refuses an edit to the generated `*.pb.go` / `*.connect.go` and points at `make proto`; it does not guard `api/docs/**/*.openapi.yaml`, which MAINTAINING.md has a maintainer hand-edit each release. Payload arrives as JSON on stdin; exit 0 allows, exit 2 blocks and returns stderr to Claude. |
| [`ci/`](ci/) | Helpers for the benchmark and load-test jobs in `.github/workflows/ci.yml`. `parse-bench.js` diffs Go benchmark output against the base run, `parse-load.js` does the same for k6 output, and both render a markdown comparison table. `post-comment.sh` posts that table on the PR, updating its previous comment in place via an HTML marker instead of stacking a new one per run. |
