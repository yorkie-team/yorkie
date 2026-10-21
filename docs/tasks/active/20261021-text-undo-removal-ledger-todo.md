**Created**: 2026-10-21

# Keep a Text removal the undo never saw

## Problem

Fixes the Text half of #2237. JS counterpart: yorkie-js-sdk (same branch
name). Two ways an identity-preserving undo of a Text deletion diverges
replicas:

1. `RGATreeSplit.Restore` cleared a tombstone unconditionally, so an undo also
   revived text a concurrent deletion it never saw had removed. From
   `abcdefgh`, B deletes `d` and undoes it while A deletes `defgh`: A ends
   with `abcd` and B with `abc` depending on delivery order.
2. A remote deletion removed every node whose creation it knew, so also a
   node the deleter saw removed and an undo revived here (#2237: d1 `efh`,
   d2 `eh`).

## Layer decision

A per-node removal ledger in `RGATreeSplit`, the structure Text is built on:
the deletions that removed a node while it was live in the deleter's view,
and the undos that cancelled some of them. `removedAt` stays the latest
uncancelled deletion, so nothing changes for a node no undo touched. Same
design as the JS SDK change; see `docs/design/undo-redo.md`, Removal Ledger.

## Plan

- [x] RED: `pkg/document/text_undo_removal_ledger_test.go`, through protobuf:
      undo vs unseen concurrent deletion (both orders), #2237,
      undo-redo-undo, a deleter that saw the piece removed, the ledger
      through a snapshot, undo after GC with another deletion holding the
      piece, plain revive after a snapshot. 7 of 8 fail on main.
- [x] Ledger in `RGATreeSplitNode`, live-in-view rule in `deleteNodes`,
      `RemovedBy` in `Restore`, version vector in `Retombstone`, purged ledger
      in `purge`, recreation along the purged boundaries.
- [x] `Text.Edit` returns spans with `RemovedBy`; the Edit reverse rewrites
      `RemovedBy` to the removal its execution made.
- [x] Protobuf fields and converter (`removed_by`, `has_ledger`, `removals`,
      `removal_cancels`, `purged_removals`); default ledger elided.
- [x] `api.CapRestoreRemovedBy` in `ServerCapabilities`.
- [x] `docs/design/undo-redo.md`: Removal Ledger.
- [x] `make verify`.

## Out of scope

- Undo after GC still anchors a recreated piece next to the following piece
  of its insertion (`findRestoreAnchor`), which can land after text inserted
  concurrently there.
- The Tree half of #2237 (`CRDTTree` has its own removal path).
- Purged ledgers are kept for the life of the document.
- The Go client does not learn server capabilities (it does not send
  presence patches either), so its undo sends `removed_by` to an older
  server too.
