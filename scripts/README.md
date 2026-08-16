# Scripts

Repository automation: the task-doc tooling, the one-time clone setup, and the
CI helpers. None of it is part of the Go module — these run by hand, from
`.githooks/`, or from `.github/workflows/ci.yml`.

## Task docs

| Script | Invoked as | Role |
|---|---|---|
| `tasks-archive.sh` | `bash scripts/tasks-archive.sh` | Moves completed task pairs from `docs/tasks/active/` into `docs/tasks/archive/YYYY/MM/`, bucketed by each todo's `**Created**` line. Eligibility is decided by unchecked boxes alone, so read a todo's Review section before trusting the result. |
| `tasks-index.sh` | `bash scripts/tasks-index.sh` | Regenerates `docs/tasks/README.md` and `docs/tasks/archive/README.md`. Never hand-edit those two. `docs/tasks/active/README.md` is hand-written prose and is left alone. |

Both take an optional tasks directory argument, defaulting to `docs/tasks`.

## Setup

| Script | Invoked as | Role |
|---|---|---|
| `setup.sh` | `bash scripts/setup.sh` | Points `core.hooksPath` at `.githooks/`. Run once per clone. |

## Directories

| Directory | Contents |
|---|---|
| [`ci/`](ci/) | Helpers for the benchmark and load-test jobs in `.github/workflows/ci.yml`. `parse-bench.js` diffs Go benchmark output against the base run, `parse-load.js` does the same for k6 output, and both render a markdown comparison table. `post-comment.sh` posts that table on the PR, updating its previous comment in place via an HTML marker instead of stacking a new one per run. |
