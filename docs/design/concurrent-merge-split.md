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

## Text vs Element ID Asymmetry

Understanding this asymmetry is essential for the rest of the document.

- **Text split nodes** have deterministic IDs: same `CreatedAt` + offset.
  Any client that knows the original node can resolve the split via
  `findFloorNode`. No discovery problem.
- **Element split nodes** have non-deterministic IDs: each split creates a
  new ticket via `issueTimeTicket`. A client that didn't witness the split
  cannot find the product by ID alone.
- **`InsNextID`/`InsPrevID` chain** is the sole discovery mechanism for
  concurrent element split products. `SplitElement` links the new (right)
  node into this doubly-linked list. Every phase that must account for
  concurrent element splits walks this chain.

This asymmetry is why Phase 7 (Split) needs per-iteration sibling
advancement in the recursive loop — each ancestor-level element split
produces a new undiscoverable ID.

## Edit Execution Flow

```text
Edit(from, to, contents, splitLevel)
  ├── Phase 1: Position Resolution (FindTreeNodesWithSplitText)
  ├── Phase 2: Split Sibling Advance (advancePastUnknownSplitSiblings)
  ├── Phase 3: Range Narrowing (cross-parent boundary)
  ├── Phase 4: Range Collection (collectBetween)
  ├── Phase 5: Delete — tombstone collected nodes
  ├── Phase 6: Merge — move children to fromParent, set mergedInto
  │     └── Delete Propagation to merge-moved children
  ├── Phase 7: Split — SplitElement for splitLevel > 0
  └── Phase 8: Insert — with parent-deletion guard + merge redirect
```

## Phase 1: Position Resolution

**Function**: `FindTreeNodesWithSplitText`

Resolves `CRDTTreePos` (parent ID + left sibling ID) into concrete tree
node pointers. Two concurrent issues can corrupt resolution results.

### §1.1 Merge-Tombstone Redirect

When the resolved parent has been tombstoned by a concurrent merge,
its children have already been moved to the merge destination. The
`mergedInto` forwarding pointer redirects resolution to that
destination.

`mergedInto` is a runtime cache on `TreeNode`, set during merge
execution and rebuilt from the persisted `MergedFrom` field on
snapshot load via `rebuildMergeState`. This enables a fast nil-check
on the hot path without persisting a separate field.

### §1.2 Inverted Range Guard

**Function**: `traverseInPosRange`

When a merge redirect places the `to` position before `from` in tree
order, the range is inverted. This is treated as a no-op
(`if fromIdx > toIdx { return nil }`).

## Phase 2: Split Sibling Advance

**Function**: `advancePastUnknownSplitSiblings`

After position resolution, `fromLeft`/`toLeft` may point to an element
that has been split by a concurrent operation. The split product
(right sibling) is unknown to the editor — its `CreatedAt` is not in
the editor's version vector.

This phase walks the `InsNextID` chain from the resolved left node,
advancing past element split siblings that are unknown to the editor.
Skip when `leftNode == parent` (leftmost position — no left sibling
to advance from).

This prevents multi-level split mispositioning and side-by-side
insert/delete errors. Without it, operations target the original
element instead of landing after all its split products.

The same advance logic is applied in `Style` and `RemoveStyle` entry
points (see Phase 9).

**Options** (used in Phase 7's recursive split loop):
- `relaxParentCheck`: skip parent equality check at ancestor
  iterations, where a concurrent ancestor split may move siblings to
  different parents.
- `skipActorID`: stop at siblings created by the current change's
  actor, since same-actor siblings are own split products, not
  concurrent ones.

## Phase 3: Range Narrowing

**Location**: `Edit`, after position resolution

When `fromParent` and `toParent` are different nodes — the from
position is in the original parent while the to position landed in a
split sibling — the `collectBetween` traversal range crosses parent
boundaries and may include content from concurrent split products that
wasn't in the editor's original range.

Follow `fromLeft`'s `InsNextID` chain to find a split sibling whose
parent is `toParent`, and use that sibling as the `collectBetween`
from-position.

Only the `collectBetween` range is narrowed. The original
`fromParent`/`fromLeft` are preserved for merge, split, and insert
steps so that content is inserted at the editor's intended position.
On the other replica, §7.3 (Boundary Insert Migration) handles the
symmetric placement.

VV-independent: relies only on `InsNextID` chain and parent pointer
comparison, preserving clone/root consistency.

## Phase 4: Range Collection

**Function**: `collectBetween`

Walks the resolved range to build lists of nodes to delete and merge.
Three concurrent issues require special handling.

### §4.1 Split Sibling Cascade

When collecting an element for deletion, follow its `InsNextID` chain
to include split siblings unknown to the editor's version vector.
These siblings are part of the same logical element and must be
deleted together. Element-only: text splits use deterministic
offset-based IDs that `findFloorNode` already resolves.

### §4.2 Moved Children Guard

Exclude children whose parent is in `toBeMergedNodes` from the
parent-cascade delete. These children are being moved by the merge
operation, not deleted. Without this guard, merge-target children
would be incorrectly tombstoned.

### §4.3 Skip Concurrent Element Merge

When a delete range crosses into an element whose `CreatedAt` is not
in the editor's version vector, skip merge detection for that element.
The boundary crossing is an artifact of a concurrent split — the
editor's original range did not include this element, so detecting it
as a merge target would be incorrect.

## Phase 5: Delete

Tombstone the nodes collected in Phase 4. No concurrent merge/split-
specific logic in this phase — it applies the deletion marks produced
by `collectBetween`.

## Phase 6: Merge

**Functions**: `mergeNodes`, `propagateMergeDeletes`

### §6.1 Child Migration and Forwarding Pointers

During merge, children are moved from the merge-source to the
merge-target (`fromParent`). Each moved child receives:

- **`MergedFrom`** (persisted): the source parent's ID. Used to
  identify which children were moved by this merge.
- **`MergedAt`** (persisted): the immutable merge ticket. Needed
  because `source.removedAt` can be overwritten by LWW, but the
  merge timestamp must be stable for version-vector checks.
- **`mergedInto`** (runtime cache): set on the source node, pointing
  to the target. Rebuilt from `MergedFrom` on snapshot load.

### §6.2 Delete Propagation to Merge-Moved Children

When a merge-source is deleted (not merged), its former children —
now in the merge target — must also be tombstoned. Children are
identified via `child.MergedFrom == source.id`.

Skip propagation when `mergedInto == fromParent`: this indicates a
concurrent merge (both sides merged into each other), not a delete.

## Phase 7: Split

Three levels: `SplitElement` (child partitioning), `Split`
(node-level linking), and the recursive split loop (ancestor
traversal).

### §7.1 SplitElement: Merge-Moved Children

During child partitioning into left/right, children that were moved
by a concurrent merge must stay in the original (left) node. A child
is kept in the left partition when:

1. Its `MergedFrom` identifies a source that was a child of the
   split node, AND
2. The editor's version vector does not cover the child's `MergedAt`.

`MergedAt` (not `source.removedAt`) is used for the VV check because
`removedAt` can be overwritten by LWW.

### §7.2 SplitElement: Attribute Copy

Deep-copy the original node's attributes (`Attrs`) to the split
(right) node. Without this, the right node is created with nil
attributes, losing any styling applied before the split.

### §7.3 SplitElement: Boundary Insert Migration

After partitioning children into left/right, any consecutive run of
concurrent inserts (children whose `CreatedAt` is unknown to the
splitter's version vector) at the start of the right partition is
moved to the left partition.

A concurrent insert placed between the split boundary and the next
original child was positioned relative to the pre-split child order.
Its CRDT position (after a left-partition child) means it belongs in
the left partition.

Element split siblings (nodes with `InsPrevID`, non-text) are skipped
during the boundary scan — without this, a split sibling at the start
of the right partition would prematurely stop the scan. Text split
siblings retain their boundary role because their deterministic IDs
make them safe markers.

### §7.4 Split: Empty Sibling Re-Parenting

After creating a split sibling and linking it into the `InsNextID`
chain, check whether the existing `InsNext` sibling is in a different
parent (due to a prior parent-level split by another operation). If
so, and the new split sibling has no children (empty), detach it from
the original parent and insert it before `InsNext` in `InsNext`'s
parent.

This fixes divergences where concurrent `splitLevel >= 2` operations
produce empty replay split siblings that land in different parents
depending on application order. The empty-children guard prevents
false activation on splits at different positions, where the new
sibling legitimately carries children.

VV-independent (purely structural), preserving clone/root consistency.

### §7.5 Recursive Split Loop: Per-Iteration Advance

The recursive `split` function executes
`advancePastUnknownSplitSiblings` at each iteration, not just before
loop entry. Element splits produce non-deterministic IDs, so the
`InsNextID` chain is the only discovery path for concurrent element
split siblings at each ancestor level.

The parent equality check is relaxed (`relaxParentCheck`) at ancestor
iterations. A concurrent ancestor split may move siblings to different
parents, causing the strict check to break the chain prematurely.

If the advance moves `left` to a node under a different parent,
`parent` is updated to match so `FindOffset` and `Split` operate on
the correct subtree.

### §7.6 Recursive Split Loop: Removed-Inclusive Offset

`FindOffset` in the split loop passes `includeRemoved: true` to match
`SplitElement`'s `Children(true)` partition. Without this, tombstoned
children before the split point cause the visible-only offset to be
smaller than the all-children offset, misplacing the partition
boundary.

### §7.7 Recursive Split Loop: skipActorID

`advancePastUnknownSplitSiblings` accepts a `skipActorID` option that
stops advancement at siblings created by the current change's actor.

In the split loop, `issueTimeTicket` creates siblings with lamport
timestamps beyond the change's version vector, making them appear
"unknown". Without `skipActorID`, the function advances past the
current operation's own split products, diverging from the clone path
(`VV=nil`) where no advancement occurs.

Since a single actor's changes are always sequential, an "unknown"
sibling from the same actor is always our own creation, not a
concurrent one. This preserves clone/root consistency while still
advancing past genuine concurrent siblings from other actors.

## Phase 8: Insert

Insert content at the editor's intended position with parent-deletion
guard and merge redirect. No concurrent merge/split-specific logic in
this phase — the preceding phases ensure the insertion point is correct.

## Phase 9: Style Operations

**Functions**: `Style`, `RemoveStyle`

Style operations use the same split sibling advance as Edit (Phase 2)
but require two additional mechanisms for concurrent split handling.

### §9.1 End-Token Split Sibling Guard

When processing an End token, skip styling if the node has an
`InsNextID` split sibling not in the editor's version vector. The End
token is in the range only because a concurrent split extended the
traversal past the original element boundary.

Helper: `hasUnknownSplitSibling(node, vv)` — follows `InsNextID`,
checks the sibling is an element whose `CreatedAt` is not covered
by VV. Omits parent-equality check (same rationale as §7.5).

**Why End-token guard over range clamping**: Clamping works for
text-level ranges but fails for element-level ranges where the
editor selected children that moved to the split sibling. The guard
handles both uniformly:

- **Text-level** (range within element content): P's End token
  skipped → P' Start rejected by `canStyle` → no elements styled.
  Matches the unsplit behavior (no element tokens in range).
- **Element-level** (range across children): parent End token
  skipped → child Start token still styled via `canStyle`. Matches
  the unsplit behavior (child was in the original range).

### §9.2 Style Propagation to Split Siblings

After styling a node via its Start token, follow the `InsNextID`
chain and apply the same style/remove-style to unknown split siblings
(those whose `CreatedAt` is not covered by the editor's VV).

This ensures that a style operation whose range was determined before
a concurrent split also covers the right part. Without this
propagation, the client that splits first misses the style on the
right node, while the client that styles first copies it via the
attribute deep-copy in §7.2 — causing divergence.

Helper: `ticketKnown(vv, ticket)` — reused for the unknown-sibling
check.

## Key Design Decisions

| Decision | Reason |
|----------|--------|
| Implicit move over explicit `TreeMove` op | Same fixes needed; Move adds protocol complexity |
| Persist `MergedFrom` + `MergedAt` in proto | Durable merge witness; `MergedAt` is immutable (unlike `removedAt` which LWW overwrites) |
| `mergedInto` as runtime cache only | Fast nil-check on hot path; rebuilt from `MergedFrom` on load |
| Derive moved-children on demand | `target.Children(true) | where MergedFrom == source.id`; call sites already have the target |
| Position-level advance for splits (§2) | `collectBetween`-level fix can't distinguish contained vs side-by-side delete |
| End-token guard over range clamping (§9.1) | Clamping fails for element-level ranges; guard handles both text and element uniformly |
| Relaxed parent check at ancestor splits (§7.5) | `InsNextID` is only set by `SplitElement`, so its existence is sufficient evidence |
| `includeRemoved` in split loop offset (§7.6) | Must match `SplitElement`'s `Children(true)` partition to avoid boundary mismatch |
| `skipActorID` in split loop advancement (§7.7) | Same-actor siblings are own split products, not concurrent; advancing past them diverges root from clone |
| Boundary insert migration in SplitElement (§7.3) | CRDT position of concurrent insert is relative to pre-split child order; physical position after split is misleading |
| Empty sibling re-parenting in Split (§7.4) | When a concurrent parent split already separated siblings into different parents, a replay split's empty product must follow the existing chain to be deterministic; VV-independent to preserve clone/root consistency |
| Narrow collectBetween only, preserve insert point (§3) | Adjusting fromLeft/fromParent for both delete and insert changes the insertion position, diverging from the other replica where §7.3's boundary migration handles placement |

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
| SplitSplit | 321 | 0 |
| SplitEdit | 145 | 0 |
| **Total** | **1597** | **0** |

### Clone/root consistency

`syncClientsThenAssertEqual` and `syncClientsThenCheckEqual` now verify
that each document's clone tree XML matches its root tree XML after
sync. This catches bugs where `change.Execute(root, versionVector)`
produces a different tree structure from `change.Execute(clone, nil)`.

## Appendix: Fix Number Cross-Reference

For traceability from git history (commit messages reference Fix N).

| Fix | Section | Short Description |
|-----|---------|-------------------|
| Fix 1 | §4.1 | Split sibling cascade delete in collectBetween |
| Fix 2 | §4.2 | Moved children guard in collectBetween |
| Fix 3 | §1.1 | Merge-tombstone insert redirect |
| Fix 4 | §1.1 + §6.1 | mergedInto forwarding pointer + merge data model |
| Fix 5 | §6.2 | Delete propagation to merge-moved children |
| Fix 6 | §1.2 | Inverted range no-op |
| Fix 7 | §2 | Split sibling advance |
| Fix 8 | §7.1 | SplitElement skips merge-moved children |
| Fix 9 | §4.3 | Skip concurrent element merge |
| Fix 10 | N/A | JS-only: splitElement uses raw children |
| Fix 11 | §9.1 | End-token split sibling guard |
| Fix 12 | §7.2 + §9.2 | Attribute copy + style propagation |
| Fix 13 | §7.5 | Per-iteration advance in recursive split loop |
| Fix 14 | §7.6 | Removed-inclusive offset in split loop |
| Fix 15 | §7.7 | skipActorID in split loop |
| Fix 16 | §7.3 | Boundary insert migration in SplitElement |
| Fix 17 | §7.4 | Empty sibling re-parenting in Split |
| Fix 18 | §3 | Cross-parent range narrowing |
