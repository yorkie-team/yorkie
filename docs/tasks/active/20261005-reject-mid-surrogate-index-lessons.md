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

## Follow-up Review

- Reverse builders resolve SDK-derived indexes after mutation, so they use
  `FindPosUnchecked` consistently with undo execution. This supersedes the
  earlier decision to retain the checks: size arithmetic is not a guarantee
  of character boundaries.
- White-box regression cases supply mid-pair indexes to all three builders,
  covering both endpoints of the split reverse. These pin the resolver
  contract; they do not reproduce a full concurrent-edit sequence. Checked
  `FindPos` still rejects the same index.

## Concurrent Replica Coverage

- Added two-document ChangePack tests for parent deletion versus emoji
  insertion, split versus overlapping emoji replacement, and merge versus
  overlapping emoji replacement. Each propagates two undo/redo cycles and
  checks both replicas' exact XML.
- All three cases pass before and after the reverse-builder resolver change
  (the prior implementation was tested with a Go source overlay). They are
  compatibility tests, not reproductions of the review's claimed rejection.
- A bounded exploratory program checked 58,248 valid schedules using local
  deletion/split and remote deletion/emoji replacement, followed by up to two
  undo/redo cycles. No ErrInvalidUTF16Index builder failure was found. This
  does not exhaust all histories, nesting, actor orders, or GC states.
- The concurrent split case reproduces existing surrogate corruption: undo
  leaves U+FFFD and redo does not restore the original structure. Both
  implementations have the same result. This remains a reconciliation
  limitation, not a regression introduced or fixed by this change.

## Reproduced Reverse-Builder Rejection

- Extended exploratory histories found a real `toSplitReverseOperation`
  endpoint inside a surrogate pair. Reduced it to four public Tree.Edit
  calls plus two change exchanges after initial synchronization; random
  generation and instrumentation are not part of the regression test.
- B splits nested p/section at index 3, level 2. A receives it and replaces
  [2,4) and [5,7) with emoji, then sends both changes back. B splits at the
  valid caller boundary 4, level 2. Split lineage puts the new boundaries
  apart, so the reverse range [4,8) ends inside direct section emoji text.
- `TestTreeSplitReverseAfterRemoteSplitHistory` fails with
  ErrInvalidUTF16Index on the previous implementation (Go source overlay),
  and passes on the current implementation. The prior Update has already
  changed root XML when it returns the error. The fixed test also checks
  propagation and a subsequent Update retain the same tree on both replicas.
- This proves rejection in the split reverse builder, not each of the other
  four calls independently. Using the unchecked resolver across all three
  builders follows the same documented internal-position policy.
- Undo/redo of this reduced history still has existing reconstruction
  limitations and can split an emoji; the resolver change preserves the
  old internal resolution policy rather than repairing reverse ranges.
