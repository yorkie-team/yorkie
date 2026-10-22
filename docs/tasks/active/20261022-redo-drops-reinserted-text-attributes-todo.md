**Created**: 2026-10-22

# Redo after GC drops the attributes of re-inserted text

## Problem

yorkie#2273. `Edit.toReverseOperation` records the run an edit inserted as a
`retombstoneSpans` entry carrying `Content` but no `Attributes`
(`pkg/document/operations/edit.go:319-330`). The comment there argues the
attributes are irrelevant because re-removing a run never reads them, which
holds for the undo step only.

The redo flips `restoreMode`, so that same span becomes the content to
restore. `RGATreeSplit.restore` re-establishes a surviving piece by clearing
its `removedAt` — attributes intact — but recreates a purged piece from the
span's value alone. A replica that garbage-collected the tombstone between the
undo and the redo therefore revives the text with no attributes, while a
replica that still holds the tombstone revives it with them. The two diverge.

Repro from the issue: X and Y share `"ab"`; Y runs
`Edit(1, 1, "X", {b: "1"})` and undoes it; X receives both; Y garbage-collects
(purging the node on Y only); Y redoes and X receives it. Y ends with `a X b`,
X with `a X{b:"1"} b`.

## Options explored

| Option | Verdict |
|---|---|
| Carry the edit's `attributes` on the inserted span | Chosen: the span already has an `Attributes` field, already on the wire (`api.RestoreSpan.attributes`), already round-tripped and already honoured by `Text.Restore` |
| Make the restore path look the attributes up from the tombstone | Impossible: the GC'd replica has no tombstone left — that is the defect |
| Have the redo re-insert the run as a fresh node with attributes | Rejected: copy-reinsertion is what the identity-preserving path exists to avoid; a fresh node sorts ahead of an unrevived neighbour |

## Plan

- [x] Reproduce: a replica that collects between undo and redo diverges (Red)
- [x] `toReverseOperation` copies the edit's `attributes` onto the inserted
      span when non-empty (nil otherwise, matching how `Text.Edit` fills
      `removedSpans`)
- [x] Regression tests: GC'd vs non-GC'd replica after redo; chained
      undo/redo; plain insert still carries no attributes
- [x] `make lint`, `go test ./pkg/document/... ./api/converter/...`

## Notes

The JS SDK builds `insertedSpans` the same way; that half is tracked in
yorkie-team/yorkie-js-sdk#1497. No proto change: `api.RestoreSpan` already has
`attributes`, and both converters already carry it.

The attributes recreated from a span are stamped with the restoring op's
ticket rather than the original edit's, as every other span-recreated
attribute already is. That is pre-existing behaviour of
`Text.Restore` and is not touched here.
