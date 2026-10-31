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

The Follow-up below supersedes the items marked superseded.

- [x] RED: `pkg/document/text_undo_removal_ledger_test.go`, through protobuf:
      undo vs unseen concurrent deletion (both orders), #2237,
      undo-redo-undo, a deleter that saw the piece removed, the ledger
      through a snapshot, undo after GC with another deletion holding the
      piece, plain revive after a snapshot. 7 of 8 fail on main.
- [x] (partly superseded) Ledger in `RGATreeSplitNode`, live-in-view rule
      in `deleteNodes`, `RemovedBy` in `Restore`, version vector in
      `Retombstone`, purged ledger in `purge`, recreation along the purged
      boundaries.
- [x] (superseded) `Text.Edit` returns spans with `RemovedBy`; the Edit
      reverse rewrites `RemovedBy` to the removal its execution made.
- [x] (superseded) Protobuf fields and converter (`removed_by`,
      `has_ledger`, `removals`, `removal_cancels`, `purged_removals`);
      default ledger elided.
- [x] (superseded) `api.CapRestoreRemovedBy` in `ServerCapabilities`.
- [x] `docs/design/undo-redo.md`: Removal Ledger.
- [x] `make verify`.

## Follow-up (2026-10-30)

A randomized harness (three or four replicas over the `advServer` model in
`gc_rga_fuzz_test.go`; insert, delete, undo, redo and sync; GC on and off;
every exchange through protobuf) still found divergence on top of the plan
above. Each item below came from a minimized trace.

- [x] Propagate an undo or redo whose operations ran even when nothing is
      observable (the gate was on `Observable`); redo removes only what the
      redoer saw live. Done in "Redo only the Text a redoer saw live".
- [x] The reverse of a redo and of an insertion's undo names only the pieces
      the removal was recorded on, keeping the attributes #2273 refreshes.
- [x] GC keeps a tombstone until its whole ledger is known everywhere and a
      single deletion holds it; drop the purged ledgers and
      `purged_removals`. An empty text after typing and deleting 2000
      characters snapshots to 69B again (92KB before).
- [x] Removal spans in document order, not `deleteNodes`' map order.
- [x] `findRestoreAnchor` anchors before the nearest following piece of the
      insertion, not the rightmost: a deletion after the undo erased text on
      a collected replica (on main too).
- [x] Link a recreated piece to the nearest surviving pieces of its
      insertion: with the piece before the gap purged too, a later purge
      left a stale insertion link and the next edit failed (on main too).
- [x] Derive the undone removal from the undo's actor; drop
      `RestoreSpan.removed_by` and `restore-removed-by`. Encode the ledger
      as `TextNode.removal_events`; a reader that finds events breaking the
      rules keeps `removed_at` alone.
- [x] Record no second uncancelled removal by one actor: only a change
      whose vector leaves out its own removal sends one, and it stacked
      removals that held tombstones forever.
- [x] GC drops a removal with its cancel once every replica knows the
      cancel, so an undo-heavy text stops carrying every removal it ever had
      (a 36-character text deleted and undone 20 times, then split by 35
      inserts: 31KB, now 2.9KB as on main). A first removal stays removedAt
      alone.
- [x] `make verify`.

## Review

- Ad-hoc fuzz runs with the harness's model, 10000 seeds each: GC off, 3
  and 4 replicas, with and without inserts: 0 diverged (the plan above
  alone: 25 to 680 by setting). GC on, deletes only, 3 and 4 replicas: 0.
  GC on with inserts: 92 of 10000 (order 62, content 30), all the
  out-of-scope anchor case below. The committed `TestTextUndoFuzz` runs a
  1000-seed subset of these settings.
- Every step of 3000 seeds re-encoded each replica's text as a snapshot and
  compared the decoded ledger with the original: 1.67M checks, 0 mismatches.

## Out of scope

- After GC, a recreated piece can still land after text another insertion
  placed between the gap and the following piece of its own insertion, and
  a deletion spanning it then erases text on that replica. Fixing it needs
  the purged piece's position. Also on main and in JS.
- The Tree half of #2237 (`CRDTTree` has its own removal path).
- A tombstone held by two concurrent deletions that are never undone is
  never collected, with or without undo; main collects it.
- The JS SDK (yorkie-js-sdk#1494) needs the same changes, including
  `removal_events` instead of its `has_ledger` fields.
