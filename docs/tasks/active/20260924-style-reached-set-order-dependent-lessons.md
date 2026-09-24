# Lessons: a style's reached node set depends on delivery order

- **Rebuild the reporter's measurement before changing anything.** The issue
  quoted 1001/135/0 and 7098/297/2879. Reconstructing the two scans and
  landing on exactly those numbers proved the harness matched theirs, which
  made every later number comparable — and turned "is this better?" from a
  judgement call into a diff of two sorted key lists.

- **Count the pairs that got worse, not just the total.** Going from 297 to
  126 divergences is not progress if 20 of the 126 are new. Dumping every
  diverging `(edit, style)` pair before and after and running `comm -13` was
  the check that mattered; the totals alone would have hidden a regression
  behind a bigger improvement.

- **A stable CRDT position answers what an index cannot.** Every predicate
  this area had accumulated tried to infer, from the current tree, which nodes
  the styling replica would have reached. After a concurrent split, "this End
  token was in range anyway" and "it is in range only because of the split"
  are the same index-space fact. The change's own range-end position separates
  them in one comparison, and it is the same on every replica by construction.

- **A token-type gate is a claim about tree shape.** The `InsNextID` closure
  was gated on `index.Start` because the node it was written for happened to
  arrive that way. A node fully covered by a range arrives on `Start`; one the
  range begins inside arrives on `End` alone — the very case the guard above
  it exists for. Gating on which token arrives, rather than on what the change
  meant, is how the two halves of one fix ended up disagreeing.

- **`RHT` already deduplicated, which made folding the two paths safe.**
  `Style` and `RemoveStyle` visited a fully covered element twice, once per
  token, and the second write lost LWW at the same ticket. That existing
  no-op is what let `styleTargets` return a deduplicated node list without
  changing any ledger arithmetic.

- **An "agree with yourself" scan is not a correctness scan.** Review round 1
  pointed out that `styleScanDivergences` compared the two delivery orders
  and nothing else, so an implementation that styled every node — or none —
  scored zero divergences. The scan now also carries absolute counts
  (`styledPairs`, `styledNodes`) and counts the pairs neither order could
  apply, which is the arm that used to drop pairs in silence. Both scans were
  already passing; the counts are what makes passing mean something.

- **A guard copied from the traversal can undo the fix.** The review asked
  for the range-order check `traverseInPosRange` applies to also cover
  `boundaryElements`. Gating on the *resolved* index order costs 108 more
  diverging pairs, because a range collapsed by a concurrent merge is exactly
  the case those elements exist to repair. The gate that works asks *why* the
  range is backwards: only a removal can collapse one, so an inversion with a
  live declared ancestry on both ends is the caller's own and gets nothing.
  Same for the skip predicate — splitting it into "did the change reach this
  node" and "may this re-anchored traversal touch it" let the boundary set
  answer the first without the second, which costs 150 pairs.

- **Measure each guard separately before keeping any of them.** Four review
  fixes went in together and the merge scan's tombstone count went 1292 →
  1550. Toggling them one at a time attributed 108 to one gate and 150 to
  another and cleared the other two; guessing which one was at fault would
  have been wrong, because the two cheap-looking ones were free.

- **Half a fix, measured, beats a whole fix, asserted.** The durable shape the
  issue asks for — the range resolved in a version-vector-filtered index space
  — has to ship with the JS SDK. What lands here closes the split family
  completely and halves the merge family, and the test carries the remaining
  count as a ratchet so the next attempt starts from a number rather than from
  a claim.
