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
| Overlapping | split + delete (overlapping text) | ✅ | concurrent element merge skip (Fix 9) |
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
| Split | 14 | 14 | 0 |
| Style | 10 | 10 | 0 |
| **Total** | **63** | **63** | **0** |

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

Add a runtime `mergedInto *TreeNodeID` cache on the source parent. A
replica that runs the merge operation locally sets it during Step 04.
A replica that loads the document from a snapshot rebuilds it via
`Tree.rebuildMergeState` from the persisted `MergedFrom` field on the
moved children (see Fix 8). `mergedInto` is a cache only — it enables
a fast nil-check on the hot path (`FindTreeNodesWithSplitText`); the
alternative of scanning `NodeMapByID` on every position resolution
would be too expensive.

When merge moves children from a source to `fromParent`:
1. Set `child.MergedFrom = sourceParent.id` on the moved child (the
   single persisted witness).
2. `DetachChild` from old parent (correct lengths, prevent ghost references).
3. `Append` to `fromParent`.
4. Set `source.mergedInto = fromParent.id`.

This decouples DetachChild from redirect: children are cleanly detached,
and the merge destination is still discoverable via `mergedInto`.

### Fix 5: Delete propagation to merge-moved children

**Location**: `CRDTTree.Edit` Step 04-1 (after merge)

When a merge-source node is fully deleted (in `toBeRemoveds` but not in
`toBeMergedNodes`), its former children in the merge target should also
be deleted. The list of moved children is recomputed on the fly from
the merge target's children filtered by `MergedFrom`:

```text
mergeTarget = findFloorNode(source.mergedInto)
moved = [c for c in mergeTarget.Children(true) if c.MergedFrom == source.id]
```

The moved children (and their full subtrees) are tombstoned.

Skip propagation when `mergedInto` points to `fromParent` — this means
a prior local merge already moved the children, and the current
operation is a concurrent merge (not a delete).

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

Add two persisted fields on moved children: `MergedFrom *TreeNodeID`
(the source parent ID) and `MergedAt *time.Ticket` (the immutable
merge ticket). Both are written to the snapshot encoding
(`TreeNode.merged_from` and `TreeNode.merged_at` in
`resources.proto`). When merge moves a child from its source parent
to `fromParent`:
1. Set `child.MergedFrom = sourceParent.id`.
2. Set `child.MergedAt = editedAt`.
3. Detach and append.

`MergedAt` must be captured explicitly at merge time rather than read
from `source.removedAt` at use time: the source's `removedAt` is
mutated by LWW overwrite when a later concurrent tombstone targets the
same node, which would corrupt the merge-time causal boundary.

On snapshot load, `Tree.rebuildMergeState` walks the tree and uses
`MergedFrom` to set `source.mergedInto = target.id` (the moved
child's current parent). For backwards compatibility with snapshots
written before `MergedAt` was added, it falls back to
`source.removedAt` — approximate but the best available without the
persisted ticket.

In `SplitElement`, when partitioning children into left/right:
1. A child in the right partition whose `MergedFrom` is set is kept
   in the original node when (a) the merge source was a child of the
   node being split and (b) the editor's version vector does not
   cover `child.MergedAt`. Everything else flows naturally to the
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

### Fix 9: Skip merge for concurrent elements

**Location**: `CRDTTree.collectBetween`

When a delete range crosses into an element that was created by a concurrent
operation (not covered by the editor's version vector), the delete should not
trigger a merge. The element boundary was unknown to the editor, so the range
crossing is an artifact of the concurrent split, not an intentional merge.

In `collectBetween`, at the merge detection point (`Start && !ended`), check
whether the element's `CreatedAt` is covered by the editor's version vector.
If not, skip adding it to `toBeMergedNodes` and `toBeMovedToFromParents`.
Text content inside the concurrent element is still tombstoned individually
via the existing `canDelete` check.

**Convergence proof**:

```text
Initial: <root><p>abcd</p></root>
d1: Edit(2,4,nil,0) — delete "bc"
d2: Edit(3,3,nil,1) — split at b|c with splitLevel=1

d1 (delete → split): "bc" tombstoned. d2 split resolves at tombstone
  boundary, SplitElement partitions ["a",†"b"] | [†"c","d"].
  Result: <p>a</p><p>d</p>.

d2 (split → delete): split creates <p>ab</p><p'>cd</p'>.
  d1 delete: collectBetween(p,"a" → p',"c").
  p' Start: p'.CreatedAt not in d1's VV → skip merge.
  "b" canDelete → tombstoned. "c" canDelete → tombstoned.
  "d" outside range → survives in p'.
  Result: <p>a</p><p>d</p>.

Both: <root><p>a</p><p>d</p></root> ✅
```

Intentional merges are unaffected: the target element existed when the editor
created the operation, so its `CreatedAt` is always covered by the editor's
version vector.

### Fix 10: JS splitElement preserves tombstoned children

**Location**: `IndexTreeNode.splitElement` in `index_tree.ts`

The `children` getter filters out removed nodes. `splitElement` used this
getter to partition children, then reassigned `_children` from the filtered
result. Tombstoned children were silently dropped from the tree structure.

**Fix**: Use `_children` (raw array) instead of `children` (filtered getter)
in `splitElement`. This matches Go's `Children(true)` behavior and preserves
tombstoned children across splits.

Additionally fixed `clone.visibleSize` calculation: was using
`paddedSize(true)` (total) instead of `paddedSize()` (visible), diverging
from Go's `PaddedLength()` usage for visible length.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Fix 1 cascade deletes too aggressively | Version vector check ensures only unknown-to-editor splits are cascaded. Element-only guard prevents text split interference |
| Fix 2 guard is too broad | Only applies when parent is in `toBeMergedNodes`, a pattern unique to merge |
| Fix 3 redirect fires on plain deletes | Redirect only when mergedInto is set or a living child exists in a different living parent |
| Fix 5 propagation deletes too much | Skip when mergedInto == fromParent (concurrent merge, not delete) |
| Fix 7 advances past known siblings | Version vector check ensures only unknown siblings are skipped. `leftNode == parent` guard preserves leftmost-child semantics |
| Fix 8 skips too many children | Only children with `mergedFrom` set are skipped. `mergedFrom` is only set during merge, so normal children are unaffected |
| Fix 9 skips merge for known elements | Only elements whose CreatedAt is not covered by editor's VV are skipped. Intentional merges target elements the editor knew about |
| Fix 10 changes splitElement child visibility | Uses raw `_children` array matching Go's `Children(true)`. Tombstoned children were already invisible in toXML output |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Fix implicit move instead of explicit Move operation | All bugs require the same fixes regardless. Move adds protocol complexity with no additional convergence benefit |
| Element-only cascade for Fix 1 | Text splits use same-CreatedAt offset-based IDs that findFloorNode already resolves |
| Persist `MergedFrom` + `MergedAt` in proto | `MergedFrom` is the witness of the merge relationship; `MergedAt` is the immutable merge ticket that Fix 8 needs. The source's `removedAt` can be overwritten by later LWW tombstones, so it cannot substitute for `MergedAt`. Other merge state (`mergedInto`, the list of moved children) is still derived at load time or on demand |
| Keep `mergedInto` as a runtime cache | Needed for a fast nil-check on the hot path (`FindTreeNodesWithSplitText`). Rebuilding it lazily per call would require scanning `NodeMapByID` every position resolution |
| Derive moved-children list on demand | `target.Children(true) \| where MergedFrom == source.id` gives the list. Both call sites (`propagateMergeDeletes`, the redirect in `FindTreeNodesWithSplitText`) already have the target in hand, so the cost is a short filter. Avoids carrying a redundant slice on every TreeNode |
| Position-level fix for split siblings (Fix 7) | collectBetween-level fix proved infeasible — cannot distinguish contained delete (text should die) from side-by-side delete (text should survive) |
| No undo/redo in scope | Consistent with undo-redo.md Phase 2 deferral |
| `MergedFrom` persisted on child nodes | The moved child is the only durable witness of the merge after snapshot roundtrip (source parent's linkage is otherwise lost once tombstoned). Single optional proto field; backwards-compatible |
| Skip in SplitElement rather than post-reconciliation | Filtering during partition is simpler and preserves child ordering naturally |
| VV check in collectBetween for Fix 9 | Same pattern as other fixes. Cleanly distinguishes intentional merge (editor knew the element) from accidental boundary crossing (concurrent split) |
| Fix split position resolution instead of merge skip (Fix 9 alt) | Changing offset semantics in FindTreeNodesWithSplitText affects all text operations. collectBetween-level fix is more isolated |

## Remaining Issues

### Hand-crafted integration suite: 63/63 passing

All 63 hand-crafted convergence cases in `test/integration/tree_test.go`
pass. These are the scenarios Fix 1–10 were designed against.

### Property-based suite: 1528/1597 passing, 69 silent divergences

`test/complex/tree_concurrency_test.go` enumerates operation combinations
over `TestTreeConcurrency{EditEdit, SplitSplit, SplitEdit, StyleStyle,
EditStyle}`. When two clients diverge it calls `t.Skip(...)` instead of
`t.Fail`, so CI stays green but the divergences are still observable in
verbose output.

Result at `d7154c27` (`-tags complex`):

| Suite | Pass | Skip (divergence) | Fail |
|---|---:|---:|---:|
| `TestTreeConcurrencyEditEdit` | 901 | 0 | 0 |
| `TestTreeConcurrencyStyleStyle` | 145 | 0 | 0 |
| `TestTreeConcurrencyEditStyle` | 85 | 0 | 0 |
| `TestTreeConcurrencySplitSplit` | 275 | **46** | 0 |
| `TestTreeConcurrencySplitEdit` | 122 | **23** | 0 |
| **Total** | **1528** | **69** | **0** |

The 69 divergences reduce to **two independent root causes**.

#### Root cause A: `splitLevel ≥ 2` (multi-level split)

63 of the 69 divergences (91%) involve at least one operation with
`splitLevel = 2`.

`SplitSplit` cross-tabulated by the split level of each client:

```text
         op2=L1  op2=L2
op1=L1      0      16
op1=L2     18      12
```

Pure level-1 × level-1 concurrent splits fully converge. Every
`SplitSplit` divergence has at least one `splitLevel = 2` operation.

`SplitEdit` distribution:

| split level | divergent cases |
|---|---|
| `splitLevel = 1` | 6 (all TreeStyle — see root cause B) |
| `splitLevel = 2` | 17 (delete 2, replace 2, insert 3, style 5, remove-style 5) |

Fix 1–10 only reason about single-level splits. `splitLevel = 2` is
implemented by recursively splitting the parent after the child split,
but the recursive path does not propagate Fix 7 (split sibling forwarding
in `FindTreeNodesWithSplitText`) or Fix 8 (`MergedFrom` filter in
`SplitElement`) to the newly created ancestor split nodes.

#### Root cause B: TreeStyle does not apply Fix 7 to split siblings

6 divergences in `SplitEdit` are pure `splitLevel = 1` × TreeStyle:

```text
A_contains_B(split-1, style | remove-style)
right_node(text)(split-1, style | remove-style)
right_node(element)(split-1, style | remove-style)
```

TreeEdit at `splitLevel = 1` is fully clean — the regressions are
exclusive to the style path. TreeStyle's range traversal does not go
through the Fix 7 branch in `FindTreeNodesWithSplitText` that skips
unknown split siblings, so style ranges over a concurrently split region
apply on a different node set on each replica.

#### Scope observations

- Zero divergences involve the `merge` operation. The merge fixes from
  PR #1722–#1727 and the snapshot fix from PR #1729 are not implicated
  in any skipped case in this suite.
- `EditEdit` (the largest pre-#1722 divergence class) is 901/901 clean.
  No regressions from the recent merge/split work.
- The two root causes are independent: fixing multi-level split would
  clear 63 cases but leave the 6 `splitLevel = 1` TreeStyle cases; the
  TreeStyle fix is small and well-scoped, likely a direct port of Fix 7
  into TreeStyle's traversal path.

These are tracked as follow-up work, not as regressions of the merge
runtime-state fix.

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Explicit `TreeMove` CRDT operation | Same fixes needed regardless. Adds protocol complexity with no additional convergence benefit |
| `mergedInto`/`mergedChildIDs` as protobuf fields | Derivable from `MergedFrom` + tree structure at load time. Adding them would duplicate information already implicit in the loaded children |
| Stored `mergedChildIDs` list on `TreeNode` | Recomputed on demand via `target.Children(true) \| where MergedFrom == source.id`. The call sites (`propagateMergeDeletes`, the redirect branch in `FindTreeNodesWithSplitText`) are not on the hot path, so storing the list would be needless state |
| Deriving `MergedAt` from `source.removedAt` | Rejected after code review: `source.removedAt` is not immutable — a later concurrent delete can overwrite it via LWW, producing a wrong causal boundary for the SplitElement Fix 8 check. `MergedAt` must be captured explicitly at merge time and persisted alongside `MergedFrom` |
| Range-based Move (move all children after boundary) | Does not commute with concurrent inserts — divergence when applied in different order |
| Fix only at JS SDK level | Does not fix CRDT layer bugs. Go concurrency tests would still fail |
| Parent creation guard for split text nodes | Cannot distinguish contained delete (text should die) from side-by-side delete (text should survive) at collectBetween level |
| Always advance past split siblings (no VV check) | Breaks when editor knew about the split and intentionally positioned between original and sibling |
| Advance only fromLeft, not toLeft | Delete ranges need toLeft advancement to include split siblings of range-end node |
| Merge redirects to split sibling (Fix 8 alt) | Merge would need structural awareness of splits. Direction ambiguous when multiple siblings exist |
| Deterministic container selection (Fix 8 alt) | Requires generic comparison rule that interacts with all other fixes. Broad change surface |
| Post-split reconciliation (move children back) | Error-prone ordering, harder to reason about than filtering during partition |
| Fix split position resolution for overlapping delete (Fix 9 alt) | Changing offset semantics in SplitText/FindTreeNodesWithSplitText affects all text operations. Broad change surface with high regression risk |
| Post-reconciliation for split boundaries (Fix 9 alt) | Does not fit the operation-based model. Complex correctness proof |
| JS splitElement with visible-to-all offset conversion (Fix 10 alt) | Adds complexity without benefit. Go uses same visible offset with Children(true) and passes all tests |
