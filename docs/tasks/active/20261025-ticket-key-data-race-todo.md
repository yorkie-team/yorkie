# Ticket.Key caches its key without synchronization

**Created**: 2026-10-25

Issue #2248. `Ticket.Key()` (`pkg/document/time/ticket.go`) fills `cachedKey`
lazily on first call, with no synchronization. `Ticket.ActorIDBase64()` fills
`cachedActorIDBase64` the same way.

## Problem

```go
func (t *Ticket) Key() string {
	if t.cachedKey == "" {
		t.cachedKey = ...
	}
	return t.cachedKey
}
```

A `*Ticket` is treated everywhere as an immutable value — it is passed by
pointer, compared, and used as a map key — so callers share one instance
freely. The package-level `InitialTicket` and `MaxTicket` are shared by every
document in the process, and a server handles many documents concurrently.
Every call to `Key()` is a potential *write*, so two goroutines reading the
same shared ticket race on `cachedKey`.

`go test -race ./test/conformance/...`, whose runner replays seeds in parallel
goroutines, reports the race. It is a plausible cause of the lagging-mode
`del4o` seed 5 flake (checks `00001`) that would not reproduce in 60 single
runs, 1500 in-process repeats, or three full reruns.

The string written is the same from every goroutine, so the practical damage
is bounded (a torn string header is the worst case), but it is a data race by
the Go memory model and the detector fails the build on it.

## Fix

Compute both strings in `NewTicket`, making `Ticket` immutable after
construction: no field is ever written after the constructor returns, so
`Key()` and `ActorIDBase64()` are plain reads and need no synchronization.

Chosen over `sync.Once`/atomics because it keeps `Ticket` a plain copyable
struct with no lock word, removes a branch from `Key()`, and leaves no second
way to construct a ticket that could reintroduce the lazy path. The cost is
two small allocations per ticket whose key is never read; the base64 encoding
was already shared between the two caches, so a ticket that needs both pays
exactly what it paid before.

`SetActorID` builds its result through `NewTicket` so it cannot skip the
computation.

## Tasks

- [x] Compute `key` and `actorIDBase64` in `NewTicket`; drop the lazy writes.
- [x] Route `SetActorID` through `NewTicket`.
- [x] Regression test: concurrent `Key()`/`ActorIDBase64()` on the shared
      `InitialTicket` and `MaxTicket`, which fails under `-race` before the
      fix.
- [x] Fix the sibling: `RGATreeSplitNodeID.key()`
      (`pkg/document/crdt/rga_tree_split.go`) has the identical lazy write,
      and `initialNodeID` — built from `time.InitialTicket` — is the dummy
      head ID of *every* `Text` in the process (`crdt/text.go:192`), so the
      same `-race` conformance run would still fail with only `Ticket`
      fixed. Computed in `NewRGATreeSplitNodeID`; the `key()` accessor is
      gone (a field and a method cannot share a name) and its two in-package
      call sites read `id.key` directly.

## See Also

- `docs/tasks/active/20261025-ticket-key-data-race-lessons.md`
