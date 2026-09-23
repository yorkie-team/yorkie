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
