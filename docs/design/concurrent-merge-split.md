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

- Fix the three bugs above so the failing integration tests pass.
- Preserve backward compatibility: no protobuf or protocol changes.
- Keep all existing passing tests green.

### Non-Goals

- Undo/redo support for merge/split (deferred per `undo-redo.md` Phase 2).
- General-purpose `Tree.Move` operation (Phase 2).
- JS SDK changes (separate follow-up).

## Tree.Edit Convergence Coverage

### Edit execution flow

```
Edit(from, to, contents, splitLevel)
  │
  ├── Step 01: FindTreeNodesWithSplitText(from), FindTreeNodesWithSplitText(to)
  │            CRDTTreePos → (parentNode, leftNode), split text nodes
  │
  ├── Step 02: collectBetween(fromParent, fromLeft, toParent, toLeft)
  │            ├── traversal: walk nodes in range (includeRemoved=true)
  │            ├── merge detection: Start token && !ended → collect children
  │            ├── delete judgment: canDelete(editedAt, creationKnown, tombstoneKnown)
  │            └── cascade: parent in toBeRemoveds → children also deleted
  │
  ├── Step 03: Delete — tombstone toBeRemoveds nodes
  │
  ├── Step 04: Merge — move toBeMovedToFromParents to fromParent
  │
  ├── Step 05: Split — SplitElement for splitLevel > 0
  │
  └── Step 06: Insert — insert contents at fromParent
               └── concurrent parent deletion guard: tombstoned parent → new node tombstoned
```

### Basic Edit + Edit (insert, delete, replace)

All 27 cases from `tree.md` converge. These are the foundation:

| Range | Scenario | Status | Mechanism |
|-------|----------|--------|-----------|
| Overlapping | delete + delete | ✅ | `canDelete` + version vector |
| Overlapping | insert + delete | ✅ | version vector visibility |
| Overlapping | insert + insert | ✅ | `insertAfter` only + timestamp order |
| Contained | delete ⊃ insert | ✅ | concurrent parent deletion guard (Step 06) |
| Contained | delete ⊃ delete | ✅ | `canDelete` LWW |
| Side-by-side | insert + insert | ✅ | InsPrevID/InsNextID chain + RGA order |
| Side-by-side | insert + delete | ✅ | independent ranges |
| Side-by-side | delete + delete | ✅ | independent ranges |
| Equal | all combinations | ✅ | LWW tombstone / RGA order |

### Merge (Edit crossing element boundary)

| Range | Scenario | Status | Notes |
|-------|----------|--------|-------|
| Contained | merge + delete element | ✅ | existing |
| Contained | merge + delete text | ✅ | **fixed**: split sibling cascade + moved children guard |
| Contained | merge + insert | ✅ | **fixed**: merge-tombstone redirect |
| Contained | merge + delete contents | ✅ | **fixed**: merge-tombstone redirect |
| Contained | merge + delete whole | ✅ | existing |
| Contained | merge + split merged node | ✅ | existing |
| Contained | merge + merge (different levels) | ✅ | existing |
| **Overlapping** | **merge + merge** | ❌ | DetachChild ↔ redirect conflict |
| **Contained** | **merge + merge (same level)** | ❌ | same conflict + range error |
| Side-by-side | merge + insert | ✅ | existing |
| Side-by-side | merge + delete | ✅ | existing |
| Side-by-side | merge + split | ✅ | existing |

### Split (Edit with splitLevel > 0)

| Range | Scenario | Status | Notes |
|-------|----------|--------|-------|
| Contained | split + split (same position) | ✅ | existing |
| Contained | split + split (different positions) | ✅ | existing |
| Contained | split + insert (into original) | ✅ | existing |
| Contained | split + insert (into split node) | ✅ | existing |
| Contained | split + insert (at split position) | ✅ | existing |
| Contained | split + delete contents | ✅ | existing |
| Contained | split + delete whole | ✅ | **fixed**: InsNextID cascade delete |
| **Contained** | **split + split (different levels)** | ❌ | multi-level position resolution conflict |
| **Side-by-side** | **split + insert** | ❌ | split sibling ordering with concurrent insert |
| **Side-by-side** | **split + delete** | ❌ | side-by-side cascade range insufficient |
| Side-by-side | split + split | ✅ | existing |
| Side-by-side | split + merge | ✅ | existing |

### Style

| Scenario | Status | Mechanism |
|----------|--------|-----------|
| style + style (all range combinations) | ✅ | RHT LWW |
| edit + style (all range combinations) | ✅ | nodeID-based style, position-independent |

### Summary

| Category | Total | ✅ Converge | ❌ Remaining |
|----------|-------|-------------|-------------|
| Basic Edit + Edit | ~27 | 27 | 0 |
| Merge | 12 | 10 | 2 |
| Split | 12 | 9 | 3 |
| Style | ~10 | 10 | 0 |
| **Total** | **~61** | **56** | **5** |

## Remaining Issues

### Issue 1: Double-merge convergence (2 tests)

**Tests**: `overlapping-merge-and-merge`, `contained-merge-and-merge-at-the-same-level`

**Root cause**: Fix 1 (DetachChild) and Fix 3 (redirect) conflict:
- DetachChild clears old parent's children list → redirect cannot find merge
  destination (scans empty children list)
- Without DetachChild → ghost references remain → double-merge diverges

**Proposed fix**: Add a runtime-only `mergedInto *TreeNodeID` forwarding
pointer to `TreeNode`. When merge tombstones a parent in Step 04, record
`oldParent.mergedInto = fromParent.id`. The redirect in Step 01 follows
`mergedInto` instead of scanning children. This decouples DetachChild from
redirect: children are cleanly detached, and the merge destination is still
discoverable.

No protobuf change needed — each replica computes `mergedInto` locally when
executing the merge operation. Both replicas execute the same merge, so they
set the same value.

### Issue 2: Multi-level split convergence (1 test)

**Test**: `contained-split-and-split-at-different-levels`

**Root cause**: When two clients split at different tree levels concurrently,
each split modifies the ancestor chain. When the remote split is applied,
`FindTreeNodesWithSplitText` resolves positions against a tree whose ancestor
structure has already been modified by the local split. The offset calculation
in `tree.split()` produces different results depending on application order.

**Status**: Needs deeper trace analysis. May require position resolution
changes in `tree.split()` or a different approach to multi-level split
coordinate mapping.

### Issue 3: Side-by-side split interactions (2 tests)

**Tests**: `side-by-side-split-and-insert`, `side-by-side-split-and-delete`

**Root cause**: `SplitElement` inserts the new node via `InsertAfterInternal`,
which places it immediately after the original. A concurrent insert or delete
at a side-by-side position interacts with this placement:
- Insert: the split sibling and the inserted element compete for ordering at
  the same level, producing different orders depending on timestamp.
- Delete: the cascade delete via `InsNextID` does not reach split siblings
  that are positionally side-by-side (not contained in the deletion range).

**Status**: Needs individual trace analysis. The insert ordering issue may
require explicit ordering rules for split siblings vs concurrent inserts. The
delete issue may need the cascade logic to also check side-by-side siblings.

## Design

### Fix 1: Split sibling cascade delete

**Location**: `CRDTTree.collectBetween`, `crdt/tree.go`.

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

### Fix 2: Moved children guard

**Location**: `CRDTTree.collectBetween`, `crdt/tree.go`.

When `collectBetween` detects a merge (Start token with `!ended`), the merge-
source node appears in both `toBeRemoveds` and `toBeMergedNodes`. Its children
are being moved, not deleted. The parent-cascade check
(`slices.Contains(toBeRemoveds, parent)`) must exclude nodes whose parent is
in `toBeMergedNodes` to prevent merge-moved children from being tombstoned.

### Fix 3: Merge-tombstone insert redirect

**Location**: `CRDTTree.FindTreeNodesWithSplitText`, `crdt/tree.go`.

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
code already follows `leftNode.Index.Parent.Value` to the correct parent.

### Fix interaction

- Fix 2 (moved children guard) prevents Fix 1's cascade from over-deleting
  merge-moved children.
- Fix 3 (redirect) currently relies on scanning the old parent's children
  list, which conflicts with DetachChild. See Remaining Issue 1 for the
  `mergedInto` forwarding pointer proposal that resolves this conflict.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Fix 1 cascade deletes too aggressively | Version vector check ensures only unknown-to-editor splits are cascaded. Element-only guard prevents text split interference |
| Fix 2 guard is too broad | Only applies when parent is in `toBeMergedNodes`, a pattern unique to merge |
| Fix 3 redirect fires on plain deletes (not merges) | Redirect only when a living child exists in a different living parent — a pattern unique to merge |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix implicit move instead of adding explicit Move operation | All three bugs require the same fixes regardless of approach. Move adds protocol complexity with no additional benefit for these cases |
| Element-only cascade for Fix 1 | Text splits use same-CreatedAt offset-based IDs that findFloorNode already resolves. Cascading text splits would break the existing split text semantics |
| No new fields on TreeNode (current phase) | Keeps protobuf unchanged. Merge destination is derived from live tree structure rather than stored pointers |
| No undo/redo in scope | Consistent with undo-redo.md Phase 2 deferral. Merge/split convergence is prerequisite |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Explicit `TreeMove` CRDT operation | Same three fixes needed regardless. Adds protobuf message, converter code, and operation type for no additional convergence benefit in Phase 1 |
| `mergedInto` pointer field on TreeNode | Originally rejected for Phase 1 to avoid new fields. Now identified as the likely solution for double-merge (Remaining Issue 1). Runtime-only field with no protobuf change is viable |
| Range-based Move (move all children after boundary) | Does not commute with concurrent inserts — causes divergence when insert and move are applied in different order |
| Fix only at JS SDK level (rewrite splitByPath/mergeByPath) | Does not fix the CRDT layer bugs. Go concurrency tests would still fail |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
