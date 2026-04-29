---
title: tree-split-undo-redo
target-version: 0.7.8
---

# Tree Split Undo/Redo

## Summary

Generating reverse operations for `Tree.Edit` with `splitLevel >= 1`
unlocks Yorkie-native undo/redo for paragraph splitting (Enter key) in
ProseMirror and Wafflebase. This document describes the boundary
deletion approach used as the reverse operation for both `splitLevel=1`
and `splitLevel=2`, the undo/redo cycle, and an additional fix for
content accumulation when the deleted subtree contains
already-tombstoned descendants.

This document is canonical across Yorkie SDKs. See
[undo-redo.md](undo-redo.md) for the broader undo/redo architecture and
how this fits in.

### Goals

- Generate reverse operations for `splitLevel >= 1` split edits.
- Support single-client undo/redo cycles: split → undo → redo → undo.
- Support 2-client concurrent scenarios where remote edits do not
  overlap with the split boundary (reconciliation Cases 1–2).
- Avoid resurrecting independently-tombstoned descendants when undoing
  a parent delete that includes those descendants.

### Non-Goals

- Overlapping range reconciliation Cases 3–6 — Phase 2 scope, unchanged
  by this work.
- New operation types or protocol changes.

## Proposal Details

### Reverse Operation: Boundary Deletion

A split creates new element boundaries without removing any nodes. The
boundary size is `2 * splitLevel` tokens (one close + one open tag per
level):

```
splitLevel=1: <p>ab|cd</p>  →  <p>ab</p><p>cd</p>          (2 boundary tokens)
splitLevel=2: <div><p>ab|cd</p></div>
            → <div><p>ab</p></div><div><p>cd</p></div>      (4 boundary tokens)
```

The reverse is a **boundary deletion** — a `splitLevel=0` edit that
removes the boundary tokens, merging the split elements back:

```
L1 reverse: edit(fromIdx, fromIdx + 2, undefined, 0)   // delete 2 boundary tokens
L2 reverse: edit(fromIdx, fromIdx + 4, undefined, 0)   // delete 4 boundary tokens
```

The reverse op is a standard `TreeEditOperation` with `isUndoOp=true`,
`splitLevel=0`, and integer indices for reconciliation. This means:

1. **No new operation types.** The reverse reuses the existing
   `TreeEditOperation` infrastructure.
2. **Reconciliation unchanged.** The reverse op is `splitLevel=0`, so
   the existing 6-case overlap logic in `reconcileOperation` applies
   directly.
3. **Redo works automatically.** When the boundary deletion (undo)
   executes, it removes nodes and produces its own reverse via the
   existing `toReverseOperation` path — a `splitLevel=0` re-insertion
   of the boundary nodes.

### Undo/Redo Cycle

```
split(L1)
  → undo: boundary delete (splitLevel=0, removes 2 tokens)
    → redo: re-insert boundary nodes (splitLevel=0, deep-copied nodes)
      → undo again: boundary delete (same as first undo)
```

Each step produces a standard `splitLevel=0` reverse op, so the entire
cycle uses existing infrastructure after the initial reverse op
generation.

### Pre-tombstoned Descendant Filtering

Independent of split semantics, the `splitLevel=0` reverse op for a
parent delete must not resurrect descendants the user had already
deleted in earlier operations.

Scenario (manual two-step split flow used by Wafflebase): insert a
sibling block with an empty inline, type into the inline, undo each
char (tombstoning the texts), then undo the block insert and later
redo it.

Without filtering, `clearRemovedAt` walks the whole deep-copied
subtree and clears every descendant's `removedAt`, resurrecting
content the user had independently deleted. Each undo→redo cycle
revives those descendants and the next parent-undo re-tombstones
them, growing `reverseContents` per cycle.

Fix: `CRDTTree.edit()` collects a `preTombstoned` set of node IDs that
were tombstoned **before** this edit ran. `editT`'s return tuple
exposes this set, and `toReverseOperation` deep-copies the top-level
removed node, drops descendants whose IDs are in `preTombstoned`,
then clears `removedAt` on the survivors. The redo therefore restores
only what this edit just transitioned from visible to tombstoned.

This preserves CRDT identity (same node IDs revived on redo, no new
nodes created), so multi-client semantics for non-overlapping
concurrent edits are unchanged.

### Edge Cases

| Case | Behavior |
|------|----------|
| Front split (`<p>\|ab</p>` → `<p></p><p>ab</p>`) | Undo deletes 2 boundary tokens, merges empty element back |
| Back split (`<p>ab\|</p>` → `<p>ab</p><p></p>`) | Same — boundary deletion merges trailing empty element |
| L2 front split (`<div><p>\|ab</p></div>`) | Undo deletes 4 boundary tokens, merges both levels back |
| L2 back split (`<div><p>ab\|</p></div>`) | Same — 4 boundary tokens removed |
| Concurrent parent deletion | `fromIdx + boundarySize > tree.getSize()` guard → undo is no-op |
| Concurrent insert into split result (non-overlapping) | Reconciliation Cases 1–2 shift indices correctly |
| Concurrent insert into split boundary (overlapping) | Out of scope — Cases 3–6, Phase 2 |
| Parent delete with already-tombstoned descendants | Pre-tombstoned filtering drops them from `reverseContents`; redo does not resurrect |

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Boundary size assumption (`2 * splitLevel`) wrong if concurrent edits insert nodes between split boundaries | L1 concurrent split converges. L2 forward convergence also passes. Reconciliation Cases 1–2 handle non-overlapping shifts. |
| L2 boundary deletion removes 4 tokens — more structural change than L1 | Same merge path as L1, applied twice. Verified with L2-specific tests (front/middle/back). |
| Merge semantics on undo may differ from original pre-split state (`mergedFrom`/`mergedAt` metadata) | Boundary deletion triggers the standard CRDTTree merge path, same as user-initiated merge. |
| Redo re-inserts deep-copied boundary nodes — node IDs may conflict with GC | Existing `splitLevel=0` redo path already handles this. |
| Without pre-tombstoned filtering, undo→redo of a parent delete with prior-deleted descendants accumulates content per cycle | `preTombstoned` set, populated during `CRDTTree.edit()`, is consumed by `toReverseOperation` to drop those descendants from `reverseContents`. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Boundary deletion as reverse | Reverse op is `splitLevel=0`, reuses all existing reconciliation and redo infrastructure. Simplest approach. |
| L1 first, L2 after forward fix | L2 forward convergence was broken when L1 undo was implemented. After the forward fix, L2 undo proceeded. |
| L2 single-client tests first | Validate the boundary-deletion mechanism for 4 tokens before adding multi-client complexity. |
| Single guard relaxation for L2 | `toSplitReverseOperation` already supports any splitLevel via the `boundarySize` formula. |
| `boundarySize = 2 * splitLevel` | Each split level creates one close tag + one open tag = 2 tree index tokens per level. |
| Filter `reverseContents` by `preTombstoned`, not by tombstone ticket equality | LWW overwrites of `removedAt` on already-tombstoned nodes make ticket equality unreliable. Capturing the pre-edit tombstone state by ID is robust. |

### Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| **Content-based reverse for splits** (deep-copy original node, delete split result, re-insert original) | Concurrent edits to the split result would be lost. Dangerous in collaborative environment. |
| **Split-aware reverse** (store splitLevel in reverse, execute merge on undo) | Merge is just boundary deletion. Adds a new reverse op variant for no benefit. |
| **OT-literal reverseOp payload** (Text-style content-only reverse) | Loses CRDT identity for tree elements; element attributes (RHT) require timestamp re-issuance, changing concurrent semantics; the surgical descendant filter solves the accumulation bug without these costs. |

## Test Strategy

The JS SDK uses the table-driven pattern from `history_tree_test.ts`,
declaring a state space and iterating over combinations.

### Test State Space

```
┌─────────────────┬─────────────────────────────────────────────────┐
│ Variable        │ Domain                                          │
├─────────────────┼─────────────────────────────────────────────────┤
│ SplitPosition   │ {front, middle, back}                           │
│ Action          │ {undo, undo-redo, undo-redo-undo}               │
│ ClientCount     │ {1, 2}                                          │
│ ChainOp         │ {split-only, split+insert-text, split+delete,   │
│                 │  insert-text+split}                              │
│ RemoteOp        │ {insert-text, delete-text, insert-element}      │
│ RemotePosition  │ {before-split, after-split, different-element}  │
└─────────────────┴─────────────────────────────────────────────────┘
```

### L1 Sections

- **A**: Single-client split undo/redo (table-driven, 9 tests).
- **B**: Single-client chained ops with snapshot verification (9 tests).
- **C**: Multi-client convergence with `withTwoClientsAndDocuments` (9 tests).
- **D**: Edge cases — empty paragraph undo, concurrent parent deletion.

### L2 Sections

L2 initial tree: `<doc><div><p>ABCD</p></div></doc>`. Splits use
`splitLevel=2` and create 4 boundary tokens at three positions:

```
<doc>  <div>  <p>  A  B  C  D  </p>  </div>  </doc>
  0      1     2   3  4  5  6    7      8
```

- **E**: Single-client L2 split undo/redo (9 tests).
- **F**: Single-client L2 chained ops (8 pass, 1 skipped — consecutive
  L2 splits produce tombstone structure that breaks boundary-deletion
  reverse).
- **G**: Multi-client L2 convergence using
  `<doc><div><p>ABCD</p></div><div><p>EFGH</p></div></doc>` (18 tests:
  9 undo + 9 redo).
- **H**: Multi-client L2 edge cases — front/back L2 split undo with
  concurrent remote insert (2 tests).

### Pre-tombstoned Filtering Regression

A regression test in `history_tree_split_test.ts` reproduces the
4-cycle type-undo-undo-redo flow against an empty inline in a sibling
block, then asserts the redoStack-top `contents` size is constant
across cycles. Before the fix the size grows by 4 per cycle
(`[6, 10, 14, 18]`); after the fix it stays at 2 (`[2, 2, 2, 2]`).
