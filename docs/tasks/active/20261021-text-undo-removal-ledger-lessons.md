**Created**: 2026-10-21

# Lessons: Keep a Text removal the undo never saw

- The reproduction needs the protobuf round trip: an in-process exchange
  keeps `RemovedBy` on the operation objects even when the converter drops
  it.
- Once an undo can revive a tombstone, "the deleter knew the node" is not
  the rule for a remote deletion any more; it has to be "the node was live in
  the deleter's view", or a deletion removes text its author never saw.
- A server does not pass unknown fields through. It decodes the pack and
  encodes it again, so a new field that changes what an operation does needs
  a capability, the same as `presence-patch`.
- (Superseded) Recreating a purged run as one node made the collected
  replica's node partition differ from an uncollected one, so #2270
  recreated along the purged pieces' boundaries. A piece is now purged only
  once a single stable removal holds it, and the recreated piece is linked
  to the nearest surviving pieces of its insertion, so the partition no
  longer decides anything.
- Hand-written scenarios found the two first divergences; the rest came
  only from a randomized run with GC on, minimized to a few operations. Keep
  such a harness next to a CRDT change, not just the scenarios.
- "Observable" and "changed state" part ways once state can change without
  anything showing. A gate on the first drops changes the others need.
- What GC may forget is decided by what a later change can still ask. A
  piece is safe to purge only when every change that can arrive already
  knew its whole ledger, not just its latest removal.
- Before adding a wire field, check whether the receiver can derive it. An
  undo always undoes its own actor's removal, which made `removed_by`, its
  capability and the cancel pairing in the snapshot unnecessary.
- A recreated piece needs its insertion links as much as its place: linked
  only to the pieces covering the adjacent offsets, it was skipped once the
  piece before it was purged, and the next edit ending after it failed.
- A wrong anchor for recreated text is not only an ordering bug: a later
  range deletion between the misplaced text and its neighbour runs to the
  end of the text.
- A ledger needs its own GC, not only its node's: cancelled pairs that every
  replica knows decide nothing, yet without pruning they grew each revived
  piece and each snapshot for good.

## Self review

- Round 1 (correctness, tests): no blocking finding. Fixed: no CI test for
  the recreated stand-in or for span order (added both, each fails without
  its fix), a redundant stability loop in `HeldFromPurge`, a decoder that
  accepted a repeated ticket, dead `SetLedger`, a stale anchor comment.
  Kept as a known limitation: a tombstone held by two concurrent deletions
  is never collected, also without undo.
- Round 2 (design fit): blocking: ledgers of live pieces grew without bound
  (fixed by pruning stable pairs at GC). Fixed: the GC hold is a named
  `GCHold` in `gc.go`; a first removal no longer builds a ledger; a snapshot
  with bad events loads with `removed_at` alone instead of failing; the
  stand-in and the frozen pairing rule are written into the proto comment
  and the design doc; rollout order is in the design doc; commit order and
  one overstated commit message. Left for the PR body: JS #1494 is
  wire-incompatible until reworked; squash-merge, since #2270's commits
  describe fields this branch removes. Declined: splitting the Ceiling fix
  into its own PR, since it is a commit here and can be cherry-picked if
  the ledger work stalls.
- Round 3 (security, docs): no blocking finding. Fixed: a client that
  leaves its own removal out of a change's vector could stack removals by
  one actor, holding tombstones forever and costing quadratic work; a
  removal is now not recorded while the same actor holds an uncancelled one.
  Documented that a forged `executed_at` can still cancel another actor's
  removal (#2114; main is looser). Docs wording left over from `removed_by`.
  A 4-replica fuzz setting run afterwards found a recreated piece left with
  a stale insertion link (on main, in JS and at #2270 too); fixed in the
  anchor commit with a regression test.
