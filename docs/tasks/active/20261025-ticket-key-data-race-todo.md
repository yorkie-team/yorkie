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

Only instances shared across goroutines can race: the package-level
`time.InitialTicket` and `time.MaxTicket`, and `crdt.initialNodeID`, which the
head of every `Text` shares. Every other ticket and split-node ID lives inside
one document, behind that document's lock.

So the accessors stay lazy, as on `main`, and only those singletons are
warmed at package init: `InitialTicket`, `MaxTicket` and `initialNodeID` are
built through an unexported `warm()` that fills their caches, so a later read
of them is a pure read. `time` is initialized before `crdt`, so warming
`initialNodeID` reads an already warm `InitialTicket`. The type docs of
`Ticket` and `RGATreeSplitNodeID` state that an instance shared between
goroutines must be warmed when it is created.

The first revision computed both strings eagerly in `NewTicket` and
`NewRGATreeSplitNodeID` instead. Review measured the cost in the `bench` job
against `main`: allocs/op +149% on `Array/random_delete`, +33% on
`Document/counter`, +24–28% on `Document/text`, +21% on
`RemoteApply`/`ChangeReplay` — most tickets on the decode/apply path never
have their key read, and each paid two strings. That revision was dropped.

## Tasks

- [x] Keep `Key()`, `ActorIDBase64()` and `RGATreeSplitNodeID.key()` lazy.
- [x] Warm `InitialTicket`, `MaxTicket` and `initialNodeID` at package init.
- [x] Document on both types that a shared instance must be warmed.
- [x] Regression tests under `-race`: concurrent reads of `InitialTicket` and
      `MaxTicket` (`TestTicket`), and of the shared head ID through
      `crdt.InitialTextNode().IDString()` (`TestRGATreeSplit`). Both fail with
      `WARNING: DATA RACE` when the `warm()` calls are removed.
- [x] Confirm allocs/op on the affected benchmarks match `main`.

## See Also

- `docs/tasks/active/20261025-ticket-key-data-race-lessons.md`
