---
title: array-set-move-undo
---

# Array Set + Move Undo

## Problem

When an element is moved and then set (value replaced), undoing the set restores the value at the element's **original (dead) position** instead of its **moved position**.

```text
Initial: [a, b, c, d, e]
Move a after c: [b, c, a, d, e]
Remove b: [c, a, d, e]        ← S2
Set index 1 to 's': [c, s, d, e]  ← S3

Undo set (expect S2): [a, c, d, e]  ← WRONG (a at front, not after c)
                       [c, a, d, e]  ← EXPECTED
```

This was introduced by the LWW position register redesign (`array-move-convergence.md`), which separates element identity from position identity.

### Root Cause

`RGATreeList.set(createdAt, newValue, executedAt)` is implemented as `insertAfter(createdAt, newValue) + delete(createdAt)`. The `insertAfter` uses `nodeMapByCreatedAt[createdAt]` to find the insertion anchor. After a move, this finds the **original dead position** — not the element's current position.

This is **intentional for concurrent convergence**: two replicas applying concurrent Move + Set in any order both insert at the original position, producing the same result. The Go SDK documents this explicitly:

> "Use the element's original position (via nodeMapByCreatedAt[createdAt]) so that Set always inserts at the position where the element was when the Set operation was created, regardless of concurrent moves."

The trade-off: undo of Set after Move places the restored value at the dead position.

### Goals

- Undo of Set correctly restores the element at its pre-set position, even after Move.
- Concurrent Move + Set convergence is preserved.
- Both Go SDK and JS SDK produce the same behavior.

### Non-Goals

- Changing how `RGATreeList.set` works for forward operations.
- Fixing Move + Set interaction for redo (same approach applies symmetrically).

## Design

### Option A: Position anchor field in ArraySetOperation proto

Add an optional `prev_pos_created_at` field to `ArraySetOperation` in the protobuf schema. When present, `execute` uses it as the insertion anchor instead of `createdAt`.

- Forward set: `prev_pos_created_at` is unset → uses `createdAt` (convergence preserved).
- Reverse set (undo): `prev_pos_created_at` = element's position at undo-capture time → inserts at correct position.

```protobuf
message Operation {
  message ArraySet {
    TimeTicket parent_created_at = 1;
    TimeTicket created_at = 2;
    JSONElementSimple value = 3;
    TimeTicket executed_at = 4;
    TimeTicket prev_pos_created_at = 5; // NEW: position anchor for undo
  }
}
```

Changes required:
- `resources.proto`: Add field to `ArraySet` message
- Go `operations/array_set.go`: Add `prevPosCreatedAt` field, use in Execute
- Go `converter`: Serialize/deserialize the new field
- JS `array_set_operation.ts`: Same changes
- JS `converter.ts`: Same changes

Pros: clean, no behavioral change for forward operations, self-contained in the operation.
Cons: proto change requires coordinated Go + JS release.

### Option B: Composite reverse (ArraySetOperation + MoveOperation)

Return two operations from `toReverseOperation`:
1. `ArraySetOperation` (restores value at original position)
2. `MoveOperation` (moves restored element to correct position)

This avoids proto changes but requires `ExecutionResult.reverseOp` to support arrays.

Cons: the MoveOperation's `createdAt` (element to move) changes during undo due to `reconcileCreatedAt`, making it difficult to keep the paired operations consistent.

### Option C: Use current position in set for local operations

Change `set` to use `elementMapByCreatedAt[createdAt].positionNode` for insertion. This breaks concurrent convergence (Move→Set and Set→Move produce different positions).

**Rejected**: convergence is a harder requirement than undo correctness.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Proto field addition requires coordinated release | New field is optional — old clients ignore it, new clients fall back to `createdAt` when absent |
| Redo after undo needs same treatment | Reverse of reverse set also captures position anchor. Symmetric. |

### Design Decisions

| Decision | Rationale |
|----------|-----------|
| Option A (proto field) | Cleanest approach. No behavioral change for forward ops. Self-contained in operation. Backward compatible via optional field |
| Keep `set` using original position for forward ops | Required for concurrent convergence |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Option B (composite reverse) | `reconcileCreatedAt` during undo changes element identity, breaking the paired MoveOperation. Would need complex bookkeeping |
| Option C (current position in set) | Breaks concurrent convergence. Move→Set and Set→Move produce different results |
| Skip undo for set-after-move | Temporary workaround, not a solution. Currently applied in JS SDK tests |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
