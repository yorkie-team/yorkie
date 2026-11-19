# Keep a release record zero when nothing will collect the charge

**Created**: 2026-11-19

Issue: yorkie-team/yorkie#2261 (follow-up filed by the review loop on #2259).

## The finding as filed

> `moveSizeToGC` still tops up a zero release record, bypassing the invariant
> the new `ChargeToRemovedElement` guard establishes.

The reviewer's reasoning: `release` (root.go) writes `sizeInGC[elem] =
DataSize{}` for every element of a subtree a restore orphaned, and deletes its
`gcElementPairMap` entry. `index` retires such a record only while it is
*exactly zero*. Anything that tops the record up therefore makes it permanent,
and — since `release` already dropped the only collection entry that would
have subtracted it — leaves `docSize.GC` holding bytes nothing will ever take
back out.

## What is actually true in `main`

Two corrections, both load-bearing:

1. **There is no `ChargeToRemovedElement` in `main`.** The function the issue
   quotes at root.go:819 is `Root.AccMovedElement` (root.go:845), landed by
   #2037, and it has **no** zero-record guard: it tops up whatever record it
   finds, zero included.

2. **`moveSizeToGC` is not where the hole is.** Its top-up of a zero record is
   balanced, because every one of its three callers registers (or is covered
   by) a `gcElementPairMap` entry for the element whose charge it just raised:

   - `adoptRemovedElementPair` → registers the pair itself.
   - `RegisterRemovedElementPair` → registers the pair itself.
   - `moveDescendantsToGC` → only ever called from those two, and
     `deregisterElement` walks the same `Descendants` set when the ancestor's
     pair is purged.

   So a released element that a peer removes again is charged to GC *and*
   re-enters the worklist; `collect` → `deregisterElement` subtracts exactly
   the recorded amount and deletes the record.
   `TestReleasedRecordStaysWhileAddressable` already walks that path and
   asserts the root still rebuilds to the same sizes afterwards.

**`AccMovedElement` is the caller with no entry behind it.** It is reached
from `Move.Execute` / `json.Array.MoveAfter`, which charge the `movedAt`
ticket a move stamps. For an element that is already a tombstone it routes the
diff to GC and raises the record — correct for an ordinary tombstone, wrong
for a released one:

- the released element's bytes are charged to **neither** ledger, so a diff
  added to GC is residue by construction;
- `release` deleted its collection entry and a second `Remove` of an
  already-removed element reports `nil`, so nothing re-registers it;
- the record is now non-zero, so `index` can never retire it either — the
  dead subtree stays reachable from the map for the life of the document.

Reachable with the same setup the existing test builds: a peer adds a
container into a container that has already been removed, the removal is then
undone (orphaning both), and the peer — which has not seen the undo — moves an
already-tombstoned member inside the orphan it can still address.

## Plan

1. `Root.AccMovedElement`: when the record is exactly zero, charge nothing and
   leave the record zero. A released element's bytes belong to no ledger, and
   keeping the record zero keeps it retirable by `index`.
2. `Root.moveSizeToGC`: no behaviour change; state the invariant that makes
   its top-up of a release record safe, so the next reader does not have to
   re-derive it (this is what the issue asked to look at).
3. Regression tests in `pkg/document/crdt/root_size_in_gc_internal_test.go`:
   - a move stamped on a released tombstone leaves `docSize` and the record
     untouched, and the record is still retired when a copy takes the slot;
   - the already-covered remove-inside-an-orphan path keeps its balance
     (extend the existing test with the record-level assertion).

## Acceptance criteria

- [x] No top-up of a zero release record from a caller that registers no
      collection entry.
- [x] `moveSizeToGC`'s deliberate top-up is documented with the invariant that
      makes it safe.
- [x] Regression test fails before the change and passes after.
- [x] `make lint` and `go test ./pkg/document/...` green.

## Out of scope

- The zero record of an orphan that no copy ever takes over is still held for
  the life of the document (known limitation from
  `20260927-element-rht-loser-and-gc-record`; the JS SDK uses a `WeakMap`).
- The `ErrChildNotFound` a peer gets when it removes an orphan's member whose
  parent slot *was* taken over by the restored copy.
