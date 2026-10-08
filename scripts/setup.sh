#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)

# GIT HOOKS RUN FROM THE TRACKED `.githooks/`, the way wafflebase wires them.
# A relative `core.hooksPath` is resolved against the top of whichever
# worktree git is running in, so one setting serves every linked worktree of
# the clone, and a hook change reaches it on the next checkout with nothing to
# re-run.
#
# This used to copy the hooks into `$GIT_DIR` and point there, plus a
# `trusted-tree.sh` guard inside the hooks that refused a checkout carrying
# commits this clone did not create. Both existed so that committing in a
# checkout of an unread pull request would not run it. They cost more than they
# bought: every maintainer commit on an agent-loop branch carries bot commits,
# so every commit and push there needed `YORKIE_ALLOW_FOREIGN_TREE=1`, and the
# snapshot went stale until someone re-ran this script. The hooks only run
# `make lint` / `make verify`, which a reviewer building the branch runs
# anyway. docs/design/local-enforcement-layer.md records the decision.
git -C "$REPO_ROOT" config core.hooksPath .githooks
echo "Git hooks enabled: core.hooksPath -> .githooks"

# The Claude Code hooks are a different case and keep their `$GIT_DIR`
# snapshot: Claude Code runs what project settings name at session start,
# unconfirmed, so tracked wiring would run a branch's `scripts/hooks/*.sh` the
# moment a session opens in its checkout — no commit or push needed.
# `install.mjs` carries the full argument.
#
# THE RE-RUN WOULD PERSIST A BRANCH'S HOOKS. Run inside a checkout of somebody's
# pull request, the install below would make that branch's `scripts/hooks/*.sh`
# the clone's checkout-proof Claude Code hooks. So compare what the install
# runs and copies against the upstream default branch and refuse when it
# differs. The escape hatch is an environment variable rather than a prompt,
# because this script is also run non-interactively.
#
# This guards against ACCIDENT only. A malicious branch's setup.sh can simply
# leave the check out, and running that file is already running the branch's
# code. Run setup on the default branch.
#
# `:(glob)scripts/*.mjs` because `install.mjs` imports `../direct-run.mjs`: a
# list naming only the importers once let a branch whose sole change was that
# module run its code here. `:(glob)` magic because git's default pathspec `*`
# spans `/` and would pull in all of `scripts/agent/**`.
HOOK_SOURCES=(scripts/hooks scripts/setup.sh ':(glob)scripts/*.mjs')

# `upstream/main` first: in a fork, `origin/main` is the fork's and may lag,
# and current hook sources would then read as a local edit and be refused.
UPSTREAM_REF=""
for ref in refs/remotes/upstream/main refs/remotes/origin/main refs/remotes/origin/HEAD; do
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref" >/dev/null; then
    UPSTREAM_REF="$ref"
    break
  fi
done

if [ -z "$UPSTREAM_REF" ]; then
  echo "setup: no origin/main to compare the Claude Code hook sources against;" >&2
  echo "       installing this worktree's copies as-is." >&2
elif ! git -C "$REPO_ROOT" diff --quiet "$UPSTREAM_REF" -- "${HOOK_SOURCES[@]}"; then
  if [ "${YORKIE_ALLOW_LOCAL_HOOKS:-}" != "1" ]; then
    echo "setup: this worktree's Claude Code hook sources differ from ${UPSTREAM_REF#refs/remotes/}:" >&2
    git -C "$REPO_ROOT" diff --stat "$UPSTREAM_REF" -- "${HOOK_SOURCES[@]}" >&2
    echo >&2
    echo "       Installing would snapshot THESE copies into \$GIT_DIR, where no later" >&2
    echo "       checkout can replace them. If this is a branch you are reviewing rather" >&2
    echo "       than one you wrote, that is not what you want. The git hooks above are" >&2
    echo "       already enabled." >&2
    echo "       Re-run on the default branch, or, if you meant it:" >&2
    echo "         YORKIE_ALLOW_LOCAL_HOOKS=1 bash scripts/setup.sh" >&2
    exit 1
  fi
  echo "setup: Claude Code hook sources differ from ${UPSTREAM_REF#refs/remotes/};" >&2
  echo "       installing them anyway because YORKIE_ALLOW_LOCAL_HOOKS=1." >&2
fi

# Node is optional on this leg. `make verify-license` needs it anyway, but a
# contributor without it must still end up with the git hooks enabled, so this
# reports and moves on instead of failing the script.
if command -v node >/dev/null 2>&1; then
  node "$REPO_ROOT/scripts/hooks/install.mjs"
else
  echo "node not found; skipping Claude Code hook install (scripts/hooks/install.mjs)" >&2
fi
