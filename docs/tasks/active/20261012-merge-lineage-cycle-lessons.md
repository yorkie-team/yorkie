**Created**: 2026-10-12

# Lessons: merge lineage cycle

## RED evidence

On main (685989bd) both test cases time out after 3s with "the merge closed
a parent cycle and the edit never returned". The goroutine keeps spinning in
`UpdateAncestorsLength` after the test fails, which is the server hang.

## Notes

- `IsAncestorOf` is false for the node itself, so the guard needs the
  `node == dest` check too. A lineage whose carrier is a direct child of the
  moved node makes the moved node its own destination.
- The forged state is built by setting `MergedFrom` and re-reading the
  tree with `NewTree`, the same path the converter takes for an element
  payload, as `TestRebuildMergeStateRejectsForgedSource` does.

## Self review

Round 1 (correctness/tests): nothing blocking. Applied: stronger assertions
(the skipped child keeps its parent, carries no MergedFrom, and the source
gets no mergedInto), clearer guard comment. Not changed here:

- Go and JS derive merge pointers differently from a forged lineage on a
  live source (JS refuses a live source since js-sdk#1406, Go does not).
  Pre-existing and separate from this fix; tracked as a follow-up issue.
- A skipped child stays live under a tombstoned parent. Honest merges reach
  the same shape already, so this adds no new state.

Round 2 (external review): the round-1 note above was wrong on its second
half. The source a skipped child stays under is tombstoned by Phase 5 of the
same edit, and a live node under a tombstone is not a shape an honest merge
leaves: when that source becomes collectable, `Tree.Purge` detaches the
subtree with `RemoveChild` but unregisters only the source itself, so every
node below it stays in `NodeMapByID` and stays charged to `docSize.Live` —
the detached-but-registered state behind #2008. Fixed by tombstoning the
skipped child in place, with its descendants, and booking a GC pair for each
(`tombstoneSubtree`), mirroring what `propagateMergeDeletes` does for a
merge-moved child whose source this edit deletes. The pair is what both
un-charges Live and gets the node purged. `mergeNodes` now returns those
pairs, and `Edit` appends them after `deletePairCount`, so they also count
against `SpansComplete` as every post-delete pair does.

Round 3 (blast radius): tombstoning in place broke an accounting invariant
round 2 did not look at. `Edit` computes `RemovedSize` by summing
`PaddedLength` over `toBeRemoveds` only, which is exact because every node
that drains a removed parent's aggregate is itself in that list. A stranded
child is not — the merge had collected it to *move*, so it lives in
`toBeMovedToFromParents` — yet `remove()` on it drains the removed source it
stays under, so its bytes left the sum without ever being counted. In the
regression shape `RemovedSize` came back 2 (b's padding) instead of 7 (all of
b). That number becomes `lastToIdx` for the undo/redo reconciliation loop in
`operations/tree_edit.go`, so stacked entries would be shifted against a
range short by a whole subtree. Fixed by having `mergeNodes` return the
stranded *nodes* rather than their pairs: `Edit` books a pair for each and
sums their `PaddedLength` alongside `toBeRemoveds`, on the same
post-every-phase timing. Counting each exactly once holds because
`tombstoneSubtree` takes the whole subtree (so every stranded node has had
its final say in every other node's aggregate) and skips anything already
carrying a `removedAt` (so the two sets are disjoint — Phase 5 runs first).
