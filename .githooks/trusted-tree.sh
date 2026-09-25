#!/usr/bin/env bash

# Sourced by pre-commit and pre-push. Not a hook itself — git runs hooks by
# name, so a sibling file in the hooks directory is inert.
#
# WHY THIS EXISTS. `scripts/setup.sh` snapshots the hook SCRIPTS into
# `$GIT_DIR` so a branch cannot rewrite them, and that closes exactly half the
# hole: the snapshot pins which script runs, not what the script invokes.
# `make lint` resolves through the working tree's `Makefile` and
# `.golangci.yml` (whose `linters.custom` can name a loadable plugin), and
# `make verify` additionally runs `go test ./...`, which compiles and executes
# the branch's own `TestMain`. So before this file existed, `gh pr checkout`
# followed by a single commit ran an unread branch's code — a surface the
# hooks themselves introduced, since the only hook before them checked the
# shape of a commit message.
#
# Extending `setup.sh`'s source comparison does not reach this: that guard runs
# at install time, and the dangerous commit happens later, in a checkout of a
# branch that did not exist when setup ran. The check has to be at hook-run
# time.
#
# WHAT IS COMPARED, and why it is authorship rather than a file list. The
# tempting spelling is "refuse when `Makefile` or `.golangci.yml` differ from
# upstream". That closes two of the three paths and leaves the widest one open:
# `go test ./...` executes every `_test.go` file in the tree, so any branch at
# all supplies code to pre-push. There is no file list to pin — the gate's
# whole job is to run the tree. The property that actually separates "my work"
# from "a pull request I am reviewing" is who wrote the commits this checkout
# carries on top of the upstream default branch, so that is what is checked.
#
# Commits reachable from the upstream ref are trusted by construction: they are
# on the default branch, which is what reviewing a pull request produces. So a
# branch rebased onto — or merged with — a fetched `main` does not trip this.

# Echo the upstream default-branch ref, or fail if the clone has none.
yorkie_upstream_ref() {
  local ref
  for ref in refs/remotes/origin/main refs/remotes/origin/HEAD; do
    if git rev-parse --verify --quiet "$ref" >/dev/null; then
      printf '%s\n' "$ref"
      return 0
    fi
  done
  return 1
}

# Refuse to hand the working tree to `make` unless every commit this checkout
# carries on top of upstream was written by the identity configured here.
#
# FAILS CLOSED on the two ways the question cannot be answered — no upstream
# ref, no `user.email` — because "I could not tell whose code this is" and "it
# is yours" must not share an answer. Both are one command away from fixed
# (`git fetch origin main`, `git config user.email`) and both are named in the
# refusal.
yorkie_require_own_work() {
  local hook="$1" runs="$2" upstream me foreign

  if [ "${YORKIE_ALLOW_FOREIGN_TREE:-}" = "1" ]; then
    return 0
  fi

  if ! upstream=$(yorkie_upstream_ref); then
    echo "$hook: no origin/main to tell your commits from a branch you are" >&2
    echo "        reviewing, and $runs runs this tree's code. Fetch it with" >&2
    echo "        'git fetch origin main', or see the bypass below." >&2
    yorkie_print_bypass "$hook"
    return 1
  fi

  me=$(git config --get user.email || true)
  if [ -z "$me" ]; then
    echo "$hook: no user.email is configured, so there is no identity to" >&2
    echo "        compare this branch's commits against. Set one with" >&2
    echo "        'git config user.email you@example.com', or see the bypass" >&2
    echo "        below." >&2
    yorkie_print_bypass "$hook"
    return 1
  fi

  # `%aE` is the mailmap-resolved author address; lowercased on both sides
  # because git preserves the case a commit was made with and addresses are not
  # case-sensitive in practice. An empty range — HEAD at or behind upstream —
  # produces no output and passes.
  foreign=$(
    git log --format='%aE' "$upstream..HEAD" |
      tr '[:upper:]' '[:lower:]' | sort -u |
      grep -vFx "$(printf '%s' "$me" | tr '[:upper:]' '[:lower:]')" || true
  )
  if [ -n "$foreign" ]; then
    echo "$hook: this checkout carries commits on top of ${upstream#refs/remotes/} written by" >&2
    while IFS= read -r address; do
      echo "          $address" >&2
    done <<<"$foreign"
    echo "        and $runs runs the WORKING TREE's code: that branch's Makefile," >&2
    echo "        its .golangci.yml, its test files. If you checked this branch out to" >&2
    echo "        review it, that is not what you want." >&2
    yorkie_print_bypass "$hook"
    return 1
  fi

  return 0
}

yorkie_print_bypass() {
  local hook="$1" verb="commit"
  if [ "$hook" = "pre-push" ]; then
    verb="push"
  fi
  echo "        Skip the gate with 'git $verb --no-verify', or, having read the diff:" >&2
  echo "          YORKIE_ALLOW_FOREIGN_TREE=1 git $verb ..." >&2
}
