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
| `direct-run.mjs` | imported, not run | `isDirectRun(import.meta.url)` — the predicate the three verify/install scripts here use to tell "run as a CLI" from "imported by the suite". Its reach stops at `agent/`, for the staging reason that row gives. Shared because it has to realpath both sides: `import.meta.url` is resolved through symlinks by the loader and `process.argv[1]` is not, so a plain string comparison makes a script invoked through a symlinked path exit 0 having checked nothing. |
| `verify-license.mjs` | `node scripts/verify-license.mjs`, or `make verify-license` | Fails on any `.go` file that does not carry the Apache 2.0 grant clause within its first 40 lines. Matches the clause alone, not the copyright year or the comment style, since the tree has years from 2020 on. Generated files are in scope — `buf generate` reproduces the header, so a plugin change that dropped it is exactly what this should catch. It also fails when it could not read part of the tree — an unlistable directory or an unopenable file is reported as the gap it is, never as a missing header. `make verify` runs it, announcing a skip if Node is absent; CI's `build` job runs it unconditionally. |
| `verify-doc-links.mjs` | `node scripts/verify-doc-links.mjs` | Walks the documentation graph from `CLAUDE.md`, `AGENTS.md`, and `README.md`, and fails on a link that resolves to nothing. Archived task records are reached but not walked — a finished task's citations are a record of what was true then. Run by the `Docs` workflow, which exists separately from `ci.yml` because that one ignores `**/*.md`. |
| `verify-doc-index.mjs` | `node scripts/verify-doc-index.mjs` | The opposite question to the one above: not "does this link resolve" but "does anything link this file". Fails on a `docs/design/*.md` that `docs/design/README.md` does not link, or a top-level `scripts/` entry this README does not name — the two hand-written indexes with a standing instruction to update them. It carries a SECOND, non-gating check: a todo in `docs/tasks/active/` with every box ticked is a finished task nobody archived, which coverage cannot see (the index and the filesystem agree, and both say "active"). That one only reports, because `CLAUDE.md` archives at merge — so it is the normal state of a pull request that has just finished its work. Run by the `Docs` workflow. |

## Setup

| Script | Invoked as | Role |
|---|---|---|
| `setup.sh` | `bash scripts/setup.sh` | Installs both hook systems for this clone. Copies `.githooks/` into `$GIT_DIR/githooks` and points `core.hooksPath` there — `commit-msg` (message shape), `pre-commit` (`make lint`), `pre-push` (`make verify`) — then runs `hooks/install.mjs` for the Claude Code hooks. Both are snapshots rather than the worktree, so a branch cannot supply the hook SCRIPT that runs on a reviewer's machine; what those scripts then invoke — `make lint`, `make verify` — is still the working tree's, which is why `.githooks/trusted-tree.sh` refuses when the checkout carries commits on top of `origin/main` that this clone did not create, judged from HEAD's reflog rather than from the branch-supplied author line. Re-run it to pick up hook changes. |

## Directories

| Directory | Contents |
|---|---|
| [`agent/`](agent/) | The `@claude` command surface: the verb router (`command.mjs`), the review-panel orchestrator and its lenses, and the credential, state, metrics and comment helpers the `agent-*.yml` workflows call. A separate npm package — its own lockfile, its own dependencies, its own `node --test` suite (`cd scripts/agent && npm test`) — and nothing in the Go build reaches it. `docs/design/agent-command-verbs.md` is the design. **Nothing in here may import outside this directory**, `../direct-run.mjs` included: every workflow stages the package detached from its parent, either with `sparse-checkout: scripts/agent` and `sparse-checkout-cone-mode: false` (cone mode off makes the pattern literal, so `scripts/` itself is never written) or with `cp -R scripts/agent "$RUNNER_TEMP/agent-tools"`. Six workflows go further and check `command.mjs` out on its own. A `../` import is therefore `ERR_MODULE_NOT_FOUND` at the top of a job; `checks.test.mjs` pins both rules. The reverse direction is fine — `test/harness-hooks.test.mjs` imports `agent/git-env.mjs`, and a test only runs from a full checkout. |
| [`test/`](test/) | `node --test` suites for the scripts above, run by the `Docs` workflow. Invoke with the glob — `node --test 'scripts/test/**/*.test.mjs'` — since passing the directory makes Node try to load it as a module. The verify-script cases plant their tree under the OS temp directory and shell out to nothing. `harness-hooks.test.mjs` is the exception and says why: it pins facts about *this* tree — that the guard hook refuses every generated file actually present, that every script `install.mjs` wires resolves, that no `.claude/settings.json` is tracked, that `docs.yml` still runs both unfiltered checks — and a planted copy of those would assert only that the test agrees with itself. It stays read-only. |
| [`hooks/`](hooks/) | Claude Code hooks, wired per clone by `install.mjs` (run from `setup.sh`) — a different layer from `.githooks/`, which git runs. The wiring is NOT tracked: Claude Code runs what a project settings file names without confirming, so a tracked `.claude/settings.json` would execute a pull-request branch's hooks on any checkout of it. `install.mjs` snapshots the scripts into `$GIT_DIR/agent-hooks/` and writes the gitignored `.claude/settings.local.json`, so neither the wiring nor the code a session runs comes from the branch; re-run `setup.sh` to pick up changes. `session-prime.sh` (SessionStart) states the workflow so it does not depend on CLAUDE.md being read to the end. `guard-generated-files.sh` (PreToolUse on Edit/Write) refuses an edit to the generated `*.pb.go` / `*.connect.go` and points at `make proto`; it does not guard `api/docs/**/*.openapi.yaml`, which MAINTAINING.md has a maintainer hand-edit each release. Payload arrives as JSON on stdin; exit 0 allows, exit 2 blocks and returns stderr to Claude. |
| [`ci/`](ci/) | Helpers for `.github/workflows/ci.yml`. The lane runner and its two readers are below; alongside them, `parse-bench.js` diffs Go benchmark output against the base run, `parse-load.js` does the same for k6 output, and both render a markdown comparison table. `post-comment.sh` posts that table on the PR, updating its previous comment in place via an HTML marker instead of stacking a new one per run. |

## CI lanes

Design: [`docs/design/ci-lane-reports.md`](../docs/design/ci-lane-reports.md).
These exist so the autonomous CI-fix loop is handed a diagnosis that names the
failing test rather than the last 40 KB of a build log.

| Script | Invoked as | Role |
|---|---|---|
| `ci/run-lanes.mjs` | `node scripts/ci/run-lanes.mjs <lane>` from each check step in `ci.yml`'s `build` job; `node scripts/ci/run-lanes.mjs` with no arguments locally | Runs each of that job's checks as a named lane and writes `.ci-reports/lane-<name>.json`. Every lane's command is the literal string the `ci.yml` step it replaced ran — this changes how a lane is REPORTED, never what it runs, which is what keeps `review-panel.mjs`'s `MECHANICAL_COVERAGE_NOTE` true. `--finish` rewrites `summary.json` over the whole manifest, so a lane that never ran is stated as `skip` (an earlier lane failed) or `filtered` (a precondition did not hold) rather than omitted. `--list` prints the lane names. Run with no arguments it executes the whole manifest in order and stops at the first failure, which is the local reproduction of the `build` job. |
| `ci/lane-failure.mjs` | imported, not run | Turns a failed lane's captured output into ONE line naming what failed. Per output kind, with an explicit precedence order: a package that did not compile cannot have a failing test, a data race outranks the `--- FAIL:` it causes, a panic makes every test after it collateral. Pure — no filesystem, no network, no child process — so the parsing can be tested exhaustively without a Go toolchain and importing it cannot run a build. |
| `ci/summarize-ci.mjs` | `node scripts/ci/summarize-ci.mjs --dir <path>`, from `agent-iterate-ci.yml` | Renders the reports into the block the CI-fix agent reads. Writes NOTHING to stdout and exits 3 when no lane failed — a missing artifact, a truncated one, or a red run whose failure was outside the lanes. The consumer keys its `gh run view --log-failed` fallback off exactly that empty capture, so the fixer is never dispatched with an empty diagnosis. |

Reports land in `.ci-reports/` (gitignored). Deliberately **not**
`.harness-reports/`, which is the upstream name for this artifact but is
already a scratch directory `scripts/agent/novelty.test.mjs` plants throwaway
git repositories under — sharing it would put a nested `.git` inside the
artifact upload's search root.
