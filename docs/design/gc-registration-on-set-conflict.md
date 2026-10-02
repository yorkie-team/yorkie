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

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
