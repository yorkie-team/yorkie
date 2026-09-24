# Lessons: unwrap vs. merge-delete of the same paragraph

- **Ask which boundary the edit *named*, not which one it resolved to.**
  §1.1 redirects a position away from a merged-away parent, so by Phase 6
  `fromParent`/`toParent` no longer say what the remote client asked for.
  Every attempt to tell "this edit re-runs the concurrent merge" from "this
  edit deletes the merged-away node" out of the resolved range failed:
  full containment, `ended`, and the merge destination all read the same in
  both cases, because the redirect is what widens the range over the source.
  `ToTreeNodes(from)` / `ToTreeNodes(to)` read the declared position and
  separate them in one comparison.

- **An exhaustive two-replica matrix is the cheap regression net.** Every
  pair of range deletes over a four-token-per-paragraph fixture is 45x45
  cases and runs in under a second on the serverless
  `exchangeInOrder` harness. Two candidate fixes looked right on the
  reported case and broke 29 and 9 other pairs respectively; the accepted
  one fixes 12 pairs (78 on the nested fixture) and breaks none. The matrix
  also shows how much divergence is still there: 224 of 2025 ordered pairs
  disagree on XML after this fix, so the single reported case is a corner of
  a much larger surface.

- **Convergence of the visible tree is not convergence of the tree.** The
  replicas here agree on XML and `Marshal()` but order the hoisted children
  differently, because `mergeNodes` appends them to the end of the
  destination in arrival order. Same class as the split-product ordering of
  #2030: invisible until a later position-based operation lands on it.
