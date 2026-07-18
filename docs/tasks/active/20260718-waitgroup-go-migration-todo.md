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

Non-test (2/7 done):
- [x] `server/clients/housekeeping.go` — commit `2605156a`
- [x] `server/backend/background/background.go` — commit `63d8348c`
- [ ] `server/backend/fanout.go`
- [ ] `server/backend/membership/membership.go`
- [ ] `server/rpc/testcases/testcases.go`
- [ ] `client/client.go`
- [ ] `pkg/limit/limiter.go`

Test (0/22 done) — see Scope list below for the full file set.

Remaining after file migration: CONTRIBUTING.md note, full
`make lint` + `make test` (MongoDB up) pass, `/code-review`
self-review, rebase onto `main`, open Draft PR referencing #1860.

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
