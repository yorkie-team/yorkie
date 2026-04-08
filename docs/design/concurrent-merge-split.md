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

```text
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
| **Overlapping** | **split + delete (overlapping text)** | ❌ | see Remaining Issues |
| Contained | split + delete whole | ✅ | InsNextID cascade delete |
| Contained | split + split (different levels) | ✅ | split sibling forwarding (Fix 7) |
| Contained | multi-level split + cross-boundary merge | ✅ | SplitElement merge-moved children skip (Fix 8) |
| Side-by-side | split + insert | ✅ | split sibling forwarding (Fix 7) |
| Side-by-side | split + delete | ✅ | split sibling forwarding (Fix 7) |
| Side-by-side | split + split | ✅ | existing |
| Side-by-side | split + merge | ✅ | existing |

### Style

| Scenario | Status | Mechanism |
|----------|--------|-----------|
| style + style (all range combinations) | ✅ | RHT LWW |
| edit + style (all range combinations) | ✅ | nodeID-based style, position-independent |

### Summary

| Category | Total | ✅ Converge | ❌ Remaining |
|----------|-------|-------------|--------------|
| Basic Edit + Edit | 27 | 27 | 0 |
| Merge | 12 | 12 | 0 |
| Split | 14 | 13 | 1 |
| Style | 10 | 10 | 0 |
| **Total** | **63** | **62** | **1** |

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
1. Record each moved child's ID on its **actual source parent** only
   (not all `toBeMergedNodes`), to prevent cross-contamination in
   multi-boundary merges.
2. `DetachChild` from old parent (correct lengths, prevent ghost references).
3. `Append` to `fromParent`.
4. Set `source.mergedInto = fromParent.id`.

This decouples DetachChild from redirect: children are cleanly detached,
and the merge destination is still discoverable via `mergedInto`.

### Fix 5: Delete propagation via mergedChildIDs

**Location**: `CRDTTree.Edit` Step 04-1 (after merge)

When a merge-source node is fully deleted (in `toBeRemoveds` but not in
`toBeMergedNodes`), its former children in the merge target should also be
deleted. Follow `mergedChildIDs` to find and tombstone them, including their
full subtree (descendants of moved element nodes).

Skip propagation when `mergedInto` points to `fromParent` — this means a
prior local merge already moved the children, and the current operation is a
concurrent merge (not a delete).

### Fix 6: Inverted range no-op

**Location**: `CRDTTree.traverseInPosRange`

When a concurrent merge redirects the to-position into an earlier part of the
tree (before the from-position), the traversal range becomes empty because
the merge already handled the work. Treat `from > to` as a no-op instead of
an error.

### Fix 7: Split sibling forwarding

**Location**: `CRDTTree.Edit` Step 01-1 (between position resolution and
`collectBetween`)

When `SplitElement` creates a split sibling linked via `InsNextID`, the
sibling is unknown to concurrent editors whose positions were computed
against the unsplit tree. After resolving `fromLeft`/`toLeft` via
`FindTreeNodesWithSplitText`, advance each past element-type split siblings
whose `CreatedAt` is not covered by the editor's version vector.

This prevents three classes of bugs:
1. **Multi-level split**: the remote split's boundary resolves after all
   concurrent split products, producing the correct ancestor split point.
2. **Side-by-side insert**: the insert position lands after all split
   siblings, not between original and sibling.
3. **Side-by-side delete**: the delete range starts after split siblings,
   preventing traversal from passing through them and tombstoning their
   text children.

Skip advancement when `leftNode == parent` (leftmost child position) to
preserve "insert at front" semantics.

### Fix 8: SplitElement skips merge-moved children

**Location**: `TreeNode.SplitElement`, `CRDTTree.mergeNodes`

When a multi-level split (splitLevel ≥ 2) and a cross-boundary delete+merge
operate concurrently, `SplitElement` may move merge-moved children to the
split sibling, causing divergence. The fix anchors merge-moved children in
the merge destination so `SplitElement` does not relocate them.

Add runtime-only `mergedFrom *TreeNodeID` field to `TreeNode`. When merge
moves a child from its source parent to `fromParent`:
1. Set `child.mergedFrom = sourceParent.id` before detach and append.

In `SplitElement`, when partitioning children into left/right:
1. Children in the right partition whose `mergedFrom` is set are kept in the
   original node (appended to the left partition) instead of moving to the
   split sibling.

**Convergence proof** (main scenario):

```text
Initial: <root><p><p>ab</p><p>cd</p></p></root>
d1: Edit(3,3,nil,2) — split 'a|b' at level 2
d2: Edit(1,6,nil,0) — delete first inner <p>

d1 (split → merge): split creates outer_p', merge moves "cd" to outer_p.
  mergedFrom set on "cd" but split already done → no effect.
  Result: outer_p has "cd".

d2 (merge → split): merge moves "cd" to outer_p, sets mergedFrom.
  SplitElement on outer_p: "cd" has mergedFrom → skip, stays in outer_p.
  Result: outer_p has "cd".

Both: <root><p>cd</p><p></p></root> ✅
```

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Fix 1 cascade deletes too aggressively | Version vector check ensures only unknown-to-editor splits are cascaded. Element-only guard prevents text split interference |
| Fix 2 guard is too broad | Only applies when parent is in `toBeMergedNodes`, a pattern unique to merge |
| Fix 3 redirect fires on plain deletes | Redirect only when mergedInto is set or a living child exists in a different living parent |
| Fix 5 propagation deletes too much | Skip when mergedInto == fromParent (concurrent merge, not delete) |
| Fix 7 advances past known siblings | Version vector check ensures only unknown siblings are skipped. `leftNode == parent` guard preserves leftmost-child semantics |
| Fix 8 skips too many children | Only children with `mergedFrom` set are skipped. `mergedFrom` is only set during merge, so normal children are unaffected |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix implicit move instead of explicit Move operation | All bugs require the same fixes regardless. Move adds protocol complexity with no additional convergence benefit |
| Element-only cascade for Fix 1 | Text splits use same-CreatedAt offset-based IDs that findFloorNode already resolves |
| Runtime-only mergedInto/mergedChildIDs | Keeps protobuf unchanged. Each replica computes locally during merge execution |
| Position-level fix for split siblings (Fix 7) | collectBetween-level fix proved infeasible — cannot distinguish contained delete (text should die) from side-by-side delete (text should survive) |
| No undo/redo in scope | Consistent with undo-redo.md Phase 2 deferral |
| Runtime-only `mergedFrom` on child nodes | Same pattern as `mergedInto`/`mergedChildIDs`. No protobuf change needed |
| Skip in SplitElement rather than post-reconciliation | Filtering during partition is simpler and preserves child ordering naturally |

## Remaining Issues

### Split + delete on overlapping text content

**Test**: `split-with-concurrent-delete-overlapping-content`

When one client deletes text and another concurrently splits the same
paragraph at a position inside the deleted range, the replicas diverge.

**Scenario**:
```text
Initial: <root><p>abcd</p></root>
d1: Edit(2,4,nil,0) — delete "bc"
d2: Edit(3,3,nil,1) — split at b|c with splitLevel=1
```

**Root cause**: The split position (between 'b' and 'c') resolves inside
tombstoned content on the replica that applied the delete first. The
position resolution and offset calculation produce different structural
splits depending on operation order:

| Replica | Delete first? | Result |
|---------|--------------|--------|
| d1 | ✅ yes | `<root><p>a</p><p>d</p></root>` |
| d2 | ❌ no | `<root><p>ad</p><p></p></root>` |

### JS SDK: splitElement drops tombstoned children

**Location**: `IndexTreeNode.splitElement` in `index_tree.ts`

The `children` getter (line 323) filters out removed nodes:
```typescript
get children(): Array<T> {
  return this._children.filter((child) => !child.isRemoved);
}
```

`splitElement` uses this getter to partition children, then reassigns
`_children` from the filtered result. Tombstoned children are silently
dropped from the tree structure. In Go, `Children(true)` includes removed
nodes so they survive the split.

This does not cause visible divergence (tombstoned nodes are invisible in
`toXML()`), but it can affect:
- GC bookkeeping: orphaned tombstones cannot be collected
- Subsequent operations that reference tombstoned nodes by ID
- Tree size/index calculations

**Fix approach**: Use `_children` (raw array) instead of `children` (filtered
getter) in `splitElement`, and compute the split offset against the full
child list including removed nodes.

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Explicit `TreeMove` CRDT operation | Same fixes needed regardless. Adds protocol complexity with no additional convergence benefit |
| `mergedInto` as protobuf field | Runtime-only field suffices. No serialization needed since each replica computes it locally |
| Range-based Move (move all children after boundary) | Does not commute with concurrent inserts — divergence when applied in different order |
| Fix only at JS SDK level | Does not fix CRDT layer bugs. Go concurrency tests would still fail |
| Parent creation guard for split text nodes | Cannot distinguish contained delete (text should die) from side-by-side delete (text should survive) at collectBetween level |
| Always advance past split siblings (no VV check) | Breaks when editor knew about the split and intentionally positioned between original and sibling |
| Advance only fromLeft, not toLeft | Delete ranges need toLeft advancement to include split siblings of range-end node |
| Merge redirects to split sibling (Fix 8 alt) | Merge would need structural awareness of splits. Direction ambiguous when multiple siblings exist |
| Deterministic container selection (Fix 8 alt) | Requires generic comparison rule that interacts with all other fixes. Broad change surface |
| Post-split reconciliation (move children back) | Error-prone ordering, harder to reason about than filtering during partition |
