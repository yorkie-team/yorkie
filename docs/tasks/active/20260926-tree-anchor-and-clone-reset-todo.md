**Created**: 2026-09-26

# Guard empty-text anchors and reset the clone on failed applies

Go side of yorkie-js-sdk#1394. Comparing the JS SDK against Go `main` after
#2035 turned up two gaps that both implementations had; the JS PR fixes them
there, this one fixes them here.

## Problems

1. **`leftAnchorID` anchors an empty text node at offset `-1`.**
   `Offset + Length() - 1` has no last character to point at. Local edits
   never create an empty text node, but a remote peer's contents can.
2. **A failed apply leaves the clone and the root apart.** `Change.Execute`
   does not roll back and every change runs on the clone before the root, so
   a change that fails partway leaves the two holding different prefixes.
   `Update` already drops the clone when the updater fails, but not when the
   root's `Execute` does; `applyChanges` and `executeUndoRedo` never drop it.

## Plan

- [x] `leftAnchorID`: return the node's own ID when `Length() == 0`.
- [x] `Update`: drop the clone when the root's `Execute` fails.
- [x] `applyChanges`, `executeUndoRedo`: drop the clone on any error, via a
      deferred reset. The `ErrRefusedDuringUpdate` return comes before the
      defer, because an updater is still using the clone.
- [x] Tests that fail on `main`:
      `pkg/document/crdt/tree_left_anchor_test.go`,
      `pkg/document/clone_reset_test.go` (remote change with a second
      operation whose parent does not exist).

## Not covered

- The `Update` and `executeUndoRedo` paths have no test of their own: from
  identical clone and root, an operation fails on both or on neither, so
  reaching a root-only failure needs a prior divergence.
- Mid-surrogate-pair splits and partial-failure semantics are tracked as
  design issues, not here.

## Verify

- `make verify`: green.
