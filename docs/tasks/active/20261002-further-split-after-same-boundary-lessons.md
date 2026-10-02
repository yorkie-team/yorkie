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
  can become true for an empty same-boundary product after the fact.
  `holdsKnownChild` now skips a child whose `MergedAt` the version vector
  does not cover. No dedicated reproducer yet — the scenario needs a merge
  racing two same-boundary splits across three replicas.
