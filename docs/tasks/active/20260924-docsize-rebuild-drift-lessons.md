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

## A document-level test that looked like it covered the GC branch did not

The first attempt to cover "the attribute was stripped from a node that is
already a tombstone" built the obvious shape: two replicas, one deletes the
styled paragraph, the other strips the attribute, exchange. It passed — and it
still passed with the GC compensation deleted, which is how the gap showed.
Instrumenting the branch showed why: a `Tree`'s `traverseInPosRange` never
reaches a node the replica has already tombstoned, so only the replica that had
*not* deleted it ran the removal, and there the node was live.

`Text` is different — `findBetween` walks tombstones, and the comment on
`Text.RemoveStyle` says so. The branch is pinned there instead, at the CRDT
layer, with a range resolved before the delete so it still addresses the node
after it.

The lesson is procedural: a new test that passes is not evidence until it has
been run against the code without the fix. Both tests here were, and one of
them had to be rewritten as a result.

## Only half of item 2 is reachable from this repository

The issue offers two fixes for the server/SDK size disagreement and prefers the
SDK one. That one lives in `yorkie-js-sdk`. The Go half is implemented here and
the comments it reconciles are the SDK's, so the two repositories have to agree
on which half landed — noted in the PR rather than left to be discovered.

## Rounds

Self-review was not run: this was a one-shot autonomous run with no reviewer
subagent available. Verification is CI, `@claude review`, and a human.

## Panel round 1: the ledger a diff lands in is a second decision

Two of the three blocking findings were the same mistake in different places:
the change computed *how much* a write costs and then assumed *where* it goes.

`Move.Execute` charged the movedAt ticket to Live unconditionally, but a move
applied to an element another replica has already deleted is ordinary — RGA
stamps movedAt either way — and a rebuild charges a tombstone's whole DataSize
to GC. Routing it needs more than picking `AccGC`: `deregisterElement`
subtracts what `sizeInGC` recorded for that element, so a GC top-up that leaves
the record alone only moves the stray bytes from Live to GC. `Root.AccMovedElement`
raises the record with the charge; without it the test still failed, one ledger
over.

The decode boundary was the same shape read backwards. `Remove` minting a
valueless tombstone makes a tombstone's size a function of its key, but only
for tombstones this code minted; `SetInternal` restored whatever the wire
carried, so a pre-upgrade snapshot rebuilt a heavier tombstone than the replica
that performed the removal. Enforcing it in `SetInternal` covers both decoders
and `DeepCopy` at one choke point, which is the only place all three meet.

The reproduction rule from the round before held again: both new tests were run
against the code with the fix removed, and both failed with exactly the drift
the finding described (24 bytes stranded in Live after collection; a heavier GC
charge on the legacy payload).

## Reported, not fixed: the size limit has no server-side gate

The security lens is right that `MaxSizePerDocument` is enforced only in
`Document.Update` on the client, and that the server's push path never
re-checks it. It is also not something this change introduced or could close
here: `pushPack` stores changes without materializing a root, so a real gate
means building the document on the push path and deciding what a rejected push
does to a client that already applied it locally — a design question, not an
accounting one. Rebutted on scope with the note that the finding itself stands.
