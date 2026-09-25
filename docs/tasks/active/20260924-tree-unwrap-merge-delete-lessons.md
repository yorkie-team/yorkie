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

## Review round 1 (panel: blast-radius, correctness)

- **"The position named it" has to mean the ancestry, not the node.** The
  first cut compared the merge source against `ToTreeNodes(from/to)` -- the
  parent each position declared, and nothing above it. An edit that merges at
  more than one level names only its innermost boundary that way, so the
  enclosing element -- equally intentional, equally not-covered by the range
  -- lost the skip and had its merge-moved children tombstoned.
  `declaredBoundaries` now walks up from each declared parent, preferring
  `MergedFrom` over `Index.Parent` because a prior merge has already moved the
  node out from under the element the position was declared inside.

- **A declared position is client input; resolve it exactly.** `ToTreeNodes`
  goes through `findFloorNode`, which matches on `CreatedAt` alone. A
  `ParentID` naming an element-split product this replica does not hold
  floor-resolves to the offset-0 element, and the skip then fires on a node
  the position never named. `findMergeNode` -- exact ID, element only -- is
  the guard the rest of the file already uses for exactly this reason
  (`resolveMergeTarget`, `mergeNodes`), and it belongs here too.

- **Tombstoning outside `toBeRemoveds` is tombstoning outside the undo.**
  Merge propagation reaches nodes Phase 5 never collected, so nothing it
  killed appeared in `Removed`/`PreTombstoned` and the copy-reinsert reverse
  -- which is the reverse a caller must build here, since propagation always
  clears `SpansComplete` -- could not restore them. It reports both sets now.
  `RemovedSize` deliberately does not grow: it is one contiguous pre-edit
  range, and the propagated nodes are outside it by construction.

- **A test that asserts data loss blesses it.** The same-unwrap subtest used
  to assert `<r>ab</r>` -- the untouched second paragraph destroyed by a
  separate §1.1 problem. Unwrapping the LAST paragraph instead exercises the
  identical skip (verified: with the skip disabled the replicas split into
  `<r><p>ab</p></r>` vs `<r><p>ab</p>cd</r>`) and destroys nothing. The loss
  moved to the todo's known limitations rather than into an assertion.

- **Assert the limitation you cannot fix.** Silently omitting the ID-level
  comparison on the reported case read as "it converges". It asserts the
  live-node shape (which does converge) and pins the full-shape inequality
  (which does not), so the known tombstone-ordering limitation is visible in
  the test and fails loudly the day it is fixed.

- **Regression net, re-run.** The exhaustive two-replica matrix is unchanged
  by this round: 224/2025 XML-divergent pairs on the flat fixture, 1229/8281
  on the two-level nested one and 3872/23409 on a three-level one -- byte for
  byte what the pre-round branch produced, and still better than `main` (242
  and 1328 on the first two). So the generalizations cost nothing measurable
  on these shapes; they close reasoning gaps the fixtures do not reach.

## Panel round (2026-09-24, after "Key the merge-delete skip…")

- **A one-sided defect fix is a divergence, even when nothing on the wire
  changes.** Feeding §6.2's propagated tombstones into
  `TreeEditReverseInfo.Removed`/`PreTombstoned` made Go's undo restore content
  JS's undo drops. `undo-redo-go-port.md` (Goals; Key Decision "Port JS's known
  defects as-is") rules that out: parity is the point, and a Go-only fix widens
  the gap the port exists to close. The plumbing was reverted; the gap is
  recorded as a known limitation to fix in both ports at once. The previous
  entry in this file argued for the opposite and was wrong.

- **Widening a field's population breaks consumers written for the narrow
  one.** `Removed` was one contiguous pre-edit range, and the operations layer
  reads it that way -- `toReverseOperation` anchors at `Removed[0]`,
  `topLevelRemoved` filters by parent membership in the same set. Appending
  out-of-range propagated nodes kept the type and the name while changing the
  contract, so nothing failed to compile and nothing failed at review-time
  reading either. If a field's doc comment has to describe two populations,
  that is the signal to add a field, not to append.

- **Pinning a divergence is not the same as owning it.** The reported case's
  `assert.NotEqual` said the limitation exists; it did not say who fixes it or
  when. It now points at `20260924-merge-moved-child-order-todo.md`, a task in
  the same class as `20260923-same-boundary-split-order` (arrival-ordered
  placement, Go fix gated on a JS mirror), with the consequence spelled out --
  a third replica's insert anchored to the hoisted child, and snapshots taking
  whichever arrival order reached the server.
