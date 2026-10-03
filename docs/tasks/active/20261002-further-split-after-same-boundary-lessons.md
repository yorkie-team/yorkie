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

## Review rounds: the GC barrier, and why it came back out

- **`createdAt` does not say where a node has always lived.** A merge moves
  children keeping their original ticket, so "holds a child the editor knew"
  can become true for an empty same-boundary product after the fact. A
  `MergedAt` skip in `holdsKnownChild` was tried and taken back out: the
  field is stamped on a node's *first* merge-move only, `Split`/
  `SplitElement`/`DeepCopy` copy it onto products no merge relocated, and it
  arrives client-supplied with no `findMergeNode` resolution at that call
  site. It is also a replicated ordering rule with no reproducer. Recorded
  as a known limitation in `holdsKnownChild` and in
  `docs/design/concurrent-merge-split.md` instead.
- **A local purge barrier cannot make a tombstone-reading rule GC-safe.**
  Five fix rounds grew a split-chain barrier (ancestors, the tombstone's own
  chain node, `InsNext` successors) and every review found the next hole:
  a chain created after the purge, a successor displaced by a later split,
  `emptyRunReachesActor`'s actor-ID branch. They are one hole. A split
  applied after the purge can carry a tombstone one replica already
  collected into a product nothing named at purge time, so no ticket
  reported then can stand for it. Removing the barrier also removed the
  `GCBarrier` signature change and the per-pass ancestor walk.
- **Ask which way the purge moves the answer.** Unlinking a tombstone only
  takes a known child away, so it can only clear the marker, and a cleared
  marker sends the walk on past the right half — the walk every replica ran
  before this change. That only matters when there is a sibling to walk on
  to, and then the old walk already diverged. So GC can withhold the fix on
  a collecting replica; it cannot break an edit set that converged before.
- **Skipping tombstones the editor saw removed is not the fix either.** It
  makes GC irrelevant — a purge only unlinks a node whose removal every
  later editor knows — but it broke convergence without GC: a three-replica
  split/insert/delete fuzz went from 1164 to 1174 diverging seeds of 3000,
  twelve of them new. A removal the editor knew still marks where the right
  half was.
- **Measure against `main` before arguing.** A GC-differential fuzz (three
  editing replicas without GC; two observers fed the same change log in
  order, one collecting with the min version vector after each sync)
  showed post-GC divergence on `main` already. Over 4000 split/delete
  scripts the branch without a barrier diverged after GC on the same seeds
  as `main` (15 flat, 13 nested), bar one seed whose minimised script
  diverges on `main` without GC at all. The barrier lowered the count but
  added seeds `main` handled, so it was not a strict improvement either.
