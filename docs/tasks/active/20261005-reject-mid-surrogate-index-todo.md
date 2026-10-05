# Reject indexes that split a UTF-16 surrogate pair

**Created**: 2026-10-05

PR #2085, fixing #2065 on the Go side. The JS half is yorkie-js-sdk#1441.

## Scope

`Text.CreateRange` and `Tree.FindPos` reject a local index that falls between
the two code units of a surrogate pair with `crdt.ErrInvalidUTF16Index`.
Remote operations carry CRDT positions and are not affected. See
"Local Index Validity: Surrogate Pairs" in `docs/design/document-editing.md`.

## Tasks

- [x] Reject mid-pair indexes in `Text.CreateRange` and `Tree.FindPos`
- [x] Check boundaries without encoding the whole node
- [x] Cover every index- and path-based entry point, including later nodes
      after a split
- [x] Pin that a remote op with a mid-pair offset still applies and that
      undo/redo of an earlier local edit still works afterwards
- [x] Discard the clone when the updater panics, so an edit made before the
      rejection in the same `Update` does not survive in `Root` or leak into
      the next change
- [x] Resolve reconciled undo indexes through `Tree.FindPosUnchecked`, so a
      Case 5 reconciliation that lands mid-pair does not fail the undo after
      its history entry was popped
- [x] Record the undo and panic behavior in the design doc

- [x] Resolve reverse-operation builder indexes without the pair check and
      cover all three builders with mid-pair regression cases

## Known Limitations

- `TreeEdit.ReconcileOperation` Case 5 places the reconciled range at the
  start of the remote content without counting it, in Go and JS alike. An
  undo can therefore still split a pair, as it did before this change.
  Fixing the formula changes both SDKs and is left as its own task, per the
  undo/redo port's rule of porting JS defects as-is.
