---
title: array-move-convergence
---

# Array Move Convergence

## Problem

`Array.MoveAfter` does not converge when the same element is concurrently moved multiple times (#1416).

The current implementation handles move with a single `movedFrom` pointer and cascade logic. It has two fundamental flaws:

1. **movedFrom overwrite**: When multiple moves target the same node, `movedFrom` is overwritten non-deterministically depending on application order.
2. **Cascade movedAt overwrite**: The cascade loop (rga_tree_list.go:270-282) changes adjacent nodes' `movedAt` non-deterministically. Which nodes are adjacent depends on application order, so cascade targets and their `movedAt` values differ across replicas.

Extending `movedFrom` to a list does not solve the cascade problem. Counterexample: three concurrent operations where cascade sets `movedAt` differently depending on order, causing a subsequent insert to land at different positions (see #1416 comment).

### Goals

- Guarantee convergence of `Array.MoveAfter` under concurrent operations.
- Pass `TestArrayConcurrencyTable` (49 cases) + `TestComplicateArrayConcurrency` (4 cases) + additional counterexamples.
- Keep the existing Array insert/delete/get API unchanged.
- Keep the protobuf Move operation message unchanged.

### Non-Goals

- Range move (moving multiple elements at once) — Kleppmann's paper leaves this as an open problem.
- Move convergence for other CRDT types (Text, Tree, etc.).
- Performance optimization — correctness first, optimization later.

## Design

Apply Kleppmann's "Moving Elements in List CRDTs" (PaPoC 2020) principle on top of the existing RGA. Core idea: **separate element identity from position, and manage position with an LWW register.**

### Conceptual Model

```
Current:
  RGATreeListNode = { elem(value+position metadata), movedFrom, prev, next }
  Element and position are coupled 1:1. move = release + insertAfter + cascade.

Proposed:
  PositionNode = { prev, next, indexNode, elementEntry }   // physical position slot
  ElementEntry = { elem, positionNode, posMovedAt }         // logical element
  move = create new PositionNode + LWW-update ElementEntry's position.
```

**PositionNode**: A slot in the RGA linked list. When `elementEntry` is nil, it is a dead slot (abandoned by a move). Dead slots have splay tree weight 0.

**ElementEntry**: Stable identity of an element. `positionNode` is the position this element currently occupies. `posMovedAt` is the LWW timestamp of the most recent move.

### MoveAfter Algorithm

```
MoveAfter(prevCreatedAt, createdAt, executedAt):
  1. entry = elementByCreatedAt[createdAt]
  2. if entry.posMovedAt != nil && !executedAt.After(entry.posMovedAt):
       return  // LWW: this move already lost
  3. newPosNode = insertPositionAfter(prevCreatedAt, executedAt)
       // reuse existing insertAfter's forward skip logic (RGA insertion rule)
  4. oldPosNode = entry.positionNode
     oldPosNode.elementEntry = nil   // mark as dead slot
     splayTree.updateWeight(oldPosNode)  // weight becomes 0
  5. newPosNode.elementEntry = entry
     entry.positionNode = newPosNode
     entry.posMovedAt = executedAt
     splayTree.updateWeight(newPosNode)
```

**Convergence proof**: Concurrent moves are resolved deterministically by LWW comparison (step 2). For any element, only the move with the highest timestamp survives as the final position. This holds regardless of application order.

### Code to Remove

| Location | Content | Replaced by |
|----------|---------|-------------|
| `rga_tree_list.go:31` | `movedFrom *RGATreeListNode` | LWW register |
| `rga_tree_list.go:82-89` | `MovedFrom()` / `SetMovedFrom()` | Remove |
| `rga_tree_list.go:270-282` | Cascade loop | Remove — LWW makes it unnecessary |
| `rga_tree_list.go:326-328` | `findNextBeforeExecutedAt` backtracking | Remove |

### Code to Keep

| Location | Content | Reason |
|----------|---------|--------|
| RGA linked list + splay tree | Order management, index lookup | PositionNode reuses this structure |
| `insertAfter` forward skip (lines 330-332) | Skip nodes positioned after executedAt | Part of RGA insertion rule, still needed |
| `InsertAfter` / `Delete` | Insert/delete operations | Work independently of move |
| `operations/move.go` | Move operation struct | Same fields, only Execute internals change |

### Splay Tree Weight

Dead PositionNodes must be excluded from user-visible length:

- Dead slot: weight = 0 (invisible)
- Live slot (elementEntry != nil): weight = elem.Len() (same as current)
- Removed element in slot: weight = 0 (same as current removed logic)

### Protobuf

**Move operation message**: No change. All 4 fields map directly to the new semantics.

```protobuf
message Move {
  TimeTicket parent_created_at = 1;  // Array
  TimeTicket prev_created_at = 2;    // destination (after this)
  TimeTicket created_at = 3;         // stable ID of element to move
  TimeTicket executed_at = 4;        // LWW timestamp
}
```

**Snapshot (RGANode)**: Needs additional field for dead position nodes.

```protobuf
message RGANode {
  JSONElementSimple element = 1;
  // New field:
  TimeTicket pos_moved_at = 2;      // LWW timestamp (null if position was created by insert)
}
```

Dead position nodes are serialized as RGANodes with no element. `pos_moved_at` is set only when an element was moved.

### GC

Dead position nodes follow the existing VersionVector-based GC pattern:
- Once all clients have synced past the move timestamp, the dead node can be purged from the linked list.
- Reuse the existing `gcElementPairMap` registration mechanism.

### Counterexample Verification

Verify with the counterexample from the issue comment:

```
Initial: Head -> A -> B -> C
Op1 @t1: move(A, after C)
Op2 @t2: move(B, after A)   (t1 < t2)
Op3 @t3: insert(X, after B) (t1 < t3 < t2)
```

With the LWW approach, in any application order:
- B's final position is determined solely by Op2 (highest timestamp t2 for B). Op1 moves A, not B — no cascade contamination.
- Op3 inserts X after B's current position. Since B's position is deterministic (always the result of Op2), X always lands in the same place.

Key design decision: **insert after a moved element follows the element to its current position.** `prevCreatedAt` is the element's stable ID, so insertion always occurs after wherever that element currently is.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Dead position node accumulation increases memory | VersionVector-based GC. Loro reports ~50% memory increase as baseline |
| Incompatible with existing snapshots | Migration logic: on load, convert all existing nodes to live PositionNode + ElementEntry pairs |
| Interaction with Set operation (decomposed as InsertAfter + Remove) | Set operates at the element layer, independent of position. Verify with tests |
| Removing `findNextBeforeExecutedAt` backtracking affects insert convergence | Only backtracking is removed. Forward skip is preserved — it's part of the RGA insertion rule |

### Design Decisions

| Decision | Rationale |
|----------|-----------|
| Adopt Kleppmann LWW approach | Formally verified (Isabelle/HOL). Convergence guaranteed by CRDT primitive composition |
| Remove movedFrom + cascade entirely | Patching cannot achieve convergence (counterexample proven). Fundamental structural change needed |
| Separate PositionNode / ElementEntry | Direct implementation of Kleppmann's principle. Loro uses the same pattern (ListItem / Element) |
| Keep existing RGA linked list + splay tree | Insert/delete work correctly. Only move needs redesign |
| Keep Move protobuf message | Fields map directly to new semantics. Avoid unnecessary wire format change |
| Keep forward skip, remove only backtracking | Forward skip is RGA insertion rule. Backtracking is movedFrom-specific |

## Alternatives Considered

| Alternative | Reason for rejection |
|-------------|---------------------|
| Extend movedFrom to a list | Cascade movedAt overwrite problem remains. Convergence not guaranteed — counterexample exists |
| Loro-style full redesign (Fugue + dual B-tree) | Excessive scope. Requires replacing RGA-based insert/delete. 80% slower encode/decode, 50% more memory |
| Kleppmann undo-redo-redo (tree move paper) | Excessive complexity for lists. LWW position register is simpler with the same convergence guarantee |
| Decompose move as delete + reinsert | Concurrent moves duplicate the element. Element identity is lost |
| Fix only the cascade logic | Cascade is inherently order-dependent. Fundamentally non-deterministic |
