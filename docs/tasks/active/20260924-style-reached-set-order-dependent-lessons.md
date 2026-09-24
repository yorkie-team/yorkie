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

- **Half a fix, measured, beats a whole fix, asserted.** The durable shape the
  issue asks for — the range resolved in a version-vector-filtered index space
  — has to ship with the JS SDK. What lands here closes the split family
  completely and halves the merge family, and the test carries the remaining
  count as a ratchet so the next attempt starts from a number rather than from
  a claim.
