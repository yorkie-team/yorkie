---
title: concurrent-merge-split
target-version: 0.7.5
---

# Concurrent Merge and Split

## Problem

When two clients concurrently perform merge or split operations on the same
tree region, replicas diverge after synchronization.

### Goals

- Fix convergence bugs in concurrent merge/split.
- Preserve backward compatibility: no protobuf or protocol changes (except
  persisting `MergedFrom`/`MergedAt` on snapshot — backwards-compatible).

### Non-Goals

- Undo/redo for merge/split (deferred per `undo-redo.md` Phase 2).
- General-purpose `Tree.Move` operation (Phase 2).

## Edit Execution Flow

```text
Edit(from, to, contents, splitLevel)
  ├── 01:   FindTreeNodesWithSplitText(from/to) — resolve CRDTTreePos
  ├── 01-1: advancePastUnknownSplitSiblings (Fix 7)
  ├── 02:   collectBetween — walk range, detect merge/delete targets
  ├── 03:   Delete — tombstone nodes
  ├── 04:   Merge — move children to fromParent, set mergedInto
  ├── 04-1: Propagate deletes to merge-moved children (Fix 5)
  ├── 05:   Split — SplitElement for splitLevel > 0
  └── 06:   Insert — with parent-deletion guard + merge redirect
```

## Fixes

### Fix 1: Split sibling cascade delete

**`collectBetween`** — When deleting an element, follow its `InsNextID`
chain to include split siblings unknown to the editor's VV. Element-only;
text splits use same-CreatedAt offset IDs that `findFloorNode` resolves.

### Fix 2: Moved children guard

**`collectBetween`** — Exclude children whose parent is in
`toBeMergedNodes` from the parent-cascade delete. These children are
being moved by merge, not deleted.

### Fix 3: Merge-tombstone insert redirect

**`FindTreeNodesWithSplitText`** — When the resolved parent is
tombstoned by a merge, redirect to the merge destination via `mergedInto`.

### Fix 4: mergedInto forwarding pointer

**`Edit` Step 04, `TreeNode`** — Runtime cache `mergedInto *TreeNodeID`
on merge-source nodes. Set during merge; rebuilt from `MergedFrom` on
snapshot load via `rebuildMergeState`. Enables fast nil-check in
`FindTreeNodesWithSplitText`.

### Fix 5: Delete propagation to merge-moved children

**`Edit` Step 04-1** — When a merge-source is deleted (not merged),
tombstone its former children in the merge target. Identified via
`child.MergedFrom == source.id`. Skip when `mergedInto == fromParent`
(concurrent merge, not delete).

### Fix 6: Inverted range no-op

**`traverseInPosRange`** — When a merge redirects `to` before `from`,
treat the empty range as a no-op.

### Fix 7: Split sibling forwarding

**`Edit` Step 01-1, `Style`, `RemoveStyle`** — After resolving positions,
advance `fromLeft`/`toLeft` past element split siblings whose `CreatedAt`
is not in the editor's VV. Skip when `leftNode == parent` (leftmost).
Prevents multi-level split mispositioning, side-by-side insert/delete
errors.

### Fix 8: SplitElement skips merge-moved children

**`SplitElement`, `mergeNodes`** — Persist `MergedFrom` (source parent ID)
and `MergedAt` (immutable merge ticket) on moved children. In
`SplitElement`, keep right-partition children with `MergedFrom` in the
original node when the merge source was a child of the split node and
the editor's VV doesn't cover `MergedAt`. `MergedAt` is captured
explicitly because `source.removedAt` can be overwritten by LWW.

### Fix 9: Skip merge for concurrent elements

**`collectBetween`** — When a delete range crosses into an element whose
`CreatedAt` is not in the editor's VV, skip merge detection. The boundary
crossing is an artifact of a concurrent split.

### Fix 10: JS splitElement preserves tombstoned children

**`index_tree.ts`** — Use `_children` (raw) instead of `children`
(filtered) in `splitElement` to preserve tombstoned children across
splits.

### Fix 11: End-token split sibling guard for Style/RemoveStyle

**`Style`, `RemoveStyle` callbacks** — When processing an End token,
skip styling if the node has an `InsNextID` split sibling not in the
editor's VV. The End token is in the range only because a concurrent
split extended the traversal past the original element boundary.

Helper: `hasUnknownSplitSibling(node, vv)` — follows `InsNextID`, checks
the sibling is an element whose `CreatedAt` is not covered by VV.

**Why End-token guard over range clamping**: Clamping works for text-level
ranges but fails for element-level ranges where the editor selected
children that moved to the split sibling. The guard handles both
uniformly:

- **Text-level** (range within element content): P's End token skipped →
  P' Start rejected by `canStyle` → no elements styled. Matches the
  unsplit behavior (no element tokens in range).
- **Element-level** (range across children): parent End token skipped →
  child Start token still styled via `canStyle`. Matches the unsplit
  behavior (child was in the original range).

## Convergence Coverage

### Hand-crafted integration suite (`test/integration/tree_test.go`)

| Category | Total | Pass |
|----------|------:|-----:|
| Basic Edit + Edit | 27 | 27 |
| Merge | 12 | 12 |
| Split | 14 | 14 |
| Style | 16 | 16 |
| **Total** | **69** | **69** |

### Property-based suite (`test/complex/tree_concurrency_test.go`)

| Suite | Pass | Skip (divergence) |
|---|---:|---:|
| EditEdit | 901 | 0 |
| StyleStyle | 145 | 0 |
| EditStyle | 85 | 0 |
| SplitSplit | 275 | 46 |
| SplitEdit | 139 | 7 |
| **Total** | **1545** | **53** |

All 53 remaining divergences are `splitLevel >= 2` only.

## Key Design Decisions

| Decision | Reason |
|----------|--------|
| Implicit move over explicit `TreeMove` op | Same fixes needed; Move adds protocol complexity |
| Persist `MergedFrom` + `MergedAt` in proto | Durable merge witness; `MergedAt` is immutable (unlike `removedAt` which LWW overwrites) |
| `mergedInto` as runtime cache only | Fast nil-check on hot path; rebuilt from `MergedFrom` on load |
| Derive moved-children on demand | `target.Children(true) | where MergedFrom == source.id`; call sites already have the target |
| Position-level fix for splits (Fix 7) | `collectBetween`-level fix can't distinguish contained vs side-by-side delete |
| End-token guard over range clamping (Fix 11) | Clamping fails for element-level ranges; guard handles both text and element uniformly |

## Remaining Issue: `splitLevel >= 2`

All remaining divergences (53 in the property-based suite) involve at
least one `splitLevel = 2` operation. `splitLevel = 1` is fully clean
across all operation types (edit, merge, split, style, remove-style).

The recursive split path does not propagate Fix 7 (split sibling
forwarding) or Fix 8 (`MergedFrom` filter) to ancestor split nodes.
