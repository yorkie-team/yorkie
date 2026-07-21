# Migrate to sync.WaitGroup.Go for goroutine spawning

**Created**: 2026-07-18

## Goal

Replace the `wg.Add(1)` / `go func() { defer wg.Done(); ... }()` idiom
with Go 1.25's `sync.WaitGroup.Go` across the codebase (#1860).
Mechanical rewrite, no behavior change. Add a CONTRIBUTING.md note so
new goroutine spawns paired with a WaitGroup use `wg.Go` going forward.

## Why

`wg.Go(f)` calls `Add(1)`, spawns `f`, and calls `Done` when `f`
returns — it removes the class of bugs where `Add`/`Done` are
unbalanced (early return before `Done`, `Add` called inside the
goroutine, etc.).

## Prerequisite

- [x] `go.mod` bumped `go 1.24.0` -> `go 1.25.0`; also bumped
  `.github/workflows/ci.yml` (`GO_VERSION`), `base-docker-publish.yml`
  (`go-version`), and `Dockerfile` (`FROM golang:1.24` builder image)
  to 1.25, since CI/Docker were still pinned to 1.24 and would have
  broken on the go.mod bump alone. `go mod tidy` ran clean, no go.sum
  changes. Commit: `b8650560`.

## Progress

Working branch: `task/waitgroup-go-migration`. Each file below is its
own commit (`make lint` / relevant unit tests green before commit).
Resume by checking `git log main..HEAD --oneline` against this list.

Non-test (7/7 done):
- [x] `server/clients/housekeeping.go` — commit `2605156a`
- [x] `server/backend/background/background.go` — commit `63d8348c`
- [x] `server/backend/fanout.go` — commit `79524bc3`
- [x] `server/backend/membership/membership.go` — commit `dbb48bd4`
- [x] `server/rpc/testcases/testcases.go` — commit `180cd10d`
- [x] `client/client.go` — commit `c4f04d99`
- [x] `pkg/limit/limiter.go` — commit `d95b6982`

Two non-mechanical variants found along the way, worth knowing about
for the remaining test files too:
- Add/Done split across two functions (`membership.go`,
  `limiter.go`'s `expirationLoop`): pass the method value straight to
  `wg.Go` (`lim.wg.Go(lim.expirationLoop)`), or wrap in a closure if
  the method takes args, and drop the now-orphaned `defer wg.Done()`
  at the far end.
- Closure took loop-var params to dodge pre-1.22 capture bugs (e.g.
  `func(gIdx int) {...}(g)`): **correction, superseded twice** — an
  earlier version of this note said to rename the outer loop var to
  match the former param name (used once, in `housekeeping.go`'s
  `candidate`→`c`). The repo owner corrected this: renaming *either*
  side is out of scope for a mechanical swap. The actual rule used
  for every file after `housekeeping.go`: keep both the outer loop
  var and the param name exactly as they were, and add one shadow
  line as the first statement of the new closure body —
  `gIdx := g` (or `a, b := x, y` for multiple params). Only
  `wg.Add`/the param list/`defer wg.Done()` are touched; zero
  identifiers change anywhere in the diff.

  **Superseded again by PR review feedback**: the shadow line itself
  is dead code — Go 1.22+ already scopes `for`-loop variables per
  iteration, so a `wg.Go` closure can capture `i`/`g`/`w`/`r`/`j`
  directly with no capture-bug risk, shadow copy or not. A reviewer
  flagged this on the PR (`svr := svr` and the `idx := i` family)
  and asked for a follow-up commit deleting them. Fixed 61 sites
  across the 11 test files that had them (all under
  `pkg/trie`, `server/backend/channel`, `server/backend/pubsub`,
  `server/backend/membership`, `test/bench`, `test/integration`) by
  deleting the shadow-decl line and renaming every use of the shadow
  identifier back to the outer loop var, scoped strictly to that one
  closure's body (brace-depth tracked, not a file-wide rename — two
  sibling closures in the same file can shadow the same outer var
  under different names, e.g. `wid`/`did` both from `i` in separate
  `for i := range numWriters` loops). None of the 7 non-test files
  used this pattern. Verified via a hand-reviewed `git diff` of all
  11 files against this exact site list before running any build
  tooling — every hunk is either a deleted shadow line or an
  in-closure identifier rename, nothing else moved.

Test (22/22 done). Migrated in 12 folder-batches (dispatched to
parallel subagents for groups 3 onward), each verified with
build+vet+gofmt+lint+test (and `-race` where feasible) before commit:

- [x] `cluster/pool_test.go` — `1d594861`
- [x] `test/bench/*.go` (5 files) — `d2652c94`
- [x] `server/backend/pubsub/pubsub_test.go` — `42bfb60b`
- [x] `server/backend/membership/membership_test.go` — `cb0b9f7a`
- [x] `server/rpc/admin_server_unit_test.go` — `24f76bf4`
- [x] `pkg/limit/limiter_test.go` — `c8c25464`
- [x] `pkg/cache/cache_test.go` — no changes (all sites are batch
  `Add(n)` matched by a single loop-generated `Done()`, doesn't fit
  the mechanical triple)
- [x] `pkg/locker/locker_test.go` — `70567710`
- [x] `pkg/cmap/cmap_test.go` — `e434bc83`
- [x] `server/backend/channel/*.go` (2 files) — `e26c5045`
- [x] `test/integration/*.go` (7 of 8 files; `doc_presence_test.go`
  untouched — Done() fires conditionally inside a select loop, not a
  single deferred call) — `3f5c2ee2`
- [x] `pkg/trie/*.go` (2 files) — `fd7b0256`

Key convention established mid-task (see notes below): loop-var
capture params are shadow-assigned, never renamed. The Add(2)/two-
literal-blocks exception was extended case-by-case to Add(3)/three
blocks where each block is independently a one-Add-one-Done unit —
never to `Add(n)` preceding a loop (that stays a hard skip).

Remaining: full `make test` (MongoDB up, `-race`, `-tags integration`)
pass across the whole diff, `/code-review` self-review, rebase onto
`main`, open Draft PR referencing #1860.

- [x] Review follow-up: remove now-unnecessary loop-var shadow copies
  from `wg.Go` closures (61 sites, 11 test files) — see the
  superseded note above for the full rationale and verification.
  `make lint` (0 issues) and `go test ./...` (full unit suite) green
  after the change.

## Scope

`grep -rl "sync.WaitGroup" --include="*.go" .` found:

Non-test (7):
- `server/clients/housekeeping.go`
- `server/backend/background/background.go`
- `server/backend/fanout.go`
- `server/backend/membership/membership.go`
- `server/rpc/testcases/testcases.go`
- `client/client.go`
- `pkg/limit/limiter.go`

Test (22):
- `cluster/pool_test.go`
- `test/bench/channel_concurrency_bench_test.go`
- `test/bench/grpc_bench_test.go`
- `test/bench/sync_concurrency_bench_test.go`
- `test/bench/channel_bench_test.go`
- `test/bench/presence_concurrency_bench_test.go`
- `test/integration/doc_presence_test.go`
- `test/integration/restapi_test.go`
- `test/integration/document_test.go`
- `test/integration/clusternodes_test.go`
- `test/integration/channel_test.go`
- `test/integration/admin_test.go`
- `test/integration/counter_dedup_snapshot_test.go`
- `test/integration/broadcast_test.go`
- `server/backend/channel/manager_test.go`
- `server/backend/channel/channel_trie_test.go`
- `server/backend/pubsub/pubsub_test.go`
- `server/backend/membership/membership_test.go`
- `server/rpc/admin_server_unit_test.go`
- `pkg/cache/cache_test.go`
- `pkg/limit/limiter_test.go`
- `pkg/cmap/cmap_test.go`
- `pkg/trie/sharded_path_trie_test.go`
- `pkg/trie/path_trie_test.go`
- `pkg/locker/locker_test.go`

Each site: fold `wg.Add(1)` + `go func(...) { defer wg.Done(); BODY }(...)`
into `wg.Go(func() { BODY })`. Where the closure currently takes loop
variables as params to avoid capture bugs (Go < 1.22 idiom), drop the
params and capture directly — this repo's Go version already has
per-iteration loop variables, so capture is safe.

Skip any `WaitGroup` use that doesn't fit the `Add`/`go`/`Done` triple
(e.g. `Add(n)` for a batch followed by one `Done()` call elsewhere) —
those need case-by-case judgment, not a mechanical swap.

## Plan

1. Bump `go.mod` to `go 1.25.0`, run `go mod tidy`.
2. Rewrite non-test files first (7 files), `make lint` + relevant unit
   tests after each.
3. Rewrite test files (22 files).
4. Add one-liner to `CONTRIBUTING.md` recommending `wg.Go` for new
   goroutine-spawning code paired with a `WaitGroup`.
5. `make lint` and `make test` (MongoDB up) across the full diff.
6. Self-review via `/code-review` before opening the PR.

## Non-goals

- Not touching `errgroup.Group` usages (different type, not in scope
  per the issue).
- Not restructuring goroutine logic beyond the mechanical swap.
