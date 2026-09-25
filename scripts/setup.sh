#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
# The COMMON git dir, not `--absolute-git-dir`: in a linked worktree the latter
# is `.git/worktrees/<name>`, while `core.hooksPath` is shared config. A
# snapshot there dies with the worktree, and git then runs no hooks at all —
# silently, since a missing hooks directory is not an error to git.
GIT_COMMON=$(cd "$(git rev-parse --git-common-dir)" && pwd -P)

# THE RE-RUN IS THE VECTOR THE SNAPSHOT DOES NOT COVER BY ITSELF. Everything
# below copies the CURRENT worktree's hook sources into `$GIT_DIR`, where no
# later checkout can dislodge them. Run once on `main` that is the property we
# want; run again inside a checkout of somebody's pull request — and the docs
# do tell people to re-run this to pick up improved hooks — and that branch's
# `pre-commit`, `pre-push` and `scripts/hooks/*.sh` become the permanent,
# checkout-proof hooks of the clone.
#
# So: compare the hook sources against the upstream default branch and refuse
# when they differ. The escape hatch is an environment variable rather than a
# prompt, because this script is also run non-interactively, and it names the
# decision out loud — a maintainer editing the hooks means it; a reviewer who
# ran `gh pr checkout` almost never does.
#
# WHAT IS COMPARED IS WHAT THIS SCRIPT RUNS, not what it is named after — and
# the first version of this list got that wrong. It compared `.githooks`,
# `scripts/hooks` and `scripts/setup.sh`, while the last line below executes
# `scripts/hooks/install.mjs`, which imports `../direct-run.mjs`. A branch whose
# only change under the trust surface was that one file passed the guard and ran
# its code on the reviewer's machine: arbitrary execution, one import away from
# the pattern that named only the importers. `scripts/agent/checks.mjs` had
# already written that exact lesson down for the CI lane (`scripts/*.mjs`
# rather than `scripts/verify-*.mjs`, for the same import); it was not carried
# here.
#
# So the glob, which covers every sibling module a future import could reach.
# `:(glob)` magic because git's default pathspec `*` spans `/` — without it the
# entry would silently pull in all of `scripts/agent/**` as well. The cost is a
# false refusal on a branch that edits a verify script without touching a hook;
# that is the safe direction and the escape hatch below is one line.
#
# Everything else the branch changed stays out of scope: it is irrelevant to
# what gets PERSISTED here, and a whole-tree comparison would refuse on every
# topic branch, which is how a guard gets exported to /dev/null.
HOOK_SOURCES=(.githooks scripts/hooks scripts/setup.sh ':(glob)scripts/*.mjs')

UPSTREAM_REF=""
for ref in refs/remotes/origin/main refs/remotes/origin/HEAD; do
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref" >/dev/null; then
    UPSTREAM_REF="$ref"
    break
  fi
done

if [ -z "$UPSTREAM_REF" ]; then
  echo "setup: no origin/main to compare the hook sources against; installing this" >&2
  echo "       worktree's copies as-is." >&2
elif ! git -C "$REPO_ROOT" diff --quiet "$UPSTREAM_REF" -- "${HOOK_SOURCES[@]}"; then
  if [ "${YORKIE_ALLOW_LOCAL_HOOKS:-}" != "1" ]; then
    echo "setup: this worktree's hook sources differ from ${UPSTREAM_REF#refs/remotes/}:" >&2
    git -C "$REPO_ROOT" diff --stat "$UPSTREAM_REF" -- "${HOOK_SOURCES[@]}" >&2
    echo >&2
    echo "       Installing would snapshot THESE copies into \$GIT_DIR, where no later" >&2
    echo "       checkout can replace them. If this is a branch you are reviewing rather" >&2
    echo "       than one you wrote, that is not what you want." >&2
    echo "       Re-run on the default branch, or, if you meant it:" >&2
    echo "         YORKIE_ALLOW_LOCAL_HOOKS=1 bash scripts/setup.sh" >&2
    exit 1
  fi
  echo "setup: hook sources differ from ${UPSTREAM_REF#refs/remotes/}; installing them" >&2
  echo "       anyway because YORKIE_ALLOW_LOCAL_HOOKS=1." >&2
fi

# SNAPSHOTTED, NOT POINTED AT THE WORKTREE, and for the same reason
# `scripts/hooks/install.mjs` snapshots the Claude Code hooks — its header
# carries the long form of this argument.
#
# This used to name the tracked hook directory directly. That makes the hook
# SCRIPT branch-controlled code: a pull request can rewrite `pre-commit`, and a
# reviewer who checks the branch out and commits anything runs whatever it now
# says. Copying into `$GIT_DIR` puts the scripts where `git checkout` never
# writes, so the hook that runs is the one present when a human ran setup,
# whatever branch the worktree is on. The cost is staleness: an improved hook
# reaches a clone on the next run of this script. The directory is wiped first
# so a hook deleted upstream stops running here too.
#
# WHAT THIS DOES NOT CLOSE BY ITSELF, said plainly because an earlier version
# of this comment claimed the whole hole and closed half of it. The snapshot
# pins WHICH script runs, not WHAT it runs. `pre-commit` execs `make lint` and
# `pre-push` execs `make verify`, and both resolve through the WORKING TREE:
# the branch's `Makefile`, its `.golangci.yml`, its `go test ./...`. A gate
# that checked anything else would not be a gate.
#
# Nor can the comparison above reach it: that guard runs now, at install time,
# and the commit that hands a branch's Makefile to `make` happens later, in a
# checkout of a branch that need not exist yet. So the second half is checked
# where it has to be, inside the hooks: `.githooks/trusted-tree.sh` refuses
# when the checkout carries commits on top of `origin/main` that this clone
# did not create — read out of HEAD's reflog, not out of the author line the
# branch's own author writes — which is what checking out somebody's pull
# request produces and what writing your own does not. Bypass with
# `--no-verify` or
# `YORKIE_ALLOW_FOREIGN_TREE=1`. CONTRIBUTING.md says the same thing where
# contributors read it.
HOOKS_SNAPSHOT="$GIT_COMMON/githooks"
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
