**Created**: 2026-09-25

# `go fix` modernizations from the Go 1.26 upgrade

Issue #1872 asks for the Go 1.24 → 1.26 toolchain upgrade plus four adoption
items. This task delivers exactly one of them: *"Apply useful modernizations
reported by the revamped `go fix`"* — `maps.Copy`, `strings.SplitSeq`,
`min`/`max`, and `reflect.TypeFor`.

## Motivation

Every one of those four APIs already exists in the toolchain this repository
compiles with today (`maps.Copy` and `min`/`max` since Go 1.21,
`reflect.TypeFor` since 1.22, `strings.SplitSeq` since 1.24). They are the only
part of #1872 that can be written, compiled and tested without a Go 1.26
toolchain, so they are separable from the version bump and worth landing on
their own.

The rewrites are small but not cosmetic: each one deletes a hand-rolled loop or
a re-implementation of a standard-library primitive, which is what the issue
means by excluding "cosmetic-only bulk rewrites".

## Not in scope, and why

The toolchain bump itself — `go.mod`, CI, the Docker builder — is **not** in
this change, and it is not a matter of running short of time. Three facts make
it undeliverable from this branch:

1. CI's Go version is `GO_VERSION: "1.25"` in `.github/workflows/ci.yml`, and
   six further workflows pin `go-version: "1.25"` inline. The agent that wrote
   this branch is denied write access to `.github/workflows/**` by design, so
   it cannot move CI to 1.26.
2. Consequently a `go 1.26.0` line in `go.mod` would turn every CI lane red
   rather than green, and the PR would be unreviewable.
3. The build environment here runs go1.25.14 with `GOTOOLCHAIN=local`, so a
   1.26 toolchain cannot even be fetched to check the result locally.

The other three Go 1.26 items are blocked by the same wall:
`errors.AsType` is a 1.26 API, the goroutine leak profile is a 1.26 runtime
feature, and the Green Tea GC benchmark (#1903) needs a 1.26 runtime to
benchmark. The two Go 1.25 items (#1859, #1860) are already checked off in the
issue.

Worth recording for whoever picks up the bump: the issue says the repository is
on Go 1.24, but `go.mod` already reads `go 1.25.0` and the Dockerfile already
reads `golang:1.25`, so the remaining move is 1.25 → 1.26, not 1.24 → 1.26.

## Plan

- [x] Write this todo and the matching lessons file.
- [x] `maps.Copy` for the five hand-written map-copy loops:
      `pkg/errors/metadata.go` (three), `pkg/trie/path_trie.go`,
      `server/rpc/connecthelper/errors.go`.
- [x] `strings.SplitSeq` for the two `range strings.Split(...)` loops that
      never keep the slice: `server/rpc/mcp/tools.go`,
      `api/types/updatable_project_fields.go`.
- [x] `min`/`max` for the three clamp-by-`if` sites: `pkg/cache/lru_with_stats.go`,
      `server/backend/channel/manager.go`, `pkg/document/crdt/tree.go`.
- [x] `reflect.TypeFor` for the four package-level `reflect.TypeOf` registry
      keys in `server/backend/database/mongo/registry.go`.

## Verification

- [x] `make lint`
- [x] `go build ./...`
- [x] Targeted unit tests over every touched package.
- [ ] Full `make verify`, `make test`, `make test-complex` — left to CI; they
      need a MongoDB stack this run has no access to.

## Review

Deliberately left alone:

- `server/logging/logging.go` copies `[]zap.Field` into `[]any`; `copy` cannot
  change element type and there is no `slices` helper for it.
- `server/backend/database/memory/database.go`, `.../mongo/client.go` and
  `cluster/pool.go` each fill a map with a *constant* from a slice, which is a
  set build, not a copy.
- `client/client_test.go` and `test/integration/snapshot_test.go` call
  `reflect.TypeOf` on a value whose static type is an interface; `TypeFor`
  would name the interface, not the dynamic type, so the rewrite changes
  meaning.
- The `for i := 0; i < n; i++` → `for range n` rewrite. It is exactly the
  "cosmetic-only bulk rewrite" the issue excludes.
