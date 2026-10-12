**Created**: 2026-09-25

# Adopt `errors.AsType` for type-safe error inspection

Issue: #1872 (Go 1.26 adoption), checklist item "Replace suitable
`errors.As` calls with `errors.AsType`".

## Context

The toolchain bump itself already landed: `go.mod` declares `go 1.26.0`,
`Dockerfile` builds on `golang:1.26`, and `.github/workflows/ci.yml` sets
`GO_VERSION: "1.26"`. What remains from the Go 1.26 section of the issue
that is actionable in this repo is the `errors.AsType` migration.

Go 1.26 adds:

```go
func AsType[E error](err error) (E, bool)
```

It replaces the `var target T; errors.As(err, &target)` pair with a single
generic call. Unlike `errors.As` it cannot panic at runtime on a bad
target — the constraint `E error` is checked at compile time — and it
removes the declare-then-check dance at every call site.

## Scope

Convert every `errors.As` call in non-generated Go code where the target
is a plain local variable used only for that check. That is the whole of
the repo's `errors.As` usage:

- [x] `server/rpc/connecthelper/errors.go` — 5 sites
      *Audit 2026-10-05:* Every listed site converted in #2060 (c54a193f).
- [x] `cluster/client.go` — 1 site
- [x] `pkg/errors/errors.go` — 2 sites
- [x] `pkg/errors/metadata.go` — 1 site
- [x] `pkg/webhook/client.go` — 1 site
- [x] `api/converter/errors.go` — 2 sites
- [x] `cmd/yorkie/version.go` — 1 site
- [x] `cmd/yorkie/project/create.go` — 1 site
- [x] `cmd/yorkie/project/update.go` — 1 site
- [x] `pkg/errors/errors_test.go` — 2 sites

Interface targets (`errors.StatusError`, `errors.MetadataError`) work the
same way: `AsType[StatusError](err)` matches any error in the chain that
implements the interface, exactly as `As` with an interface-typed target
did.

## Out of scope

- The Green Tea GC benchmark (#1903 tracks it separately).
- The goroutine leak profile evaluation — it is an experiment, not a code
  change, and belongs in its own issue.
- Bulk `go fix` cosmetic rewrites; #2058 already took the loop-shaped
  ones.

## Acceptance

- [x] No `errors.As` remains outside `api/yorkie/v1/` (generated).
      *Audit 2026-10-05:* No `errors.As(` outside `api/yorkie/v1/` on main.
- [x] `make lint` green.
      *Audit 2026-10-05:* #2060 merged with CI green.
- [x] `go test ./pkg/errors/... ./api/converter/... ./cluster/...
      ./server/rpc/... ./pkg/webhook/... ./cmd/...` green.
      *Audit 2026-10-05:* #2060 merged with CI green.
- [x] No behaviour change: each converted site keeps the same match
      semantics and the same branch structure.
      *Audit 2026-10-05:* #2060 merged after review.
