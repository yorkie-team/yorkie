# Scripts

Repository automation: the task-doc tooling, the one-time clone setup, and the
CI helpers. None of it is part of the Go module — these run by hand, from
`.githooks/`, or from `.github/workflows/ci.yml`.

## Task docs

| Script | Invoked as | Role |
|---|---|---|
| `tasks-archive.sh` | `bash scripts/tasks-archive.sh` | Moves completed task pairs from `docs/tasks/active/` into `docs/tasks/archive/YYYY/MM/`, bucketed by each todo's `**Created**` line. A todo has to clear two bars to move: no unchecked boxes, and a parseable `**Created**` date — a todo missing the date is warned about and left alone. Neither bar reads the prose, so check a todo's Review section before trusting the result. |
| `tasks-index.sh` | `bash scripts/tasks-index.sh` | Regenerates `docs/tasks/README.md` and `docs/tasks/archive/README.md`. Never hand-edit those two. `docs/tasks/active/README.md` is hand-written prose and is left alone. |

Both take an optional tasks directory argument, defaulting to `docs/tasks`.

## Verification

| Script | Invoked as | Role |
|---|---|---|
| `verify-doc-links.mjs` | `node scripts/verify-doc-links.mjs` | Walks the documentation graph from `CLAUDE.md`, `AGENTS.md`, and `README.md`, and fails on a link that resolves to nothing. Archived task records are reached but not walked — a finished task's citations are a record of what was true then. Run by the `Docs` workflow, which exists separately from `ci.yml` because that one ignores `**/*.md`. |

## Setup

| Script | Invoked as | Role |
|---|---|---|
| `setup.sh` | `bash scripts/setup.sh` | Points `core.hooksPath` at `.githooks/`. Run once per clone. |

## Directories

| Directory | Contents |
|---|---|
| [`test/`](test/) | `node --test` suites for the scripts above, run by the `Docs` workflow. Invoke with the glob — `node --test 'scripts/test/**/*.test.mjs'` — since passing the directory makes Node try to load it as a module. Every case plants its tree under the OS temp directory and shells out to nothing. |
| [`ci/`](ci/) | Helpers for the benchmark and load-test jobs in `.github/workflows/ci.yml`. `parse-bench.js` diffs Go benchmark output against the base run, `parse-load.js` does the same for k6 output, and both render a markdown comparison table. `post-comment.sh` posts that table on the PR, updating its previous comment in place via an HTML marker instead of stacking a new one per run. |
