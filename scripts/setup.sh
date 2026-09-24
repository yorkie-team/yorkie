#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)

git config core.hooksPath "$REPO_ROOT/.githooks"
echo "Git hooks path set to .githooks/"

# Claude Code hooks, for whoever uses it. Deliberately an explicit act here
# rather than a tracked `.claude/settings.json`: project settings are loaded
# and their commands run straight out of the working tree, so tracked wiring
# would execute a pull-request branch's `scripts/hooks/*.sh` on any checkout of
# it. `install.mjs` snapshots the scripts into `$GIT_DIR` and writes the
# gitignored `settings.local.json` — its header carries the full argument.
#
# Node is optional on this leg. `make verify-license` needs it anyway, but a
# contributor without it must still end up with the git hooks installed, so
# this reports and moves on instead of failing the script.
if command -v node >/dev/null 2>&1; then
  node "$REPO_ROOT/scripts/hooks/install.mjs"
else
  echo "node not found; skipping Claude Code hook install (scripts/hooks/install.mjs)" >&2
fi
