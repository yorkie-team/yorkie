#!/usr/bin/env bash
set -euo pipefail

# Claude Code SessionStart hook: state the workflow up front.
#
# CLAUDE.md carries this already, in a Task Workflow section 70 lines down.
# Putting the load-bearing half in context at session start is more reliable
# than relying on the file being read that far — and the first step is the one
# most often skipped, because it is the one that happens before any code.
#
# Non-blocking by construction: prints and exits 0. Nothing here can refuse a
# session.

cat <<'EOF'
=== WORKFLOW REQUIREMENTS (CLAUDE.md) ===
1. Plan first. Write docs/tasks/active/YYYYMMDD-<slug>-todo.md BEFORE code.
   Architecture changes also update docs/design/.
2. Branch from main. `make verify` (lint, licence headers, unit tests)
   green per commit; the pre-commit and pre-push hooks check this for you.
3. Commit subject <=70 chars, verb-first, no type prefix; blank line 2;
   body wrapped at 80.
4. Self review with /self-review before opening the PR: max 3 rounds,
   stop at the first round with no blocking findings. Log rounds in
   the task's *-lessons.md.
5. Before merge: `bash scripts/tasks-archive.sh && bash scripts/tasks-index.sh`.
6. Never hand-edit api/yorkie/v1/*.pb.go — change the .proto, `make proto`.
EOF
