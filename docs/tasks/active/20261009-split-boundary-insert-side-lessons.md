# Lessons: Port the split-boundary insert-side rule

**Created**: 2026-10-09

## Check a review finding against both bases before calling it a regression

Finding (a) on yorkie-js-sdk#1467 claimed a delivery-order divergence. Running
the scenario on Go `main`, on the Go port, on the JS PR head and on its
merge base separated "the rule introduces this" from "this was always broken":
both bases converge, both heads diverge with the same node shapes. Without the
base runs the skip reason could not say "converges on main".

## A parity port inherits known bugs on purpose

Fixing finding (a) in Go alone would make a Go replica and a JS replica
disagree on every delivery order, which is worse than the order-dependent gap
both share. The case is recorded as a skipped subtest rather than fixed.

## Attribute each remaining divergence to a change before skipping it

Running the fuzz minima with each V8 change switched off one at a time showed
that three of the seven come from `movedBySplit` alone, and that one of those
converges on `main`. A skip reason that only said "remaining #1436 case"
would have hidden that the filter trades one divergence for another.

## A gate must read only what every replica holds alike

All three review findings were the same mistake: a gate reading local state
(`IsRemoved`, a child typed after the split, a product split at another
boundary) where the change's version vector or the node tickets were needed.
Ask of every new condition whether two replicas with different delivery
orders could answer it differently.

## Review round 2 (panel): a collapsed range is not the same as an insert

Five findings, all on the same two seams.

**The call-site gate was named after the wrong property.** "Collapsed range"
was meant to say "an insert's anchor", but `Edit(i, i, nil, 1)` -- Enter --
is a collapsed range too, and so is the `Edit` the merge redirect of §1.1
resolves. Gating on `len(contents) != 0 && splitLevel == 0` and on
`declaredMergeSource(from, fromParent) == nil` says what was meant. The split
half of this was a live bug: seed 3768 of the remaining-divergence table
converges with it and is no longer skipped.

**Two consumers keyed off `fromParent` silently.** `intendedMergeParent`
(§9.4) and `propagateMergeDeletes` (§6.2) both compare `fromParent` against a
merge destination and do nothing when it does not match, so moving
`fromParent` turned both off with no error and no failing test. When a new
phase rewrites a variable that later phases read, grep every reader for an
equality test against it -- the ones that `continue` on mismatch are the
dangerous ones.

**One rule, one measurement.** `advanceIntoSplitProducts` scanned the
boundary run itself with `After(editedAt)` while `boundaryInsertRunOf` used
the version vector, and the design doc claimed they counted the same
children. Two functions that have to agree should not both contain the
comparison; the walk now calls `boundaryInsertRunOf`.

**A guard in every peer is a contract.** The new walk lacked the
`len(versionVector) == 0` early return `orderSameBoundarySplit` and
`mergedAnchorInterloperGuard` both have. With the vector empty every gate
reads at its most permissive while the rule it must agree with is off -- the
worst of both.

## Hand-rolled causality checks drift from the helper

`boundaryInsertRunOf` inlined `vv.Get(actor); ok && l >= lamport` instead of
calling `time.TicketKnown`, which differs on exactly one input: an empty
vector, where the helper says "known" and the inline form says "concurrent".
