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
