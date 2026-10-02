# Lessons: a further split after concurrent same-boundary splits

- **The InsNextID chain is a split lineage, not a boundary.** Every piece
  cut off one element sits in one chain whatever offset it was cut at; a
  walk that means "the same boundary" has to find where that boundary ends.
- **"Holds children" is not "holds the right half".** The first JS version
  stopped at any non-empty sibling; review found that text typed into an
  empty product makes it non-empty too. The marker is a child the editor's
  version vector knows, and it has to be looked for at depth because element
  split products carry fresh tickets.
- **Two walks over the same chain can ask different questions.** A review
  round asked §7.5's empty-run test to use `holdsKnownChild` too, for
  symmetry with §7.8. It diverged a span-end typing raced by an Enter
  (`TestTreeSplitAfterTypingAtSpanEnd`), JS #1435 never made the change, and
  the GC barrier added with it was untested. Both were reverted. A replicated
  rule changes in Go and JS together, with a failing script, or not at all.

## Review round: GC barrier and merge stability

- **Reverting a bundle reverts the part that was right.** The §7.5 change
  was wrong and the split-chain GC barrier shipped with it was not; both
  went out together. The barrier is back on its own —
  `Tree.splitChainBarriersAt`, reported by `PurgeBarrierAt` alongside the
  sibling-walk ticket — while §7.5 keeps its raw `Children(true)` count, so
  `TestTreeSplitAfterTypingAtSpanEnd` stays green. The barrier covers §7.5
  too: both walks answer the same way for a chain node the editor already
  knows, whatever that node holds.
- **A GC barrier is local safety, not a replicated rule.** That is why it
  can land in Go ahead of JS where the §7.5 classifier could not: it only
  delays a purge, it never changes where a split lands.
- **`createdAt` does not say where a node has always lived.** A merge moves
  children keeping their original ticket, so "holds a child the editor knew"
  can become true for an empty same-boundary product after the fact. A
  `MergedAt` skip in `holdsKnownChild` was tried and taken back out: the
  field is stamped on a node's *first* merge-move only, `Split`/
  `SplitElement`/`DeepCopy` copy it onto products no merge relocated, and it
  arrives client-supplied with no `findMergeNode` resolution at that call
  site. It is also a replicated ordering rule with no reproducer, which is
  the rule from the round above. Recorded as a known limitation in
  `holdsKnownChild` and in `docs/design/concurrent-merge-split.md` instead.

## Review round: what the barrier actually stands for

- **A barrier leg needs a ticket that retires it.** The `InsNextID` leg was
  first justified by §7.4 empty-sibling re-parenting — but §7.4 is
  deliberately VV-independent, so no ticket retires it and the leg would
  have been claiming cover it could not give. The leg stands instead for
  `emptyRunReachesActor`'s *start* node, which is found among document
  siblings and so may carry no `InsPrevID`, and whose count the node's own
  `createdAt` does retire. §7.4 is written down as an uncovered pre-existing
  exposure rather than attributed to a leg.
- **Purge relinks the chain, so the tombstone's own membership matters too.**
  Barriering the ancestors left the case where the tombstone *is* the chain
  node: §7.8 breaks at a removed chain node, and purging it lets the walk
  run on to its `InsNext`. `splitChainBarriersAt` now reports the node's own
  `createdAt` and its `InsNext`'s — the pair that has to be covered before
  both replicas break at the same place.
- **Gate predicates deserve a unit test, not only an end-to-end one.** The
  end-to-end shape a local split produces only ever exercises the
  `InsPrevID` leg. `crdt.TestTreePurgeBarrierSplitChain` links each chain
  by hand and asserts the reported tickets, so every leg has a test.
