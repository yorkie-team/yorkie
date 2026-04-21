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

### Non-Goals

- Range move (moving multiple elements at once) — Kleppmann's paper leaves this as an open problem.
- Move convergence for other CRDT types (Text, Tree, etc.).
- Performance optimization — correctness first, optimization later.

## Design

Apply Kleppmann's "Moving Elements in List CRDTs" (PaPoC 2020) principle on top of the existing RGA. Core idea: **separate element identity from position, and manage position with an LWW register.**

### Conceptual Model

```
Before:
  RGATreeListNode = { elem(value+position metadata), movedFrom, prev, next }
  Element and position are coupled 1:1. move = release + insertAfter + cascade.

After:
  RGATreeListNode = { elementEntry, createdAt, prev, next }   // position slot
  ElementEntry = { elem, positionNode, posMovedAt }            // logical element
  move = create new position node + LWW-update ElementEntry's position.
```

**RGATreeListNode (position node)**: A slot in the RGA linked list. When `elementEntry` is nil, it is a dead slot (abandoned by a move). Dead slots have splay tree weight 0. Each position node has its own `createdAt` — for initial elements this equals the element's createdAt; for move-created positions this equals the move's `executedAt`.

**ElementEntry**: Stable identity of an element. `positionNode` is the position this element currently occupies. `posMovedAt` is the LWW timestamp of the most recent move.

**Two maps in RGATreeList**:
- `nodeMapByCreatedAt`: maps position node createdAt → position node. Dead nodes stay in the map.
- `elementMapByCreatedAt`: maps element createdAt → ElementEntry. Used for element lookups.

### MoveAfter Algorithm

```
MoveAfter(prevCreatedAt, createdAt, executedAt):
  1. prevPosNode = nodeMapByCreatedAt[prevCreatedAt]   // position node lookup
  2. entry = elementMapByCreatedAt[createdAt]           // element lookup
  3. if entry.posMovedAt != nil && !executedAt.After(entry.posMovedAt):
       return  // LWW: this move already lost
  4. newPosNode = insertPositionAfter(prevPosNode, executedAt)
       // forward skip only (RGA insertion rule)
  5. oldPosNode = entry.positionNode
     oldPosNode.elementEntry = nil   // mark as dead slot
     oldPosNode.removedAt = executedAt  // register for GC
  6. newPosNode.elementEntry = entry
     entry.positionNode = newPosNode
     entry.posMovedAt = executedAt
```

**Key**: `prevCreatedAt` is a **position node identity**, not an element identity. When the user says "move B after A", the operation captures A's current position node's createdAt. On remote apply, this finds the exact position node in `nodeMapByCreatedAt` — even if A has since moved and the position is now dead.

### Position Node Identity for Move Destinations

This is the critical design decision for convergence of "move-after-moving-target":

When `move(B, after A)` is created locally, the operation stores `prevCreatedAt = A's current position node createdAt`. On remote apply, `nodeMapByCreatedAt[prevCreatedAt]` finds that exact position node (which may be dead if A was concurrently moved).

```
Example:
  Initial: Head → A_pos(tA) → B_pos(tB) → C_pos(tC)

  Op1 @t4: move(A, after C), prev=tC
  Op2 @t5: move(B, after A), prev=tA  // captures A's position at creation time

  Order 1 (Op1→Op2):
    Op1: A moves to pos_t4 after C. A_pos(tA) becomes dead.
    Op2: prev=tA → finds A_pos(dead). Insert B's new position after A_pos.
    Result: [B, C, A]

  Order 2 (Op2→Op1):
    Op2: prev=tA → finds A_pos(live). Insert B's new position after A_pos.
    Op1: A moves to pos_t4 after C. A_pos becomes dead.
    Result: [B, C, A]  ✓ Converged!
```

Dead position nodes stay in `nodeMapByCreatedAt` — they are never reassigned. This ensures remote operations always find the correct insertion point regardless of application order.

### Move Operation Semantics Change

The Move operation's `prev_created_at` field changes meaning:

```
Before: prev_created_at = element's createdAt (element identity)
After:  prev_created_at = position node's createdAt (position identity)
```

For elements that have never been moved, position createdAt = element createdAt (no difference). For moved elements, position createdAt = the move's executedAt that placed the element there.

The `json/array.go` layer converts element createdAt → position createdAt when creating move operations via `PosCreatedAt()`.

### Element Timestamps vs Position Timestamps

Two layers of timestamps exist, each tracking a different lifecycle:

```text
Element (logical value)          Position (physical slot)
├── created_at: value created    ├── position_created_at: slot created
├── moved_at: (deprecated)       ├── position_moved_at: element placed here (LWW)
└── removed_at: value deleted    └── position_removed_at: slot abandoned
```

**Element timestamps** track the value's lifecycle — when it was created and deleted. These are shared across all container types (Object, Array, Text, etc.).

**Position timestamps** track the slot's lifecycle in the RGA linked list — when the slot was created, when an element was placed into it via move, and when the slot was abandoned because the element moved elsewhere.

The two lifecycles are independent: a value can move between slots (position changes, element stays), and a slot can become dead when its element leaves (element moves, position dies).

#### Element.MovedAt deprecation

`Element.MovedAt` / `SetMovedAt` is legacy. It was originally used to track "when this element was repositioned within its parent container." After the position-identity separation, this role is entirely handled by `ElementEntry.posMovedAt` (the LWW register).

Current state across the codebase:
- **Array**: `posMovedAt` is the source of truth. `Element.SetMovedAt` is still called for snapshot serialization backward compatibility, but `Element.MovedAt()` is never read by any logic.
- **Object (ElementRHT)**: `SetMovedAt(v.CreatedAt())` is called on LWW winners in `ElementRHT.Set`, but `MovedAt()` is never read by any logic either.
- **All types**: `MovedAt()` is only read by `to_bytes.go` for snapshot serialization (`JSONElementSimple.moved_at`).

Plan: gradually remove `Element.MovedAt` in a follow-up.
1. Stop writing `JSONElementSimple.moved_at` for Array elements (use `RGANode.position_moved_at` instead)
2. Stop calling `SetMovedAt` in `ElementRHT.Set` (no reader exists)
3. Remove `MovedAt` / `SetMovedAt` from the `Element` interface
4. Remove `movedAt` field from all Element implementations

### Protobuf

**Move operation message**: No structural change. The `prev_created_at` field carries a position node identity instead of an element identity — same wire type, different semantic.

**Snapshot (RGANode)**: Three new fields for dead position nodes and move metadata:

```protobuf
message RGANode {
  RGANode next = 1;
  JSONElement element = 2;
  TimeTicket position_created_at = 3;
  TimeTicket position_moved_at = 4;
  TimeTicket position_removed_at = 5;
}
```

- `position_created_at`: The position node's own identity. For moved elements, differs from element's createdAt.
- `position_moved_at`: LWW timestamp of the move that placed the element here. Null for insert-created positions.
- `position_removed_at`: When the position became dead (for GC). Null for live positions.

Dead position nodes are serialized as RGANodes with no element.

### GC

Dead position nodes implement `GCChild` interface and `RGATreeList` implements `GCParent`:

- `MoveAfter` sets `removedAt = executedAt` on dead position nodes and returns them for registration.
- `operations/move.go` Execute registers dead nodes via `root.RegisterGCPair`.
- `json/array.go` registers dead nodes via `context.RegisterGCPair`.
- On snapshot restore, `root.NewRoot` calls `Array.GCPairs()` to register existing dead nodes.
- `GarbageCollect` purges dead nodes from the linked list once all clients have synced past.

### Code Removed

| Content | Replaced by |
|---------|-------------|
| `movedFrom *RGATreeListNode` | Position node identity in operation |
| `MovedFrom()` / `SetMovedFrom()` | Removed |
| Cascade loop | LWW makes it unnecessary |
| `findNextBeforeExecutedAt` movedFrom backtracking | Removed — forward skip only |

### Code Kept

| Content | Reason |
|---------|--------|
| RGA linked list + splay tree | PositionNode reuses this structure |
| `insertAfter` forward skip | Part of RGA insertion rule, still needed |
| `InsertAfter` / `Delete` | Work independently of move |

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Dead position node accumulation increases memory | VersionVector-based GC purges dead nodes |
| Move operation semantic change (prev_created_at) | For never-moved elements, position createdAt = element createdAt (backward compatible). Mixed old/new clients on moved elements need version gating |
| Incompatible with existing snapshots | New proto fields are additive. Old snapshots load correctly (new fields default to nil). New snapshots with dead nodes require new code |
| Interaction with Set operation (InsertAfter + Remove) | Set operates at the element layer, independent of position. Verified with tests |

### Design Decisions

| Decision | Rationale |
|----------|-----------|
| Adopt Kleppmann LWW approach | Formally verified (Isabelle/HOL). Convergence guaranteed by CRDT primitive composition |
| Remove movedFrom + cascade entirely | Patching cannot achieve convergence (counterexample proven). Fundamental structural change needed |
| Separate PositionNode / ElementEntry | Direct implementation of Kleppmann's principle. Loro uses the same pattern (ListItem / Element) |
| Position node identity for move destinations | Solves move-after-moving-target. Dead position nodes are stable anchors in the list regardless of application order |
| Two maps (nodeMap + elementMap) | nodeMap for position lookups (move destinations), elementMap for element lookups (delete, set, etc.). Clean separation of concerns |
| Keep existing RGA linked list + splay tree | Insert/delete work correctly. Only move needs redesign |
| Keep forward skip, remove only backtracking | Forward skip is RGA insertion rule. Backtracking was movedFrom-specific |

## Alternatives Considered

| Alternative | Reason for rejection |
|-------------|---------------------|
| Extend movedFrom to a list | Cascade movedAt overwrite problem remains. Convergence not guaranteed — counterexample exists |
| Loro-style full redesign (Fugue + dual B-tree) | Excessive scope. Requires replacing RGA-based insert/delete. 80% slower encode/decode, 50% more memory |
| Kleppmann undo-redo-redo (tree move paper) | Excessive complexity for lists. LWW position register is simpler with the same convergence guarantee |
| Decompose move as delete + reinsert | Concurrent moves duplicate the element. Element identity is lost |
| Fix only the cascade logic | Cascade is inherently order-dependent. Fundamentally non-deterministic |
