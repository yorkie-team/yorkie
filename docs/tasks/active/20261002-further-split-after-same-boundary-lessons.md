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
