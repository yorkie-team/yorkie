# Lessons: SplitText records the left piece's length in runes

- **One unit everywhere.** The tree's lengths and offsets are UTF-16 code
  units because they have to agree with the JS SDK's string indices. A single
  site computing in runes is invisible for BMP text, which is most test data,
  and fatal for the first emoji or flag.
- **Test text outside the BMP.** A string with a surrogate pair in the middle
  exercises every place where runes and code units could be confused; plain
  ASCII and Hangul do not.

## Review round 1 (panel)

- **Correctness (major), accepted.** Splitting at an offset inside a surrogate
  pair let `utf16.Decode` rewrite the character as U+FFFD in both halves, so the
  replica kept text no other replica has. A test that skips those offsets pins
  nothing. Restoring the JS SDK's lone-surrogate halves is not possible in a Go
  string, so the first attempt reported `ErrSplitInSurrogatePair`.

## Review round 2 (panel)

- **Failing is not an answer for a position already in history.** An offset
  inside a surrogate pair is producible by a JS replica, so it can already sit
  in a stored change. `Change.Execute` aborts the whole change on any error but
  `ErrOperationSkipped`, and every server replay path propagates it, so the
  guard from round 1 turned "two replacement characters" into "this document
  can never be loaded again" — the outcome `docs/design/tree.md` names as the
  one to avoid. The same reasoning that justified `ErrSplitOutOfRange` does not
  carry over: that guard replaced a slice-out-of-range panic, this one replaced
  a split that worked.
- **Move the position instead of refusing it.** `SplitText` now moves a
  mid-pair offset forward to the end of the pair. Nothing fails, no character
  is corrupted, both pieces keep their lengths in UTF-16 code units, and every
  Go replica moves the same offset to the same boundary, so segmentation still
  converges. Reaching the node's end that way is the existing "nothing to split
  off" no-op.
- **The direction is load-bearing, and only the test at document level caught
  it.** Moving *back* looked equally valid and passed the `SplitText` unit
  test, but an edit resolves the same anchor twice: `from` split the node at
  the earlier boundary, then `to` re-resolved, found the fresh right piece,
  split it at an offset that moved back to 0 — a no-op — and so anchored after
  the whole piece. The caret edit at offset 9 deleted `🇰🇷ㅇㄹ`. Moving
  forward puts both resolutions on the same boundary. The unit test on the leaf
  helper could not see this; the document-level test the panel asked for is
  what failed.
- **Fixing the producer fixes the consumer.** `recreateFromSpan` slices span
  text at piece boundaries, and piece boundaries are where `SplitText` cut, so
  aligning the split is what keeps that decode off a lone surrogate. It cannot
  align its own window: the recreated node has to cover exactly
  `[offset, offset+length)` code units or every piece offset in the insertion
  shifts, and U+FFFD is the one decoding that preserves that length.
- **A failed remote change leaves a dirty clone.** `applyChanges` executed
  changes against `cloneRoot` and returned on error without dropping it, while
  `Update` nils it precisely to avoid exposing invalid state. `Tree.Edit`
  applies the `from` split before resolving `to`, so a mid-change failure is
  not a no-op. The clone is now dropped on any error, so the next update
  rebuilds it from the document.
- **Moving a split offset broke a caller that looked the split product up by
  the requested offset.** `isolateTextRange` probed `findFloorNode` under
  `from` and trusted a non-nil result. `findFloorNode` returns the greatest id
  `<= ` the probe, so once `SplitText` moved a mid-pair cut forward, the probe
  missed the right half and returned the LEFT piece — `Restore` and
  `Retombstone` then acted on the text *before* the range a wire-supplied span
  addressed. The fix reads the boundary back off the left piece (`Split`
  mutates the node in place, so its end IS the offset the cut used) and
  rejects a probe that does not land exactly there. A range that names no
  character boundary at all now isolates nothing rather than the wrong node.
- **Every path that executes on the clone first needs the same drop.**
  `executeUndoRedo` and `Update`'s document execute had the dirty-clone bug
  `applyChanges` was fixed for. A defer on a named return covers all of
  `executeUndoRedo`'s exits at once.
- **Validate at the point of use when the wire cannot decide.** Whether a span
  bound lands on a character boundary depends on tree state, not on the
  message, so the converter can only reject a negative offset or a
  Length/Value mismatch; the slice in `recreateFromSpan` guards its own range.

## Review round 3 (panel)

- **A guard written for one bound belongs on both.** `isolateTextRange` read
  the moved cut back off the piece for `from` and left `to` trusting its
  request. A span ending inside a surrogate pair therefore isolated a node one
  character too long — `Retombstone` deleted the emoji the span did not name.
  The closing bound now reads the boundary back the same way and skips the
  range when it is not `to`. The symmetry is the lesson: a forward-moved cut is
  a property of `SplitText`, so every caller of it has to re-read its result.
- **Put a wire check on the shared decoder, not on one caller.** The negative
  offset was rejected in `fromTreeRestoreSpans`, which covers restore spans
  only; `fromTreeNodeID` is what every tree id goes through — span ids, their
  parent/sibling anchors, and the `from`/`to` `TreePos` of every TreeEdit and
  TreeStyle. Moved it down one rung, where one check covers all of them. A
  change is persisted before it is executed, so an unexecutable position is a
  permanent per-document failure, not a rejected request.
- **A clone drop is testable through `Root()` vs `RootObject()`.** The three
  drop sites looked untestable because they need a change that applies to the
  clone and fails on the document. `Document.Root` reads the clone and
  `Document.RootObject` the document, so the invariant is one assertion; the
  triggers are a clone-only element (what `Root()` writes never becomes a
  change), a fabricated undo entry whose second operation names no parent, and
  a remote change whose second operation targets an element the receiver has
  collected. Each test was re-run with its own drop removed to confirm it
  fails.
