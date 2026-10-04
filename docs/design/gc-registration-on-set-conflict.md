---
title: gc-registration-on-set-conflict
target-version: 0.7.6
---

# GC Registration on Set Conflict

## Problem

When two clients simultaneously attach to a new document using `Attach` with
`InitialRoot`, both generate `Set` operations for the same key. When these
operations are applied on the server (or via remote sync), the LWW (Last Writer
Wins) resolution in `ElementRHT.Set` correctly determines a winner and marks the
loser as removed. However, when the **new element loses** the LWW conflict, it
is not registered in `gcElementPairMap`, causing a GC mapping leak.

### Root Cause

In `ElementRHT.Set` (`element_rht.go:102-120`), there are two conflict
outcomes:

1. **New element wins** (higher CreatedAt): The old element is marked as removed
   and returned as `removed`. The caller (`Set` operation in `set.go`) registers
   it in `gcElementPairMap` via `RegisterRemovedElementPair`. This works
   correctly.

2. **New element loses** (lower CreatedAt): The new element is immediately
   marked as removed (`v.Remove(node.elem.CreatedAt())`), but `Set` returns
   `nil` for `removed` because the old element was not displaced. The caller
   sees `removed == nil` and skips GC registration. The new element exists in
   `nodeMapByCreatedAt` with `removedAt` set but has no entry in
   `gcElementPairMap`.

```
// Conflict timeline example:
// Client A: Set("content", valueA) — createdAt=(1, actorA)
// Client B: Set("content", valueB) — createdAt=(1, actorB)
// If actorA > actorB, valueB loses LWW:
//   - valueB added to nodeMapByCreatedAt ✓
//   - valueB.removedAt set              ✓
//   - gcElementPairMap entry for valueB  ✗ ← MISSING
```

### Impact

- `ElementsMapByCreatedAt` contains two elements for the same key (winner +
  tombstoned loser)
- `gcElementPairMap` is missing the entry for the loser
- The loser element is never garbage collected, causing a memory leak
- Reported in [#1300](https://github.com/yorkie-team/yorkie/issues/1300)

### Goals

- Register LWW-losing elements in `gcElementPairMap` so they are garbage
  collected

### Non-Goals

- Changing the LWW resolution logic in `ElementRHT`
- Modifying the `ElementRHT.Set` return type or signature

> **Superseded (2026-10-02).** Both non-goals above were later taken on
> deliberately for the losing branch only, and the "Design Decisions" and
> "Alternatives Considered" tables below are kept as the record of what was
> decided at the time, not as current guidance. See
> [Slot refusal in `ElementRHT`](#slot-refusal-in-elementrht).

## Design

Add a post-check in `Set.Execute` (`operations/set.go`) after `obj.Set()`. If
the newly added value has been immediately marked as removed (i.e., it lost the
LWW conflict), register it in `gcElementPairMap`:

```go
func (o *Set) Execute(root *crdt.Root, _ time.VersionVector) error {
    parent := root.FindByCreatedAt(o.parentCreatedAt)

    obj, ok := parent.(*crdt.Object)
    if !ok {
        return ErrNotApplicableDataType
    }

    value, err := o.value.DeepCopy()
    if err != nil {
        return err
    }
    removed := obj.Set(o.key, value)
    root.RegisterElement(value)
    if removed != nil {
        root.RegisterRemovedElementPair(obj, removed)
    }
    if value.RemovedAt() != nil {
        root.RegisterRemovedElementPair(obj, value)
    }
    return nil
}
```

The added check (`value.RemovedAt() != nil`) catches the case where
`ElementRHT.Set` marks the new element as removed due to LWW loss. This is safe
because:

- A freshly created element always has `RemovedAt() == nil`
- `RemovedAt()` is only set during `ElementRHT.Set` when the element loses LWW
- The check does not interfere with the existing `removed != nil` path — these
  are mutually exclusive (if the old element is removed, the new one wins; if
  the new one is removed, the old one stays)

### Affected Call Sites

| Call site | Impact |
|-----------|--------|
| `operations/set.go:68` | **Bug site** — LWW loser not registered in GC |
| `converter/from_bytes.go:137` | Not affected — `NewRoot` traverses all descendants and registers removed elements |
| `object.go:134` (`DeepCopy`) | Not affected — rebuilt via `NewRoot` |
| `object.go:37` (`NewObject`) | Not affected — empty RHT, no conflict possible |

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Double registration if both `removed` and `value` are non-nil | Mutually exclusive by LWW logic — only one of old/new can be the loser |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix in `set.go` rather than changing `ElementRHT.Set` signature | Minimal change, no API breakage, all callers would need updating otherwise |
| Post-check on `value.RemovedAt()` rather than new return value | Simpler, no signature change, the information is already on the element |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Change `ElementRHT.Set` to return `(removed, loser)` tuple | Breaks API for all callers, larger change for same result |
| Register inside `ElementRHT.Set` directly | `ElementRHT` has no access to `Root` or `gcElementPairMap` |

## Slot refusal in `ElementRHT`

The post-check above books a losing value into GC, which requires the losing
value to be indexed under its `createdAt` in `nodeMapByCreatedAt`. That index
is keyed by a creation ticket, not by an element, and undo/redo made two
different elements able to claim one ticket: a restore re-inserts a *copy* of
a removed element under its original `createdAt`. Two replicas undoing
concurrent overwrites of one key therefore restore two copies of the same
value. Indexing the losing copy drops whichever node held the slot, and
`nodeMapByCreatedAt` is the only way GC, `purge` and `DeepCopy` address a
node -- so the dropped one is uncollectable, absent from every snapshot built
afterwards, and the document stops rebuilding once GC runs.

`ElementRHT.SetWithExecutedAt` therefore refuses a **losing** value when the
node holding its slot is still reachable through its key -- a live node, or a
tombstone that is still its key's occupant -- and reports the refusal as a
second return value (`Element, bool`). A tombstone already displaced from its
key is taken over, so a losing restore ends where a restore that won first
and was then evicted ends. The rule lives in `refusesLoser`'s doc comment in
`pkg/document/crdt/element_rht.go`.

A **winning** value always takes the slot, as in the JS SDK. In a history the
SDKs produce, a `createdAt` sits at one key of one object, so the node a
winner displaces is the occupant it evicts or a tombstone that used to sit at
that key. When the evicted occupant is itself a copy under the winner's
`createdAt` (the newer restore arriving second), it leaves both maps, and
`Set.Execute` books it as removed and retires it at once
(`RegisterRemovedElementPair` then `UnregisterRemovedElementPair`). Booking it
as an ordinary removed pair would file it under a `createdAt` the winner now
answers to, and the next eviction would overwrite that entry and leave its GC
charge in `docSize` for good, on the replicas that met the restores in that
order only (`TestSetConcurrentRestoresConverge`).

Callers:

| Caller | On refusal |
|--------|-----------|
| `operations.Set.Execute` | `ErrOperationSkipped` -- the object is unchanged, so the operation did not apply and contributes no reverse |
| `json.Object.setInternal` | panics -- unreachable, since only a loser is refused and a local Set always wins; the caller would otherwise be handed a detached proxy |
| `api/converter.fromJSONObject` | ignored -- encoder output has one node per `createdAt`, so a refusal needs crafted bytes |
| `crdt.NewObject` | not affected -- empty RHT, no conflict possible |

`Root.UnregisterRemovedElementPair` takes the owning container and retires
only an entry that container registered. The json layer records the CRDT
container, not its proxy, as the parent so that identity check holds on the
local path.

### Orphan addressability across a rebuild

A refused copy, and a tombstone a restore displaced, hang off no container.
Both stay addressable on the `Root` that adopted them -- that is what keeps an
operation a peer addressed at a descendant only one copy carries from aborting
the change -- but a `Root` rebuilt from a tree cannot find them:

| Rebuild | Orphan slots |
|---------|--------------|
| `Root.DeepCopy` (the clone every remote change runs against first) | carried over from `Root.detached`, re-adopted at no charge |
| `InternalDocument.applySnapshot` | **lost**: a snapshot encodes only what the tree reaches, in every SDK |

The snapshot case is unrecoverable, so addressability cannot be the only thing
holding the apply path up. An operation whose target `createdAt` resolves to
nothing is therefore skipped rather than failed off the local path
(`operations.skipUnresolvedTarget`): on the replicas that still hold the
orphan it mutates data no tree reaches and no reader can observe, so dropping
it is the same no-op -- and failing instead aborts the change forever on the
server, which replays the same log on every rebuild. A local operation was
built against its own root a moment ago, so an unresolved target there is
still `ErrNotApplicableDataType`.

### Crafted payloads

These rules are about histories the SDKs can produce, but the refusal makes
the payload's `createdAt` decide which branch the apply path takes, and that
ticket arrives verbatim off the wire (`api/converter.fromSet`). So `Set`
rejects a payload whose `createdAt` answers to a **live** element that is not
the current occupant of the key being set (`ErrInUseElementIdentity`). The two
collisions a legitimate history produces -- a restore under a tombstoned
`createdAt`, and a concurrent restore losing to the copy already at the key --
both pass, so this does not split the server's snapshot replay from JS
clients. The general push-boundary validation, covering `Add` and `ArraySet`
too, is still tracked in yorkie-team/yorkie#2081.

### JS SDK port

`ElementRHT` is otherwise a port of `element_rht.ts`. The loser refusal, the
tombstone-occupant case and the release of an evicted copy are ported in
yorkie-team/yorkie-js-sdk#1440. Until that lands, a JS replica takes in a
loser that a Go replica refuses. That only happens in the concurrent-restore
shapes above, where the JS behavior is the bug this section fixes.

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
