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
