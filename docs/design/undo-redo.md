---
title: undo-redo
target-version: 0.7.5
---

# Undo/Redo (History)

## Summary

Undo/Redo allows users to reverse and replay their local edits in a collaborative
environment. Each client maintains its own undo/redo stacks, so undoing only
reverses the current user's operations while preserving other users' concurrent
edits. This document describes the architecture, reverse operation generation for
each data type, and the position reconciliation mechanism that keeps undo
operations valid when remote edits arrive.

### Goals

- Provide per-client undo/redo for all mutable data types: Object, Array, Text,
  Tree, Counter.
- Ensure convergence: after undo/redo and sync, all clients reach the same
  document state.
- Handle concurrent edits: remote operations must not break pending undo/redo
  operations.

### Non-Goals

- Server-side undo/redo or global history browsing.
- Undo/redo for `Tree.Edit` with `splitLevel >= 2` at the CRDT layer (deferred
  until L2 forward convergence is fixed; `splitLevel=1` is supported).
- Overlapping range reconciliation for Tree (Cases 3–6, deferred to Phase 2).
- Undo/redo for `TreeStyleOperation` with multi-client reconciliation (single-client undo/redo is now supported via PR #1221).

## Proposal Details

### Architecture Overview

```
┌──────────────────────────────────────────────────┐
│ Document                                         │
│  ┌──────────────────────────┐                    │
│  │ History                  │                    │
│  │  ┌────────────────────┐  │                    │
│  │  │ undoStack (max 50) │  │  pushUndo ◄── execute(op) produces reverseOp
│  │  └────────────────────┘  │                    │
│  │  ┌────────────────────┐  │                    │
│  │  │ redoStack (max 50) │  │  pushRedo ◄── undo() produces reverseOp
│  │  └────────────────────┘  │                    │
│  └──────────────────────────┘                    │
│                                                  │
│  document.update(fn)  ── local edit ──►  reverseOps ──► undoStack
│  document.history.undo() ──────────────► reverseOps ──► redoStack
│  document.history.redo() ──────────────► reverseOps ──► undoStack
│                                                  │
│  Remote change arrives ──► reconcile pending undo/redo positions
└──────────────────────────────────────────────────┘
```

Each entry in the undo/redo stacks is an `Array<HistoryOperation>`, which may
contain CRDT operations and/or presence changes. A single `document.update()`
call produces one undo entry (potentially with multiple reverse operations).

The redo stack is cleared whenever a new local edit is made, following standard
undo/redo semantics.

### Reverse Operation Generation

When an operation executes, it produces a `reverseOp` — the operation that, when
executed, undoes the effect of the original. The following table summarizes the
mapping, and each subsection below explains the details.

| Operation | Reverse Operation |
|---|---|
| `SetOperation` (Object.set) | `RemoveOperation` or `SetOperation` |
| `RemoveOperation` (Object/Array.remove) | `AddOperation` or `SetOperation` |
| `AddOperation` (Array.push) | `RemoveOperation` |
| `MoveOperation` (Array.move) | `MoveOperation` |
| `ArraySetOperation` (Array.set) | `ArraySetOperation` |
| `IncreaseOperation` (Counter) | `IncreaseOperation` |
| `EditOperation` (Text.edit) | `EditOperation` |
| `StyleOperation` (Text.style) | `StyleOperation` |
| `TreeEditOperation` (Tree.edit, splitLevel=0) | `TreeEditOperation` |
| `TreeEditOperation` (Tree.edit, splitLevel=1) | `TreeEditOperation` (boundary deletion) |
| `TreeStyleOperation` (Tree.style) | `TreeStyleOperation` |

#### Object.set → SetOperation

`SetOperation` sets a key-value pair on a `CRDTObject`. Before setting, it
captures the previous value at that key.

```
Forward:  obj.set("name", "Bob")          // previous value was "Alice"
Reverse:  SetOperation("name", "Alice")   // restore previous value
```

- If the key **existed** and was not tombstoned: reverse is a `SetOperation` with
  a deep copy of the previous value.
- If the key **did not exist** (or was already removed): reverse is a
  `RemoveOperation` targeting the newly set element's `createdAt`.

#### Object/Array.remove → RemoveOperation

`RemoveOperation` deletes an element from a `CRDTObject` or `CRDTArray`. The
reverse depends on the container type.

**Array case:**
```
Forward:  arr.remove(2)                   // removes element at index 2
Reverse:  AddOperation(prevCreatedAt, deepcopy(removedValue))
```
The reverse is an `AddOperation` that re-inserts the removed element. It captures
`prevCreatedAt` — the identity of the element that was immediately before the
removed element — to restore the correct position.

**Object case:**
```
Forward:  obj.remove("name")              // removes key "name" with value "Alice"
Reverse:  SetOperation("name", "Alice")   // restore key-value pair
```
The reverse is a `SetOperation` that restores the key with a deep copy of the
removed value. The key is retrieved via `subPathOf(createdAt)`.

#### Array.push → AddOperation

`AddOperation` inserts an element into a `CRDTArray` after a given
`prevCreatedAt` position.

```
Forward:  arr.push("item")               // inserts with new createdAt
Reverse:  RemoveOperation(createdAt)     // removes the inserted element
```

The reverse is always a `RemoveOperation` targeting the newly added element's
`createdAt`. The `executedAt` of the reverse op is left unset and assigned at
undo execution time.

#### Array.move → MoveOperation

`MoveOperation` moves an element within a `CRDTArray` from its current position
to after `prevCreatedAt`.

```
Forward:  arr.move(3, 0)                 // move element at index 3 to index 0
Reverse:  MoveOperation(originalPrevCreatedAt, createdAt)
```

The reverse is a `MoveOperation` that moves the element back. It captures
`originalPrevCreatedAt` by calling `array.getPrevCreatedAt(createdAt)` **before**
the forward move executes, preserving the element's original position.

#### Array.set → ArraySetOperation

`ArraySetOperation` replaces an element at a specific index in a `CRDTArray`.
Internally it inserts the new value and deletes the old one.

```
Forward:  arr.set(1, "new")              // replaces "old" at index 1
Reverse:  ArraySetOperation(newCreatedAt, deepcopy(oldValue))
```

The reverse is an `ArraySetOperation` that targets the forward op's newly
inserted element (`newCreatedAt`) and restores the previous value (deep-copied).
This effectively swaps the new value back to the old one.

**Known issue — Set after Move**: `RGATreeList.set` is implemented as
`insertAfter(createdAt, newValue) + delete(createdAt)`. The `insertAfter` uses
`nodeMapByCreatedAt[createdAt]` to find the insertion anchor. After a move, this
resolves to the element's **original dead position** — not its current position.
This is intentional for concurrent convergence (Move→Set and Set→Move both
produce the same result), but it means undo restores the value at the dead
position instead of the moved position. See the analysis section
"Array Set + Move Undo" below for details and proposed fix.

#### Counter.increase → IncreaseOperation

`IncreaseOperation` adds a numeric delta to a `CRDTCounter`.

```
Forward:  counter.increase(5)
Reverse:  IncreaseOperation(-5)
```

The reverse is an `IncreaseOperation` with the **negated** value. It handles both
`Long` (64-bit integer) and regular `number` types by multiplying by -1.

#### Text.style → StyleOperation

`StyleOperation` modifies text attributes (bold, italic, etc.) over a range
`[from, to)` in a `CRDTText`. It supports both setting and removing attributes.

```
Forward:  text.style(0, 5, { bold: "true" })    // was { bold: "false" }
Reverse:  StyleOperation(0, 5, { bold: "false" }) // restore previous
```

The reverse captures previous attribute values during execution and swaps them:
- Attributes that were **set** → reverse **restores** their previous values.
- Attributes that were **removed** → reverse **re-adds** them with saved values.
- When both set and remove happened: reverse combines both restorations into a
  single `StyleOperation`.

The reverse op reuses the same `[fromPos, toPos]` range since attribute changes
don't shift positions.

### Text.Edit Undo/Redo

Text uses RGA (Replicated Growable Array) with split nodes. The key insight is
that positions in the RGA are **identity-based** (`RGATreeSplitPos`), not
index-based. This means positions remain stable under concurrent edits.

#### Reverse Operation

When `Text.edit([from, to], content)` executes:
1. The edit removes text in `[from, to)` and inserts `content` at `from`.
2. The reverse operation stores:
   - `reverseFrom`: the `RGATreeSplitPos` normalized at `from` (via
     `text.normalizePos()`, which walks the RGA chain)
   - `reverseTo`: `RGATreeSplitPos.of(from.id, from.offset + content.length)`
     (identity-based offset relative to the same node)
   - `reverseContent`: the removed text values joined together
   - `reverseAttrs`: if exactly one character was removed, its attributes are
     preserved for restoration
   - `isUndoOp: true`: marks this as an undo operation for reconciliation

```
Insert:   edit([5, 5], "Hello")     →  reverse: edit([5, 10], "")
Delete:   edit([5, 10], "")         →  reverse: edit([5, 5], "Hello")
Replace:  edit([5, 10], "World")    →  reverse: edit([5, 10], "Hello")
```

When the undo operation executes, it calls `text.refinePos()` on both positions
to skip over any tombstoned nodes that may have appeared due to concurrent
edits.

#### Position Reconciliation

When a remote edit arrives, pending undo/redo operations in the History stacks
must be adjusted. Text uses `normalizePos()` which walks the physical RGA chain
to compute visible integer indices. Since the RGA chain is identical on all
clients (CRDT guarantee), this produces **symmetric values** — the same indices
on every client.

The reconciliation uses six overlap cases:

```
Case 1 (left):     [--remote--]  [--undo--]   → shift undo left
Case 2 (right):    [--undo--]  [--remote--]    → no change
Case 3 (contains): [-------remote-------]      → collapse to point
                        [--undo--]
Case 4 (contained):[--remote--]                → shrink undo range
                   [---------undo---------]
Case 5 (overlap_start): [---remote---]         → adjust start
                             [---undo---]
Case 6 (overlap_end):       [---remote---]     → adjust end
                        [---undo---]
```

### Tree.Edit Undo/Redo

Tree uses a CRDT tree structure (`CRDTTree`) with identity-based positions
(`CRDTTreePos = {parentID, leftSiblingID}`). Unlike Text, Tree positions involve
a hierarchical structure with element nodes and text nodes.

#### Reverse Operation

When `Tree.edit([from, to], contents, splitLevel)` executes:
1. `CRDTTree.edit()` returns the removed nodes and the pre-edit `fromIdx`.
2. The reverse operation:
   - **For deletion (contents=undefined)**: deep-copies the removed nodes,
     clears their tombstone markers, and stores them as `reverseContents`.
   - **For insertion**: stores no content (the reverse is a deletion).
   - **For replacement**: combines both — deep-copies removed nodes and marks the
     inserted content range for deletion.
3. The reverse uses `tree.findPos(preEditFromIdx)` on the post-edit tree to get
   the `CRDTTreePos` for the insertion point.

```
Forward:  edit([from, to], content)
Reverse:  edit([from, from + insertedSize], removedNodes)
```

#### Position Representation

The reverse operation stores both representations:
- **CRDTTreePos**: identity-based positions for initial use
- **Integer indices** (`fromIdx`, `toIdx`): for reconciliation adjustment

At undo execution time, the integer indices are converted to `CRDTTreePos` via
`tree.findPos()`, ensuring they reflect the current tree state.

#### Concurrent Parent Deletion Guard

In concurrent editing, a parent element may be deleted by another client while a
child edit is being applied. When this happens:
- Inserted content is immediately tombstoned (no visible effect)
- `preEditFromIdx + insertedContentSize` may exceed the post-edit tree size

The implementation guards against this: if the maximum needed index exceeds
`tree.getSize()`, the reverse operation is skipped (the edit was effectively a
no-op on visible content).

#### Position Reconciliation

Tree reconciliation follows the same six-case overlap logic as Text, but uses
visible integer indices instead of RGA chain positions. Each `TreeEditOperation`
exposes:
- `normalizePos()`: returns `[fromIdx, toIdx]` for reconciliation
- `reconcileOperation(remoteFrom, remoteTo, contentLen)`: adjusts stored indices
- `getContentSize()`: returns total inserted content size (sum of `paddedSize()`)

When a remote change is applied, `Document` scans the History stacks and calls
`reconcileTreeEdit()` for each pending `TreeEditOperation` targeting the same
tree.

#### Split Undo/Redo (splitLevel=1)

A split creates new element boundaries without removing any nodes:

```
splitLevel=1: <p>ab|cd</p>  →  <p>ab</p><p>cd</p>   (2 boundary tokens)
```

The reverse is a **boundary deletion** — a `splitLevel=0` edit that removes the
boundary tokens, merging the split elements back:

```
reverse: edit(fromIdx, fromIdx + 2, undefined, 0)   // delete 2 boundary tokens
```

This approach reuses all existing infrastructure:
- The reverse op is a standard `TreeEditOperation` with `isUndoOp=true` and
  `splitLevel=0`.
- Reconciliation uses the existing 6-case overlap logic unchanged.
- Redo works automatically: the boundary deletion produces its own reverse via
  the existing `toReverseOperation` path.

The `toSplitReverseOperation` method is guarded for pure L1 splits only
(`splitLevel === 1`, no contents, no removed nodes). This prevents generating
incorrect reverse ops for unsupported split shapes (L2+, split+delete combos).

The undo/redo cycle:

```
split(L1)
  → undo: boundary delete (splitLevel=0, removes 2 tokens)
    → redo: re-insert boundary nodes (splitLevel=0, deep-copied nodes)
      → undo again: boundary delete (same as first undo)
```

**Implementation note:** `splitElement` recomputes `visibleSize` from children's
`paddedSize()`. Since `IndexTreeNode.paddedSize()` does not return 0 for removed
nodes, the recomputation must explicitly skip removed children to avoid inflating
the parent's size when tombstoned text nodes are present in the left partition.

#### Phase 2: Overlapping Range Reconciliation

The integer-index approach for Tree is **asymmetric** across clients for
overlapping ranges (Cases 3–6). Unlike Text, where `normalizePos()` walks the
physical RGA chain (identical on all clients), Tree's visible indices depend on
local tombstone state, which differs between clients after concurrent deletions.

This means overlapping reconciliation cases may produce divergent results. These
cases are deferred to Phase 2, which will require either:
- A tree-native `normalizePos()` that walks the CRDT node chain (analogous to
  Text's RGA chain walking), or
- A different position representation that is symmetric across clients.

Non-overlapping cases (Case 1: left, Case 2: right) work correctly because they
only involve index shifting, not range intersection.

### Undo/Redo Flow

#### Local Edit → Undo Entry

```
1. document.update(fn)
2. fn modifies Clone via proxy
3. Change created with operations
4. Change.execute() on Root:
   - Each operation produces a reverseOp
   - reverseOps collected in array
5. Presence reverse captured
6. [reverseOps] pushed to undoStack
7. redoStack cleared
```

#### Undo

```
1. document.history.undo()
2. Pop [reverseOps] from undoStack
3. Create new Change with reverseOps
4. Execute on Clone, then on Root:
   - Each reverseOp produces a re-reverseOp
5. [re-reverseOps] pushed to redoStack
6. Change added to LocalChanges for sync
```

#### Remote Change → Reconciliation

```
1. Remote change arrives
2. Change.execute() on Root
3. For each operation in the change:
   a. If EditOperation (Text):
      - Compute [from, to] via normalizePos()
      - Call history.reconcileTextEdit(parentCreatedAt, from, to, contentLen)
      - Scan undo/redo stacks, adjust matching EditOperations
   b. If TreeEditOperation (Tree):
      - Compute [from, to] via normalizePos()
      - Call history.reconcileTreeEdit(parentCreatedAt, from, to, contentSize)
      - Scan undo/redo stacks, adjust matching TreeEditOperations
   c. If structural operation (Add/Remove/Set/Move):
      - Call history.reconcileCreatedAt(prevCreatedAt, currCreatedAt)
      - Update identity references in undo/redo stacks
```

### Risks and Mitigation

**Risk: GC removes nodes needed for undo.**
Undo operations hold deep copies of removed nodes (for Text content and Tree
nodes). These copies are independent of the CRDT and survive garbage collection.
However, if GC runs and a user then tries to undo, the CRDT positions referenced
by the undo operation might point to collected nodes. Mitigation: undo operations
use `findPos()` (index-based lookup) at execution time rather than relying on
stale CRDT positions.

**Risk: Asymmetric reconciliation for overlapping Tree edits.**
As described in Phase 2, overlapping range reconciliation for Tree uses
asymmetric integer indices. Mitigation: Cases 3–6 are skipped in Phase 1. The
non-overlapping cases (1, 2) and the adjacent case (7) cover the majority of
real-world undo/redo scenarios.

**Risk: Stack overflow from deep undo chains.**
The undo and redo stacks are capped at 50 entries (`MaxUndoRedoStackDepth`).
Oldest entries are evicted when the cap is reached.

### Current Status (as of 2026-04-20)

#### Completed

| Area | Status | Notes |
|------|--------|-------|
| Text single-client | ✅ | insert, delete, replace, style |
| Text multi-client reconciliation | ✅ | Convergence passes for all 7 cases (but see content correctness below) |
| Array undo | ✅ | add, remove, move, set |
| Tree single-client (splitLevel=0) | ✅ | All op types + chained ops |
| Tree split L1 undo/redo | ✅ | Boundary-deletion reverse ops (PR #1219) |
| Tree multi-client (non-overlapping) | ✅ | Cases 1, 2, 7 (left/right/adjacent) |
| TreeStyleOperation single-client | ✅ | setStyle, removeStyle undo/redo (PR #1221) |
| TreeStyleOperation multi-client | ✅ | style×style (18 tests), style×edit/split (24 tests) (PR #1221) |
| Tree redo divergence | ✅ | Fixed via CRDTTreePos-based undo execution + reconciliation disabled (branch `tree-undo-pos-normalization`) |

#### Remaining Work

| Priority | Item | Details |
|----------|------|---------|
| HIGH | Overlapping undo content duplication | Both Text and Tree produce duplicate content when overlapping deletes are both undone. Text converges but content is wrong; Tree diverges. See analysis below. |
| HIGH | Tree reconciliation Cases 3-6 | Blocked by overlapping undo content duplication — same root cause. 4 tests skipped. |
| HIGH | Array Set + Move undo | Set after Move restores value at dead position. Requires proto change. See analysis below. 4 tests skipped in JS SDK. |
| MED | GC vs undo interaction | Issue #664. GC can purge elements still referenced by undo/redo stack. |
| LOW | splitLevel≥2 undo/redo | Blocked by L2 forward convergence (68/320 concurrent tests fail). |
| LOW | History reconciliation performance | O(n) stack scan → indexed lookup. |

### Analysis: Overlapping Undo Content Duplication

When two clients delete overlapping ranges and both undo, the shared
content is re-inserted twice because each client's undo deep-copies
removed content and re-inserts as new CRDT nodes.

#### Concrete Example

```
Text: "0123456789"
d1: delete [4,6) = "45"     →  undo: re-insert "45"
d2: delete [2,8) = "234567" →  undo: re-insert "234567"

Both undo → sync:
  d1 re-inserts "45" (new RGA nodes)
  d2 re-inserts "234567" (new RGA nodes, includes "45")
  Result: "012345674589"  (expected "0123456789")
```

This affects **both Text and Tree**:
- Text: convergence passes (both clients get same wrong content) —
  correctness tests added in PR #1222, marked `skip`
- Tree: convergence fails (asymmetric integer indices cause different
  wrong content on each client) — Cases 3-6 skipped

#### Root Cause

The undo mechanism creates **new CRDT nodes** (deep-copy + re-insert)
instead of restoring original tombstoned nodes. When undo ranges overlap,
the overlapping content gets duplicated because each client independently
creates its own copies.

Text's reconciliation detects the overlap via the 6-case integer logic
and adjusts positions, but this only ensures convergence — not content
correctness. Tree's reconciliation fails entirely because `CRDTTreePos`
identifies slots (parent + leftSibling), not specific nodes, making
integer-based overlap detection asymmetric.

#### Approaches Explored

**Approach A: CRDTTreePos-based execution (Tree only)**

Use `CRDTTreePos` as source of truth for Tree undo ops instead of integer
indices. Disable integer-based reconciliation. Implemented in branch
`tree-undo-pos-normalization`.

Result: Fixes Tree redo divergence (1 test). Does NOT fix Cases 3-6
(overlapping content duplication is a separate problem).

Key finding: `CRDTTreePos(parentID, leftSiblingID)` identifies a **slot**
between nodes, not a specific node. Concurrent inserts between the
leftSibling and target change what node occupies the slot, making
index-based overlap detection fundamentally incorrect for Tree.

Also found: `refineTreePos` must preserve original `CRDTTreePos` when the
leftSibling is tombstoned — the `toTreePos + fromTreePos` roundtrip
corrupts the offset for removed siblings.

**Approach B: Selective un-tombstone (Resurrect)**

Instead of deep-copy re-insert, undo clears `removedAt` on nodes whose
tombstone timestamp matches the forward delete's `editedAt`. Each node is
resurrected at most once (LWW winner's undo restores shared nodes, loser
skips).

Result — single-client: **fully works** (59/59 tests including all chain
combinations, replace undo/redo, triple same replace).

Result — multi-client: **fails due to GC**. Sync triggers
`garbageCollect(versionVector)` which physically purges tombstoned nodes
before the remote undo arrives. Resurrected nodes no longer exist.

Implementation required 3 new fields on `EditOperation` (`resurrectAt`,
`retombstoneNodeIDs`, `resurrectDeletedAt`), 3 new methods on
`RGATreeSplit` (`resurrect`, `retombstone`, `retombstoneByCreatedAt`),
plus `CRDTRoot.unregisterGCPair`, protobuf schema changes, and Go server
converter updates.

**Why Resurrect fails for multi-client:**

The GC contract says: if all replicas have seen a deletion (reflected in
`minSyncedVersionVector`), the tombstoned node can be purged. But "all
replicas have seen the deletion" does NOT mean "no replica will undo it."
A client may undo after GC has already purged the target node.

Issue #664 proposes protecting locally-referenced undo elements from GC,
but this only covers the LOCAL undo stack. Client A cannot protect nodes
referenced by Client B's undo stack — Client A doesn't know Client B's
undo intent.

The existing content-based re-insert avoids this problem entirely because
it creates new nodes independent of GC state.

#### Remaining Options

| Approach | Pros | Cons |
|----------|------|------|
| Node-ID overlap detection | Extend undo op with removed node IDs; reconciliation compares node sets instead of index ranges. Content re-insert preserved (GC-safe). | Significant change to operation model and reconciliation logic. |
| Reconciliation content trimming | Case 3: when undo fully contained by remote delete, clear content. | Only handles full containment; partial overlap (Cases 5-6) can't predict future undo intent. |
| Accept as known limitation | Document the issue; focus on other improvements. | Overlapping undo is uncommon in practice; users can work around. |

### Analysis: Tree Redo Divergence (resolved)

This divergence was fixed by switching Tree undo ops to CRDTTreePos-based
execution and disabling integer reconciliation. See branch
`tree-undo-pos-normalization`.

Root cause: Tree used integer indices for redo positions, which are
asymmetric across clients. `CRDTTreePos` directly identifies the target
position, and `refineTreePos` at execution time handles tombstoned
siblings by preserving the original position.

```
d1 redo: insert "X" via CRDTTreePos → same position on all clients
d2 redo: delete "." via CRDTTreePos → same node on all clients
→ Convergence restored
```

### Analysis: Array Set + Move Undo

When an element is moved and then set (value replaced), undoing the set
restores the value at the element's original dead position instead of
its moved position.

#### Concrete Example

```
Initial: [a, b, c, d, e]
Move a after c: [b, c, a, d, e]
Remove b: [c, a, d, e]            ← S2
Set index 1 to 's': [c, s, d, e]  ← S3

Undo set → expected S2: [c, a, d, e]
Undo set → actual:      [a, c, d, e]   ← 'a' at front, not after 'c'
```

4 tests skipped in JS SDK (`move-*-set` combinations in
`history_array_test.ts`).

#### Root Cause

`RGATreeList.set(createdAt, newValue, executedAt)` = `insertAfter(createdAt,
newValue) + delete(createdAt)`. The `insertAfter` dual-lookup first checks
`nodeMapByCreatedAt[createdAt]`, which finds the **original dead position**
(before the move), not the element's current position.

This is intentional for concurrent convergence — see
`array-move-convergence.md`:

> "Use the element's original position (via nodeMapByCreatedAt[createdAt])
> so that Set always inserts at the position where the element was when
> the Set operation was created, regardless of concurrent moves."

The trade-off: the reverse `ArraySetOperation` also inserts at the dead
position, because it uses the same `insertAfter(createdAt, ...)` path.

#### Approaches Explored

**Approach A: `prevPosCreatedAt` field on ArraySetOperation (local only)**

Add a `prevPosCreatedAt` field to `ArraySetOperation`. The reverse op
captures the element's current position before the set. When the reverse
executes, it uses `prevPosCreatedAt` as the insertion anchor instead of
`createdAt`.

Result — single-client: **works** (`move-remove-set`, `move-move-set`
pass). The restored value appears at the correct moved position.

Result — multi-client: **fails** (`set-set` diverges). The
`prevPosCreatedAt` is not serialized in protobuf. When the undo's change
is synced to the other client, the reverse `ArraySetOperation` arrives
without `prevPosCreatedAt`, falling back to `createdAt` (dead position).
The two clients insert at different positions → divergence.

**Approach B: `prevPosCreatedAt` in protobuf**

Add `prev_pos_created_at` as an optional field to the `ArraySet` protobuf
message. Forward set leaves it unset (convergence preserved). Reverse set
(undo) populates it so the restored value goes to the correct position.

```protobuf
message Operation {
  message ArraySet {
    ...
    TimeTicket prev_pos_created_at = 5; // position anchor for undo
  }
}
```

Both Go and JS SDKs serialize/deserialize the new field. Old clients
ignore it (optional field, backward compatible).

This is the **recommended approach**. It requires a coordinated proto +
Go + JS change.

**Approach C: Change `set` to use current position**

Make `RGATreeList.set` use `elementMapByCreatedAt[createdAt].positionNode`
for insertion instead of `nodeMapByCreatedAt[createdAt]`.

**Rejected**: breaks concurrent convergence. Move→Set and Set→Move
produce different positions because the element's "current" position
depends on whether the Move has been applied yet.

**Approach D: Composite reverse (ArraySetOperation + MoveOperation)**

Return two reverse operations: a `ArraySetOperation` (restore value) +
`MoveOperation` (correct position). Avoids proto changes.

**Rejected**: the undo system runs `reconcileCreatedAt` on
`ArraySetOperation` values, changing the element's `createdAt`. The
paired `MoveOperation` references the old `createdAt`, which becomes
stale. Keeping them synchronized requires complex bookkeeping.
