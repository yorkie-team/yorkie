## What the mechanical lanes cover on this branch

These run on this PR and must pass before it can be promoted. A finding whose
whole content is "this would fail CI" costs a review round and tells the author
nothing they were not about to be told anyway.

ENFORCED — you may rely on these:
- `golangci-lint run ./...` under every build tag (`make lint`), with
  gofmt and goimports as formatters, plus the default set (errcheck, govet,
  ineffassign, staticcheck, unused) and gosec, revive, wrapcheck, gocyclo,
  goconst, lll, misspell, nakedret and goprintffuncname. Formatting, import
  grouping, unchecked errors and unreferenced code are covered here; lll,
  goconst and gocyclo are off in the tag-gated suites under `test/`.
- `buf lint` over the protobuf, and `buf breaking` against this PR's own base
  commit — a wire-incompatible proto change reds the lane.
- Generated-code freshness: `buf generate` must leave `api/` clean, untracked
  files included. A hand-edited `.pb.go` or a stale OpenAPI bundle reds it.
- `make build`.
- `go vet -tags rgafuzz ./...`, which COMPILES the build-tag-gated
  reproductions without running them.
- `make verify-modernize`: the Go toolchain's `go fix` modernizers must have
  nothing to rewrite under every build tag (range-over-int, min/max,
  slices/maps helpers, `any`, and so on). `make verify` runs it locally too.
- `go test -tags integration -race -coverpkg=./... ./...` against a real
  MongoDB from docker compose. The race detector is on.
- `node scripts/verify-doc-links.mjs` and its own `node --test` suite, on every
  PR with no path filter: a markdown link reachable from CLAUDE.md, AGENTS.md
  or README.md must resolve on disk.
- `node scripts/verify-license.mjs`, in the same `build` job as the Go lanes:
  every `.go` file must carry the Apache 2.0 grant clause in its first 40
  lines. `make verify` runs it locally too.

NOT ENFORCED BY ANYTHING — a real finding here is worth MORE than one the lanes
above would have caught, because nothing else in the pipeline will catch it:
- `go test -tags complex` (the sharded-cluster suite), `-tags bench`, and the
  k6 load test. All three are path-gated, so most pull requests run none.
- Everything above, on a documentation-only PR. `ci.yml`'s `build` job is
  filtered on `**/*.md` (plus api/docs, build/charts, design/ and *.txt), so
  such a PR runs no lint, no build and no tests — only `docs.yml`'s link
  check, which reads no Go behaviour.
- A data race on a path no test exercises. `-race` observes executions, not
  code, so it proves nothing about what the suite never reached.
- Whether a passing test asserts anything. The lanes prove the suite is GREEN,
  never that it is ADEQUATE — a test that asserts nothing passes just as loudly.
