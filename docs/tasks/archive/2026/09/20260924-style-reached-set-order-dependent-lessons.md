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

- **The panel round: "narrowed" was not an answer, and the remaining 126 had
  one shape.** The review panel rejected the merge family being carried as a
  ratchet rather than closed. Dumping the 126 remaining diverging pairs took
  one scratch test and showed a single family: the merge deletes the opening
  tag of the paragraph the style range *starts* inside, the children move into
  the paragraph before it, and the range start resolves inside that paragraph —
  whose End token is then in the range only because of the merge. §9.1 already
  asked that question on the range-END side. The mirror (§9.6, `beginsInside`)
  closed all 126, and with them the §9.4 known limitation filed as the
  `RemoveStyle(6,8)` / `Edit(1,5)` PBT counterexample. A ratchet is a place to
  look, not a place to stop: the shape was visible the moment the pairs were
  printed instead of counted.

- **The split-lineage match is directional, and the two sides need different
  directions.** Reusing `endsInside` verbatim for the range-start side cost 45
  rendered divergences on the *split* scan: it matches a declared ancestor
  only as the ORIGIN of a split, and a split of the element the range began
  inside leaves the start anchor in the right half — a product, not the
  origin. `beginsInside` accepts both directions; `endsInside` must not,
  because it excludes nodes and extra matches make it fail open. Two
  predicates that ask "was this position declared inside this node" still
  differ in which way the lineage may run.

- **The tombstone count did not move, and that is the evidence.** §9.6 took
  rendered 126 → 0 while tombstone-only stayed at exactly 1292 — the same
  1292 keys, not a coincidental total. Dumping keys rather than counts is what
  made "no pair that converged before diverges after" checkable again, and a
  300-seed randomised sweep returned the identical seed list before and after.

## Review panel round 3

- **A golden behind a path filter is a golden nothing checks.** The panel's
  blocking finding was not that the `test/complex` merge × style goldens were
  wrong — replayed without a server they all still hold under §9.6 — but that
  nothing in the pipeline would have said so: the lane is gated on both the
  `complex` build tag and a `test/complex/**` path filter, and this change
  touches only `pkg/document/crdt`. The unit mirror existed and covered 6 of
  the cases; it now covers all 13, including the from-side ones §9.6 is
  about. For the `RemoveStyle` cases the mirror reads the attribute entries
  off the CRDT the way the complex suite does, because Marshal shows neither
  an empty container nor a removal tombstone — the exact state #1942 reports
  diverging. Coverage that only runs when its own file changes is coverage of
  the file, not of the behaviour.

- **A port to another repository is specified, not promised.** The second
  finding — that the reached-set rules land Go-only while the issue asks for
  server and JS SDK together — cannot be closed here: `yorkie-js-sdk` is a
  separate repository. What was missing was not the code but a spec: the four
  rules are now written up without Go identifiers, each with the invariant
  that makes it decidable on any replica, plus the acceptance vectors a port
  has to reproduce (the 13 mirror cases and the scan counts). "Tracked as a
  follow-up" is a note; a rule statement plus a test vector is a task.

## Review panel round 4

- **A cross-repository requirement is a standstill, not a work item.** The
  panel re-raised "must land on the server and the JS SDK together" as
  blocking, on both lenses. Nothing in this repository can satisfy it:
  `yorkie-js-sdk` is a separate repository, and any Go-side change to the
  reached set — including the empty one of not landing — either diverges from
  unported JS clients or keeps two Go replicas diverging from each other by
  delivery order, which is the worse of the two. The round produced a
  structured rebuttal rather than a code change, so the standstill is on the
  record and reaches a human instead of being re-litigated each round.

- **Read what the quoted sentence is attached to.** The issue's "has to land
  on the server and the JS SDK together" qualifies *the durable shape* it
  proposes — the range resolved in a version-vector-filtered index space,
  which "touches the traversal contract shared by `Style`, `RemoveStyle` and
  tree position resolution". This PR deliberately does not take that approach
  and records it as the open limitation in §9.5. The same sentence, lifted
  off its antecedent, then reads as a gate on the approach that *was* taken.
  Quoting an issue is not the same as scoping the quote.

- **The repository's own ledger is the precedent.** Fix 11 (§9.1), Fix 12
  (§9.2), Fix 21 (§9.3), Fix 22 and Fix 23 (§9.4) each changed which nodes a
  `Tree.Style` writes to and each landed Go-only, through this same design
  doc, in #1909, #1928 and #1954. Fix 10 is recorded in the same table as
  "JS-only". The ledger already models the two implementations landing
  separately; a rule that forbids it would have blocked five of the twenty-four
  fixes above it.

- **Put the contract where the code is, not only where the doc is.** The
  port spec was discoverable only to someone already reading
  `docs/design/concurrent-merge-split.md`. `styleTargets` now carries a
  `NOTE(cross-implementation)` saying its return value is a replicated
  contract and pointing at that section, so the next editor of the reached set
  learns it from the function they are editing.
