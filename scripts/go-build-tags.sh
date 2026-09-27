#!/usr/bin/env bash
# Print, comma-separated, every custom build tag a `//go:build` line in this
# repository names — the set to pass as `-tags` so a tool sees the files the
# default build skips (`integration`, `bench`, `complex`, ...).
#
# Read off the tree rather than kept as a list, so a new tag cannot fall
# outside what `make lint` and `scripts/go-fix.sh` cover. Both analyse
# linux/amd64, so those are the only platform names allowed on a build line.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Every word on a //go:build line, minus the operators.
words=$(git grep -h '^//go:build' -- '*.go' |
  sed 's|^//go:build||' | tr -c 'A-Za-z0-9_.\n' ' ' | tr -s ' ' '\n' |
  grep -v '^$' | sort -u)

if git grep -q '^//go:build.*!' -- '*.go'; then
  # Turning every tag on would exclude these files instead of covering them.
  echo "go-build-tags: a //go:build line negates a tag; this script cannot cover it." >&2
  git grep -n '^//go:build.*!' -- '*.go' >&2
  exit 1
fi

platforms=$(go tool dist list | tr '/' '\n' | sort -u)

# A file name restricts a file to a platform too — `x_windows.go`,
# `y_arm64_test.go`, `z_darwin_arm64.go` — with nothing on a //go:build line
# to say so. Go reads only the last one or two `_` parts (after dropping
# `_test`), GOARCH last, so words elsewhere in a name are not platforms.
offplatform=$(git ls-files '*.go' | awk \
  -v goos="$(go tool dist list | cut -d/ -f1 | sort -u | tr '\n' ' ')" \
  -v goarch="$(go tool dist list | cut -d/ -f2 | sort -u | tr '\n' ' ')" '
  BEGIN {
    n = split(goos, a, " "); for (i = 1; i <= n; i++) OS[a[i]] = 1
    n = split(goarch, a, " "); for (i = 1; i <= n; i++) ARCH[a[i]] = 1
  }
  {
    base = $0; sub(/.*\//, "", base); sub(/\.go$/, "", base); sub(/_test$/, "", base)
    m = split(base, p, "_")
    if (m < 2) next
    last = p[m]
    if (last in ARCH) {
      if (last != "amd64") { print $0 " (" last ")"; next }
      if (m >= 3 && (p[m-1] in OS) && p[m-1] != "linux") print $0 " (" p[m-1] ")"
    } else if ((last in OS) && last != "linux") {
      print $0 " (" last ")"
    }
  }')
if [ -n "$offplatform" ]; then
  echo "go-build-tags: a file name names a platform other than linux/amd64; it would go unanalysed:" >&2
  printf '%s\n' "$offplatform" >&2
  exit 1
fi
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
        echo "go-build-tags: //go:build names platform '$w'; only linux/amd64 is analysed." >&2
        exit 1
        ;;
    esac
  else
    tags+=("$w")
  fi
done
(IFS=,; echo "${tags[*]}")
