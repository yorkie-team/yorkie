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
