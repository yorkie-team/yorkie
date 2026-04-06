---
title: concurrent-merge-split
target-version: 0.7.4
---

# Concurrent Merge and Split

## Problem

When two clients concurrently perform merge or split operations on the same
tree region, the replicas diverge after synchronization. This violates the
fundamental CRDT convergence guarantee.

The Go SDK integration tests have 10 skip-marked concurrent merge/split test
cases. Running them reveals 9 failures across three failure patterns:

| Pattern | Count | Example |
|---------|-------|---------|
| Convergence failure (d1 ≠ d2) | 6 | overlapping-merge-and-merge |
| Index error (`from > to`) | 2 | overlapping-merge-and-delete-text-nodes |
| Wrong ordering | 1 | side-by-side-split-and-insert |

Root cause analysis identifies three independent bugs in `CRDTTree.Edit`:

1. **Ghost reference**: `Append` does not detach the node from its old parent,
   leaving a stale child reference. Later traversals treat the ghost as a live
   node and tombstone it.
2. **Split sibling escape**: `SplitElement` creates a node with a fresh
   `CreatedAt` timestamp. A concurrent delete range cannot reach this sibling
   because its `toLeft` boundary was computed before the split.
3. **Merge-tombstone insert loss**: When a merge tombstones a parent, a
   concurrent insert into that parent is immediately tombstoned. There is no
   redirect to the merge destination where the parent's children now live.

### Goals

- Fix all three bugs so the 9 failing integration tests pass.
- Preserve backward compatibility: no protobuf or protocol changes.
- Keep all existing passing tests green.

### Non-Goals

- Undo/redo support for merge/split (deferred per `undo-redo.md` Phase 2).
- General-purpose `Tree.Move` operation (Phase 2).
- JS SDK changes (separate follow-up).

## Design

### Fix 1: Append detach

**Location**: `CRDTTree.Edit` Step 03 (merge), `crdt/tree.go` ~line 900.

Currently `fromParent.Append(node)` adds the node to the new parent without
removing it from the old parent. The fix detaches first:

```go
// Step 03: Merge
for _, node := range toBeMovedToFromParents {
    if node.removedAt == nil {
        // Detach from old parent before appending to new parent.
        if err := node.Index.Parent.RemoveChild(node.Index); err != nil {
            return nil, resource.DataSize{}, err
        }
        if err := fromParent.Append(node); err != nil {
            return nil, resource.DataSize{}, err
        }
    }
}
```

`RemoveChild` updates the old parent's `VisibleLength` and `TotalLength`.
`Append` updates the new parent's lengths. No ghost reference remains.

**Affected tests**: #1 overlapping-merge-and-merge, #3 overlapping-merge-and-
delete-text-nodes, #6 contained-merge-and-merge-at-the-same-level.

### Fix 2: Split sibling cascade delete

**Location**: `CRDTTree.collectBetween`, `crdt/tree.go` ~line 957.

When an element node is marked for deletion, its `InsNextID` chain may contain
split siblings created by a concurrent `SplitElement`. If the version vector
does not know about the sibling's creation, the sibling was created by a
concurrent split and should be included in the deletion.

```go
// After adding node to toBeRemoveds:
if !node.IsText() && node.InsNextID != nil {
    next := t.findFloorNode(node.InsNextID)
    for next != nil {
        // Only cascade to siblings unknown to the editing client.
        creationKnown := false
        if isLocal {
            creationKnown = true
        } else if l, ok := versionVector.Get(next.id.CreatedAt.ActorID()); ok &&
            l >= next.id.CreatedAt.Lamport() {
            creationKnown = true
        }
        if !creationKnown {
            toBeRemoveds = append(toBeRemoveds, next)
            // Also mark descendants for deletion.
            index.TraverseNode(next.Index, func(n *index.Node[*TreeNode], _ int) {
                toBeRemoveds = append(toBeRemoveds, n.Value)
            })
        }
        if next.InsNextID == nil {
            break
        }
        next = t.findFloorNode(next.InsNextID)
    }
}
```

This only applies to element nodes. Text splits use offset-based IDs with the
same `CreatedAt`, so `findFloorNode` already resolves them correctly.

**Affected tests**: #4 contained-split-and-split-at-different-levels,
#5 contained-split-and-delete-the-whole, #9 side-by-side-split-and-insert,
#10 side-by-side-split-and-delete.

### Fix 3: Merge-tombstone insert redirect

**Location**: `CRDTTree.FindTreeNodesWithSplitText`, `crdt/tree.go` ~line 1228.

When `FindTreeNodesWithSplitText` resolves a position whose parent has been
tombstoned by a merge, the insert should be redirected to the merge
destination. The merge destination is found by checking if any of the
tombstoned parent's former children have been moved to a living parent.

```go
// After resolving parentNode and leftNode via ToTreeNodes:
if parentNode.IsRemoved() {
    // Check if this is a merge-tombstone by finding children that now
    // live in a different (living) parent.
    for _, child := range parentNode.Index.Children(true) {
        childParent := child.Value.Index.Parent.Value
        if childParent != parentNode && !childParent.IsRemoved() {
            // Merge destination found. Redirect.
            mergeTarget := childParent

            if isLeftMost {
                // leftmost insert: find the left sibling just before
                // the first moved child in the merge target.
                idx, _ := mergeTarget.Index.FindOffset(child)
                if idx == 0 {
                    return mergeTarget, mergeTarget, diff, nil
                }
                prevChild := mergeTarget.Index.Children(true)[idx-1]
                return mergeTarget, prevChild.Value, diff, nil
            }
            break
        }
    }
    // If no living child found in a different parent, this is a plain
    // delete (not a merge). Fall through to existing behavior.
}
```

When `leftNode` is not the parent itself (non-leftmost insert), the existing
code at line 1248 already follows `leftNode.Index.Parent.Value` to the correct
parent. Fix 1 (detach) makes this resolution accurate by ensuring `leftNode`
has exactly one parent.

**Affected tests**: #7 contained-merge-and-insert,
#8 contained-merge-and-delete-contents-in-merged-node.

### Fix interaction

The three fixes are independent but synergistic:

- Fix 1 makes Fix 3's redirect reliable (no ghost references confuse parent
  lookup).
- Fix 2 is orthogonal to Fix 1 and Fix 3 (split-specific, merge-unrelated).
- Applying them incrementally (Fix 1 → Fix 2 → Fix 3) allows regression
  testing at each step.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Fix 1 detach breaks existing passing tests | Run full integration + concurrency test suite after each fix |
| Fix 2 cascade deletes too aggressively | Version vector check ensures only unknown-to-editor splits are cascaded. Element-only guard prevents text split interference |
| Fix 3 redirect fires on plain deletes (not merges) | Redirect only when a living child exists in a different living parent — a pattern unique to merge |
| Three fixes interact unexpectedly | Incremental application with test runs between each fix |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix implicit move instead of adding explicit Move operation | All three bugs require the same fixes regardless of approach. Move adds protocol complexity with no additional benefit for these cases |
| Element-only cascade for Fix 2 | Text splits use same-CreatedAt offset-based IDs that findFloorNode already resolves. Cascading text splits would break the existing split text semantics |
| No new fields on TreeNode | Keeps protobuf unchanged. Merge destination is derived from live tree structure rather than stored pointers |
| No undo/redo in scope | Consistent with undo-redo.md Phase 2 deferral. Merge/split convergence is prerequisite |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Explicit `TreeMove` CRDT operation | Same three fixes needed regardless. Adds protobuf message, converter code, and operation type for no additional convergence benefit in Phase 1 |
| `mergedInto` pointer field on TreeNode | Requires protobuf change and serialization update. Live tree traversal achieves the same redirect without new fields |
| Range-based Move (move all children after boundary) | Does not commute with concurrent inserts — causes divergence when insert and move are applied in different order |
| Fix only at JS SDK level (rewrite splitByPath/mergeByPath) | Does not fix the CRDT layer bugs. Go concurrency tests would still fail |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
