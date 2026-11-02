**Created**: 2026-10-25

# The node GC worklist has no guard for a child whose removal was cleared

## Problem

yorkie#2245. `Root.collect` (`pkg/document/crdt/root.go`) walks two worklists.
The element loop opens with

```go
if pair.elem.RemovedAt() == nil {
    continue
}
```

because a registered pair is only a *claim* that its element is a tombstone,
and both steps after it trust that claim: `VersionVector.EqualToOrAfter`
dereferences the ticket unconditionally
(`pkg/document/time/version_vector.go`), and `Purge` unlinks whatever the
child currently resolves to.

The node loop over `gcNodePairMap` right below it has no such guard. It goes
straight to `vector.EqualToOrAfter(pair.Child.RemovedAt())`. A registered node
pair whose child had its `removedAt` cleared — a revive not paired with
`UnregisterGCPair` — therefore panics on a nil deref, and on the server that
happens inside the snapshot build (`server/packs/snapshot.go`).

Unlike the element side, the node side is where `removedAt` actually *does* get
cleared today: `TreeNode.unremove` and `RGATreeSplitNode.SetRemovedAt(nil)`.
Every Restore path traced so far pairs its `unremove` with `UnregisterGCPair`
(`pkg/document/operations/tree_edit.go`), so no reachable panic is known. This
is the missing half of a defensive guard the element loop already has, on a
path remote changes drive. It surfaced reviewing #2243, which adds more revive
paths without touching this loop.

## Options explored

| Option | Verdict |
|---|---|
| `continue` on a nil `RemovedAt()`, keeping the entry on the worklist | Chosen: exactly mirrors the element loop, including its leak-over-crash trade-off — the entry's size stays charged to GC until a revive brings the GC→Live move `UnregisterGCPair` already does |
| Drop the entry (`delete`) when the child is no longer removed | Silently unbooks bytes from the ledger: `RegisterGCPair` moved them Live→GC, and only `UnregisterGCPair` (which also credits the `removedAt` ticket back) or a purge may move them out. Dropping leaves `docSize.GC` permanently high and loses the registration if the child is re-removed |
| Make `EqualToOrAfter` nil-tolerant | Hides the same class of bug at every call site, including ones where a nil ticket really is a programming error, and says nothing about whether `Purge` should still run |
| Call `UnregisterGCPair` from the loop | Collection is not the place to perform the revive's bookkeeping; the reviver knows whether the size move already happened and the loop does not |

## Plan

- [x] `Root.collect`: skip a `gcNodePairMap` entry whose `pair.Child.RemovedAt()`
      is nil, keeping it registered
- [x] Comment stating why the entry is kept rather than deleted, and that a
      leak is the safe failure where a panic in a snapshot build is not
- [x] `pkg/document/crdt/root_liveness_test.go`: a sibling to the existing
      `TestGarbageCollectSkipsLiveRegisteredElement`, driving the state
      directly at the crdt level — register a tombstoned tree node, clear its
      `removedAt`, collect
- [x] `make lint`, `go test ./pkg/document/...`

## Review

- The guard is placed before the `EqualToOrAfter` call, not inside the barrier
  check, because the panic is the ticket deref itself.
- `GarbageLen` still counts the skipped entry, which is what "keeps the entry"
  means observably; the test asserts that rather than only the absence of a
  panic.
