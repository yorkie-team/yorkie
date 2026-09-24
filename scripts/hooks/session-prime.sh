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
#
# LOCAL SESSIONS ONLY. What follows is the multi-commit workflow a person (or
# an interactive agent) follows: plan a task doc, self-review, archive before
# merge. A CI fix job has its own prompt, which tells it to fix the findings it
# was given "and nothing else" — handing it this instead would push it to write
# task documents and run a review loop it was not asked for.
#
# Whether it would ever be read there is genuinely unsettled. Three workflows
# (`agent-review-panel`, `agent-review-on-demand`, `agent-implement`) delete
# `.claude/` from the branch before running, and the panel's comment says why:
# it holds "settings + hooks the SDK could load and run". The workflows that
# run `claude-code-action` against the branch without stripping it —
# `agent-fix`, `agent-iterate-ci`, `agent-review-reply`, `agent-summarize` —
# would therefore pick this up if that action loads project settings.
#
# Refusing here costs nothing and settles it either way, which is the point:
# the alternative is a behaviour change to the autonomous pipeline resting on
# an assumption nobody has checked. `guard-generated-files.sh` deliberately has
# no such exit — refusing a hand-edit to a generated file is as right in CI as
# it is locally.
#
# Written as an `if` rather than `[ ... ] && exit 0`: under `set -e` the
# short-circuit form is safe only because the test is not the last command of
# its AND-list, which is a rule worth not making the next reader recall.
if [ -n "${GITHUB_ACTIONS:-}" ]; then
  exit 0
fi

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
