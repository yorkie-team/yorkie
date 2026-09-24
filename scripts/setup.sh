#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
GIT_DIR=$(git rev-parse --absolute-git-dir)

# SNAPSHOTTED, NOT POINTED AT THE WORKTREE, and for the same reason
# `scripts/hooks/install.mjs` snapshots the Claude Code hooks — its header
# carries the long form of this argument.
#
# This used to name the tracked hook directory directly. That makes every hook
# branch-controlled code: a pull request can rewrite `pre-commit`, and a
# reviewer who checks the branch out and commits anything runs it. These hooks
# invoke `make lint` and `make verify`, so they reach the branch's Makefile and
# its Go test code too. Reviewing a patch became running it — the precise
# threat the Claude-hook installer inverts its own design to close, and it
# would have been inconsistent to close one and leave the other.
#
# Copying into `$GIT_DIR` puts them where `git checkout` never writes. The cost
# is the same staleness: an improved hook reaches a clone on the next run of
# this script. The directory is wiped first so a hook deleted upstream stops
# running here too.
HOOKS_SNAPSHOT="$GIT_DIR/githooks"
rm -rf "$HOOKS_SNAPSHOT"
mkdir -p "$HOOKS_SNAPSHOT"
cp "$REPO_ROOT/.githooks/"* "$HOOKS_SNAPSHOT/"
chmod +x "$HOOKS_SNAPSHOT/"*

git config core.hooksPath "$HOOKS_SNAPSHOT"
echo "Git hooks installed from .githooks/ into $HOOKS_SNAPSHOT"

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
