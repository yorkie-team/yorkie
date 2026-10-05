# Lessons: Reject indexes that split a UTF-16 surrogate pair

**Created**: 2026-10-05

- A new error on a shared resolver reaches every caller, not only the one the
  change was written for. `Tree.FindPos` also re-resolves undo indexes that
  reconciliation computed, so checking caller input there turned a rare bad
  undo into a lost undo entry. Split the check from the resolution when an
  internal path must keep resolving.
- The json proxies report bad arguments by panicking, and `Document.Update`
  only discarded the clone on a returned error. Any change that makes a
  proxy panic on ordinary input has to make sure the panic path discards the
  clone too. A test that panics on the first statement of the updater cannot
  catch this; put a valid edit before the rejected one.

## Self Review

- Round 1 (correctness, design): no blocking findings. Applied: pin the
  resulting tree in the Case 5 test, fix a stale `Tree.FindPos` comment on
  `fromIdx/toIdx`, tighten the design doc's wording on when `Update` discards
  the clone. Not applied: moving the reverse builders in `tree_edit.go` to
  `FindPosUnchecked`. Their indexes are node boundaries of the post-edit
  tree, where a split has already turned any broken half into U+FFFD, so the
  check cannot fire there; the reviewer could not construct a case either.
