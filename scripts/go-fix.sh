#!/usr/bin/env bash
# Run the Go toolchain's `go fix` modernizers over every file in the
# repository, not just the default build.
#
#   scripts/go-fix.sh check   exit 1 if go fix has any rewrite pending
#   scripts/go-fix.sh apply   apply rewrites until go fix has nothing left
#
# `go fix ./...` sees only the files the default build compiles, so anything
# behind `integration`, `bench`, `complex` and the like is silently skipped.
# The tag set is read off the `//go:build` lines rather than kept as a list, so
# a new tag cannot fall outside the check.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# go fix analyses one platform at a time. linux/amd64 is what CI runs and it
# covers the amd64-only test files.
export GOOS=linux GOARCH=amd64

# Every word on a //go:build line, minus the operators.
words=$(git grep -h '^//go:build' -- '*.go' |
  sed 's|^//go:build||' | tr -c 'A-Za-z0-9_.\n' ' ' | tr -s ' ' '\n' |
  grep -v '^$' | sort -u)

if git grep -q '^//go:build.*!' -- '*.go'; then
  # Turning every tag on would exclude these files instead of covering them.
  echo "go-fix: a //go:build line negates a tag; this script cannot cover it." >&2
  git grep -n '^//go:build.*!' -- '*.go' >&2
  exit 1
fi

platforms=$(go tool dist list | tr '/' '\n' | sort -u)
tags=()
for w in $words; do
  # `ignore` marks files no build includes (generators run with `go run`), and
  # turning it on would put a `package main` beside the library it sits in.
  # `go1.N` is a version constraint the toolchain satisfies on its own.
  case "$w" in
    ignore | go1.*) continue ;;
  esac
  if grep -qx "$w" <<<"$platforms"; then
    case "$w" in
      linux | amd64) ;;
      *)
        echo "go-fix: //go:build names platform '$w'; only linux/amd64 is analysed." >&2
        exit 1
        ;;
    esac
  else
    tags+=("$w")
  fi
done
tag_list=$(IFS=,; echo "${tags[*]}")

# `go fix -diff` exits 0 with the diff on stdout when rewrites are pending,
# and non-zero with nothing on stdout when the code does not compile. Both
# the exit status and the output have to be read, or a broken tree passes.
pending() {
  local out
  out=$(go fix -tags "$tag_list" -diff ./...) || exit 1
  printf '%s' "$out"
}

case "${1:-}" in
  check)
    out=$(pending)
    if [ -n "$out" ]; then
      printf '%s\n' "$out"
      echo "go-fix: rewrites pending under tags [$tag_list]. Run 'make modernize' and commit the result." >&2
      exit 1
    fi
    ;;
  apply)
    # One pass is not always a fixed point: rangeint can expose a loop
    # variable copy that only forvar then removes.
    # Assign before testing: inside `[ ]` a failed substitution is not seen
    # by `set -e`, and the empty output would read as "settled".
    for _ in 1 2 3; do
      out=$(pending)
      [ -z "$out" ] && exit 0
      go fix -tags "$tag_list" ./...
    done
    out=$(pending)
    [ -z "$out" ] && exit 0
    echo "go-fix: go fix did not settle after 3 passes." >&2
    exit 1
    ;;
  *)
    echo "usage: $0 check|apply" >&2
    exit 2
    ;;
esac
