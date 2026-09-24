# docSize drifts from a rebuild in three places — lessons

**Created**: 2026-09-24

## The "one line" in item 3 was three

The issue proposed writing `""` into the tombstone `RHT.Remove` mints, and
called it one line. It is one line only if nothing downstream depends on the
tombstone being byte-identical to the value it replaced. Two things do:

- `Root.RegisterGCPair` subtracts `Child.DataSize()` from Live and adds the
  same number to GC. That is only a correct move-across because the tombstone
  currently charges exactly what the live node it replaced was charging. Shrink
  the tombstone and the value's bytes stay in Live forever — a permanent drift,
  strictly worse than the transient, self-healing GC divergence being fixed.
- `Root.collect` subtracts `Child.DataSize()` at purge, while the enclosing
  node's GC charge was taken when the node was removed and still included the
  then-live attribute. Shrink the tombstone and the same bytes strand in GC.

So `Remove` reports the dropped value's bytes and the two `RemoveStyle` callers
take them out of whichever ledger was holding them. The `nodeIsLive` flag those
callers already compute for `attrGCPair` is exactly the discriminator.

The general lesson: in this ledger a tombstone is not just a marker, it is the
receipt that lets a registration move a charge between two halves. Changing what
a tombstone weighs changes the arithmetic of every path that weighs it.

## Two tests were pinning the bug, not the fix

`TestBarrierRetentionIsBoundedByLagNotByArraySize` and
`TestBarrierCostsNothingOnConcurrentMoves` asserted that a fully collected
document costs exactly what it cost before the moves. That was true only because
the `movedAt` ticket was never charged. It is the right assertion for retention
and the wrong one for content: a moved element really does carry a ticket it did
not carry before, and a rebuild charges it.

Both now bound the residue at one ticket per moved element and additionally call
`assertRebuildsSame`, which is the invariant that cannot be satisfied by a
coincidence. A golden-number test that happens to encode a missing term reads as
a regression when the term is added; stating the invariant instead would have
made the fix land without touching them.

## Only half of item 2 is reachable from this repository

The issue offers two fixes for the server/SDK size disagreement and prefers the
SDK one. That one lives in `yorkie-js-sdk`. The Go half is implemented here and
the comments it reconciles are the SDK's, so the two repositories have to agree
on which half landed — noted in the PR rather than left to be discovered.

## Rounds

Self-review was not run: this was a one-shot autonomous run with no reviewer
subagent available. Verification is CI, `@claude review`, and a human.
