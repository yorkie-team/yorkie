---
title: concurrent-merge-split
target-version: 0.7.4
---

# Concurrent Merge and Split

## Problem

When two clients concurrently perform merge or split operations on the same
tree region, the replicas diverge after synchronization. This violates the
fundamental CRDT convergence guarantee.

### Goals

- Fix convergence bugs in concurrent merge/split so integration tests pass.
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
  │            ├── DetachChild from old parent (prevent ghost references)
  │            ├── Append to fromParent
  │            └── Set mergedInto/mergedChildIDs on source node
  │
  ├── Step 04-1: Propagate deletes to children moved by prior merges
  │              (mergedChildIDs, skip when mergedInto == fromParent)
  │
  ├── Step 05: Split — SplitElement for splitLevel > 0
  │
  └── Step 06: Insert — insert contents at fromParent
               └── concurrent parent deletion guard
               └── merge-tombstone redirect via mergedInto
```

### Basic Edit + Edit (insert, delete, replace)

All 27 cases from `tree.md` converge:

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

| Range | Scenario | Status | Mechanism |
|-------|----------|--------|-----------|
| Contained | merge + delete element | ✅ | existing |
| Contained | merge + delete text | ✅ | split sibling cascade + moved children guard |
| Contained | merge + insert | ✅ | merge-tombstone redirect via mergedInto |
| Contained | merge + delete contents | ✅ | merge-tombstone redirect via mergedInto |
| Contained | merge + delete whole | ✅ | existing |
| Contained | merge + split merged node | ✅ | existing |
| Contained | merge + merge (different levels) | ✅ | existing |
| Overlapping | merge + merge | ✅ | mergedInto forwarding + mergedChildIDs propagation |
| Contained | merge + merge (same level) | ✅ | mergedInto + inverted range no-op |
| Side-by-side | merge + insert | ✅ | existing |
| Side-by-side | merge + delete | ✅ | existing |
| Side-by-side | merge + split | ✅ | existing |

### Split (Edit with splitLevel > 0)

| Range | Scenario | Status | Mechanism |
|-------|----------|--------|-----------|
| Contained | split + split (same position) | ✅ | existing |
| Contained | split + split (different positions) | ✅ | existing |
| Contained | split + insert (into original) | ✅ | existing |
| Contained | split + insert (into split node) | ✅ | existing |
| Contained | split + insert (at split position) | ✅ | existing |
| Contained | split + delete contents | ✅ | existing |
| Contained | split + delete whole | ✅ | InsNextID cascade delete |
| **Contained** | **split + split (different levels)** | ❌ | see Remaining Issue 1 |
| **Side-by-side** | **split + insert** | ❌ | see Remaining Issue 2 |
| **Side-by-side** | **split + delete** | ❌ | see Remaining Issue 2 |
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
| Merge | 12 | 12 | 0 |
| Split | 12 | 9 | 3 |
| Style | ~10 | 10 | 0 |
| **Total** | **~61** | **58** | **3** |

## Design

### Fix 1: Split sibling cascade delete

**Location**: `CRDTTree.collectBetween`

When an element node is marked for deletion, its `InsNextID` chain may contain
split siblings created by a concurrent `SplitElement`. If the version vector
does not know about the sibling's creation, the sibling was created by a
concurrent split and should be included in the deletion.

Only applies to element nodes. Text splits use offset-based IDs with the same
`CreatedAt`, so `findFloorNode` already resolves them correctly.

### Fix 2: Moved children guard

**Location**: `CRDTTree.collectBetween`

When `collectBetween` detects a merge (Start token with `!ended`), the merge-
source node appears in both `toBeRemoveds` and `toBeMergedNodes`. Its children
are being moved, not deleted. The parent-cascade check must exclude nodes
whose parent is in `toBeMergedNodes` to prevent merge-moved children from
being tombstoned by concurrent inserts.

### Fix 3: Merge-tombstone insert redirect

**Location**: `CRDTTree.FindTreeNodesWithSplitText`

When `FindTreeNodesWithSplitText` resolves a position whose parent has been
tombstoned by a merge, the insert should be redirected to the merge
destination. Uses `mergedInto` forwarding pointer when available (set by
Fix 4), otherwise scans the old parent's children for a child living in a
different parent.

### Fix 4: mergedInto forwarding pointer

**Location**: `CRDTTree.Edit` Step 04 (merge), `TreeNode` struct

Add runtime-only `mergedInto *TreeNodeID` and `mergedChildIDs []*TreeNodeID`
fields to `TreeNode`. No protobuf change — each replica computes these locally
when executing the merge operation.

When merge moves children from a source to `fromParent`:
1. Record each moved child's ID in `source.mergedChildIDs`.
2. `DetachChild` from old parent (correct lengths, prevent ghost references).
3. `Append` to `fromParent`.
4. Set `source.mergedInto = fromParent.id`.

This decouples DetachChild from redirect: children are cleanly detached,
and the merge destination is still discoverable via `mergedInto`.

### Fix 5: Delete propagation via mergedChildIDs

**Location**: `CRDTTree.Edit` Step 04-1 (after merge)

When a merge-source node is fully deleted (in `toBeRemoveds` but not in
`toBeMergedNodes`), its former children in the merge target should also be
deleted. Follow `mergedChildIDs` to find and tombstone them.

Skip propagation when `mergedInto` points to `fromParent` — this means a
prior local merge already moved the children, and the current operation is a
concurrent merge (not a delete).

### Fix 6: Inverted range no-op

**Location**: `CRDTTree.traverseInPosRange`

When a concurrent merge redirects the to-position into an earlier part of the
tree (before the from-position), the traversal range becomes empty because
the merge already handled the work. Treat `from > to` as a no-op instead of
an error.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Fix 1 cascade deletes too aggressively | Version vector check ensures only unknown-to-editor splits are cascaded. Element-only guard prevents text split interference |
| Fix 2 guard is too broad | Only applies when parent is in `toBeMergedNodes`, a pattern unique to merge |
| Fix 3 redirect fires on plain deletes | Redirect only when mergedInto is set or a living child exists in a different living parent |
| Fix 5 propagation deletes too much | Skip when mergedInto == fromParent (concurrent merge, not delete) |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix implicit move instead of explicit Move operation | All bugs require the same fixes regardless. Move adds protocol complexity with no additional convergence benefit |
| Element-only cascade for Fix 1 | Text splits use same-CreatedAt offset-based IDs that findFloorNode already resolves |
| Runtime-only mergedInto/mergedChildIDs | Keeps protobuf unchanged. Each replica computes locally during merge execution |
| No undo/redo in scope | Consistent with undo-redo.md Phase 2 deferral |

## Remaining Issues

### Issue 1: Multi-level split convergence

**Test**: `contained-split-and-split-at-different-levels`

When two clients split at different tree levels concurrently, each split
modifies the ancestor chain. The remote split's position resolves against a
tree whose ancestor structure has already been modified by the local split.
The offset calculation in `tree.split()` produces different results depending
on application order.

### Issue 2: Side-by-side split interactions

**Tests**: `side-by-side-split-and-insert`, `side-by-side-split-and-delete`

`SplitElement` creates a new node with a fresh `CreatedAt` (unknown to the
remote client) but its text children inherit the original `CreatedAt` (known).
When a concurrent operation's traversal range passes through the split sibling,
the text children pass `canDelete` (creationKnown=true) while the parent
element doesn't (creationKnown=false). This mismatch causes text children to
be deleted from an otherwise-surviving split element.

Fixing this at the `collectBetween` level proved infeasible: the same
conditions (text node with offset > 0, parent not in toBeRemoveds) appear in
both cases where the text should be deleted (contained delete) and where it
should be protected (side-by-side delete). A solution likely requires
preventing the traversal from passing through the split sibling in the first
place, via changes to `FindTreeNodesWithSplitText` position resolution.

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Explicit `TreeMove` CRDT operation | Same fixes needed regardless. Adds protocol complexity with no additional convergence benefit |
| `mergedInto` as protobuf field | Runtime-only field suffices. No serialization needed since each replica computes it locally |
| Range-based Move (move all children after boundary) | Does not commute with concurrent inserts — divergence when applied in different order |
| Fix only at JS SDK level | Does not fix CRDT layer bugs. Go concurrency tests would still fail |
| Parent creation guard for split text nodes | Cannot distinguish contained delete (text should die) from side-by-side delete (text should survive) at collectBetween level |
