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
# WHAT IS COMPARED, and why it is provenance rather than a file list. The
# tempting spelling is "refuse when `Makefile` or `.golangci.yml` differ from
# upstream". That closes two of the three paths and leaves the widest one open:
# `go test ./...` executes every `_test.go` file in the tree, so any branch at
# all supplies code to pre-push. There is no file list to pin — the gate's
# whole job is to run the tree. The property that actually separates "my work"
# from "a pull request I am reviewing" is where the commits this checkout
# carries on top of the upstream default branch CAME FROM, so that is what is
# checked.
#
# AND "CAME FROM" IS NOT THE AUTHOR LINE. An earlier revision of this file
# compared `%aE` against the local `user.email`, which is not an authentication
# decision at all: the author address is a field the branch's own author writes,
# so `git config user.email maintainer@example.com` before committing walked
# straight through the gate, and `.mailmap` — also branch-supplied, also
# consulted by `%aE` — could rewrite it after the fact. The address is a label,
# not a credential.
#
# The credential is HEAD's reflog. It lives in `$GIT_DIR`, it is written by the
# local git as it moves HEAD, and no content a fetched branch carries can add an
# entry to it. A commit this clone CREATED has a reflog entry whose action is a
# commit-creating one (`commit`, `commit (amend)`, `rebase (pick)`, `merge`,
# `cherry-pick`, `revert`, `am`); a commit this clone merely RECEIVED is known
# only through `clone:`, `fetch`, `checkout:`, `reset:` or a `Fast-forward`,
# which is exactly the `gh pr checkout` case being refused. The author check is
# kept underneath it, because a mismatched address is still worth naming in the
# refusal — but it is the second condition, never the only one.
#
# Commits reachable from the upstream ref are trusted by construction: they are
# on the default branch, which is what reviewing a pull request produces. So a
# branch rebased onto — or merged with — a fetched `main` does not trip this.
#
# THE COST, stated because it is real: a commit you wrote on another machine and
# fetched into this clone was not created here, so this refuses it. That is the
# same evidence a stranger's commit presents, and the bypass below is the answer
# — an explicit one, which is the point.

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

# Echo the OIDs this clone CREATED, one per line.
#
# `%gs` is the reflog subject, whose first word is the action. Only the
# commit-creating actions count: `checkout:`, `reset:`, `clone:`,
# `rebase (start):`, and any entry ending in `Fast-forward` all move HEAD onto a
# commit that arrived from somewhere else, so an OID known only through those is
# precisely what this refuses. An unrecognised action is not creating, so a
# future git spelling fails closed rather than open.
yorkie_locally_created() {
  git reflog show HEAD --format='%H %gs' 2>/dev/null | awk '
    /Fast-forward$/ { next }
    $2 == "rebase" && $3 == "(start):" { next }
    $2 ~ /^(commit|rebase|merge|cherry-pick|revert|am|applypatch)/ { print $1 }
  '
}

# Refuse to hand the working tree to `make` unless every commit this checkout
# carries on top of upstream was created by this clone, under the identity
# configured here.
#
# FAILS CLOSED on every way the question cannot be answered — no upstream ref,
# no `user.email`, an unreadable commit range — because "I could not tell whose
# code this is" and "it is yours" must not share an answer. The first two are
# one command away from fixed (`git fetch origin main`, `git config
# user.email`) and both are named in the refusal.
yorkie_require_own_work() {
  local hook="$1" runs="$2" upstream me commits untrusted

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

  # Enumerated in its own command, and its status checked, because the previous
  # spelling put `git log` at the head of a pipeline: a range that failed to
  # resolve produced no output, no output read as "no foreign commits", and the
  # gate passed on the error path.
  if ! commits=$(git log --format='%H %aE' "$upstream..HEAD" 2>/dev/null); then
    echo "$hook: could not list this branch's commits against" >&2
    echo "        ${upstream#refs/remotes/}, so there is no way to tell whose code" >&2
    echo "        $runs would run. See the bypass below." >&2
    yorkie_print_bypass "$hook"
    return 1
  fi
  # An empty range — HEAD at or behind upstream — has nothing to distrust.
  if [ -z "$commits" ]; then
    return 0
  fi

  # `%aE` is the mailmap-resolved author address, compared case-insensitively
  # because git preserves the case a commit was made with and addresses are not
  # case-sensitive in practice. A commit with no author address at all
  # (`--author='A U Thor <>'`, which git accepts) yields an empty field: it is
  # reported as untrusted rather than silently compared equal to nothing, which
  # is how the earlier `grep -vFx` pipeline let it through.
  # The two lists are concatenated around a separator rather than passed as two
  # awk files, because the idiomatic `NR == FNR` split silently misreads an
  # EMPTY first file — and an empty first file is the interesting case here: a
  # clone whose reflog created nothing would have its first commit swallowed
  # into the created set and walk through. `--` cannot collide with an OID.
  untrusted=$(
    {
      yorkie_locally_created
      echo '--'
      printf '%s\n' "$commits"
    } | awk -v me="$me" '
      !past && $0 == "--" { past = 1; next }
      !past { if ($0 != "") created[$0] = 1; next }
      {
        if (!($1 in created)) { printf "  %s  not created by this clone\n", substr($1, 1, 9); next }
        if (NF < 2 || $2 == "") { printf "  %s  commit has no author address\n", substr($1, 1, 9); next }
        if (tolower($2) != tolower(me)) { printf "  %s  authored by %s\n", substr($1, 1, 9), $2 }
      }
    '
  )
  if [ -n "$untrusted" ]; then
    echo "$hook: this checkout carries commits on top of ${upstream#refs/remotes/} that" >&2
    echo "        this clone did not write:" >&2
    while IFS= read -r line; do
      echo "      $line" >&2
    done <<<"$untrusted"
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
