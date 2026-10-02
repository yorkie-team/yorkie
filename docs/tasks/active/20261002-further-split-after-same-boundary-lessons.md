# Lessons: a further split after concurrent same-boundary splits

- **The InsNextID chain is a split lineage, not a boundary.** Every piece
  cut off one element sits in one chain whatever offset it was cut at; a
  walk that means "the same boundary" has to find where that boundary ends.
- **"Holds children" is not "holds the right half".** The first JS version
  stopped at any non-empty sibling; review found that text typed into an
  empty product makes it non-empty too. The marker is a child the editor's
  version vector knows, and it has to be looked for at depth because element
  split products carry fresh tickets.
- **A classifier that reads tombstones owes GC a barrier.** `holdsKnownChild`
  counts removed children so the answer does not depend on whether a replica
  has applied a concurrent removal yet — which makes a purge, not just the
  removal, able to change where a split lands. `Tree.PurgeBarrierAt` now
  reports the chain ancestors' `createdAt` alongside the sibling-walk ticket,
  so `PurgeBarrierAt` returns a ticket per rule rather than one ticket.
- **One run, one end test.** The §7.5 advance judged "empty" by direct-children
  count while §7.8 judged it by known descendants; both walk the same chain of
  same-boundary products, so both now ask `holdsKnownChild`.

## Review round: panel findings

- **A barrier read off a mutable field has to be read off the right one.**
  The first barrier gated on `InsNextID != nil`, which a right-half product
  does not carry until it is itself split — so the tombstone inside it was
  purgeable right up to the moment the chain appeared, and the barrier was
  lost retroactively. `TestTreeSplitChainGCBarrier` reproduces the resulting
  divergence under the old gate. `InsPrevID != nil` is the correct read: it
  is exactly "reachable as some node's `InsNext`", which is exactly the set
  of nodes the two walks classify, and `Purge` maintains it across the node
  it unlinks instead of leaving it stale.
- **Enumerating "the rules" in a contract invites being wrong about it.**
  The `GCBarrier` doc claimed Tree had exactly two rules reading its linked
  nodes; §7.4 re-parenting is a third, and being version-vector-independent
  it has no ticket that retires it. The contract now says the set is open and
  that a VV-independent rule cannot be represented, and §7.4's residual
  exposure (pre-existing) is written down in the design doc.
- **A GC barrier that no test collects under is not tested.** The whole
  deferral half shipped with tests that never called `GarbageCollect`.
  `TestTreeSplitChainGCBarrier` now pins deferral, drain, and a control case
  proving the same min vector purges when no split intervenes — so the
  deferral assertion is about the barrier and not about the vector.
- **A changed classifier is a change to every caller of it.**
  `emptyRunReachesActor` also drives Style/RemoveStyle range resolution;
  `TestTreeSameBoundaryStyleAfterPeerTypedIn` covers those two consumers in
  the shape where old and new "empty" differ.
