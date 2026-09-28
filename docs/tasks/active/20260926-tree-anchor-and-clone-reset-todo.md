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

Added by the review rounds (see the lessons file for why each one was
needed):

- [x] Invalidate the clone by marking it stale instead of setting it to nil.
- [x] Lock the `Document` accessors and setters (`readLocked`, locked
      `SetActor`/`SetStatus`/`SetMaxSizeLimit`/`SetSchemaRules`/
      `ResetPresences`), and move event sends off `d.mu` onto `eventsMu`.
- [x] `CreateChangePack` deep-copies the version vectors it hands out.
- [x] Presence `Clear`/`Initialize` empty and fill the clone's map in place.
- [x] Rename `Document.InternalDocument()` to `InternalDocumentForTest()`.
- [x] Start the watch loop's event pump before the first response.
- [x] Floor a restore span's negative left anchor in the converter.

## Not covered

- The `Update` and `executeUndoRedo` paths have no test of their own: from
  identical clone and root, an operation fails on both or on neither, so
  reaching a root-only failure needs a prior divergence.
- Mid-surrogate-pair splits and partial-failure semantics are tracked as
  design issues, not here.
- Negative offsets on identity `TreeNodeID`s (a node's own ID, insertion
  neighbors, merge sources, `TreePos`) are passed through as on `main`.
  Rejecting them in the shared decoder also rejected them on the stored-change
  and snapshot paths. A wire-only check is a follow-up.
- The watch loop still ignores the error of a re-established stream
  (`_ = c.runWatchLoop(ctx, d)`), as on `main`. If that restart fails, no
  pump drains the document's events. Retry with backoff is a follow-up.
- `MaxSizePerDocument` and schema rules are enforced only on the client.
  This is tracked in `docs/design/document-size-limit.md`.
- `d.updating` is still per-document. Accessors that can be reached from
  inside an updater (`Root`, `RootObject`, `ActorID`, `GarbageCollect`, and
  the read-only views) keep the escape and so stay unlocked against another
  goroutine's updater, as they were on `main`. Only a per-goroutine owner
  check would close that.

## Verify

- `make verify`: green.
