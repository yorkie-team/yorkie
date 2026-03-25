---
title: undo-redo
target-version: 0.6.50
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
- Undo/redo for `Tree.Edit` with `splitLevel > 0` at the CRDT layer (deferred to
  Phase 2; note that user-facing `splitByPath`/`mergeByPath` decompose into
  multiple `splitLevel=0` calls, so they are supported).
- Overlapping range reconciliation for Tree (Cases 3–6, deferred to Phase 2).

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
| `TreeEditOperation` (Tree.edit) | `TreeEditOperation` |

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
