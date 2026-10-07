# Lessons: Stop the split-sibling cascade at a sibling the editor saw alive

**Created**: 2026-10-06

- A walk that skips nodes and keeps going is only safe if nothing it reaches
  later depends on what it skipped. The `InsNextID` chain is in document
  order, so a sibling the editor saw alive is a boundary: what lies past it
  came out of that sibling, not out of the deleted element.
- "Known" is not "seen alive". The first version stopped at every known
  sibling and lost deletes when the editor had merged that sibling back into
  the element. The test is whether the editor saw the sibling removed, which
  the tombstone's ticket answers, or whether this delete covers it whole.
- The obvious other step, cascading also when the delete lost the element's
  LWW, made every #1408 shape converge and still lost text: an undone delete
  or a merge that turned into a delete reaches products whose content the
  editor never saw. Tests that only assert convergence would have accepted
  it; tests that assert nobody's text disappears caught it.
- Hand-picked races miss shapes. A random fuzz over split, Enter, merge,
  insert and delete on 2–3 replicas, compared seed by seed with `main`,
  showed both the gain and the few seeds that got worse.

## Self Review

- Round 1 (correctness, tests): an independent subagent tried to break the
  first version (stop at the first known sibling). Blocking: it lost deletes
  when the known sibling had been merged back into the deleted element
  (`main` converged); the residue was described wrongly in the doc and the
  tests; the "Enter + type vs Enter" case asserted one replica only; a JS
  test file failed lint. Non-blocking: the "its own cascade reaches it" claim
  fails when that cascade loses the LWW (a new empty-element residue); the
  editor-actor check is redundant; the swap did not exercise both ticket
  orders in symmetric cases; the residue check accepted any number of empty
  spans. All fixed here: the stop rule became "saw it gone or enclosed", which
  also covers the LWW-lost sibling; tests gained the reviewer's shapes, both
  actor orders, an exact one-empty-span residue check and GC on that path;
  the docs describe the residue as observed. Two shapes stay as known
  limitations above, with their reason.
- Round 2 (design fit, docs): no blocking findings. It confirmed the walk
  tombstones a subset of what `main` did for any tree state, the lazy
  `enclosed` traversal uses the outer range and is read-only, snapshots keep
  every field the rule reads (3000 seeds with undo, 0 mismatches), and Go and
  JS match. A 10,000-seed fuzz per setting (plain, undo, snapshot reload,
  both) found no seed where this change loses a letter that `main` kept
  without `main` already losing text. Fixed here: the residue count (12 of
  40, not 6, and not only same-boundary splits), the cost of the LWW gate
  (deleted text can survive with three replicas), both limitations' wording
  (Enter + Undo + delete against Enter; the merge-vs-split order count) and
  the version the cascade shipped in (v0.7.4). Out of scope, noted for a
  separate issue: Go's Phase 3 range narrowing lacks the `toLeft != toParent`
  guard that JS has (#1237 in yorkie-js-sdk).
- Round 3 (the two remaining escapes, measured rather than argued): dropping
  `seenGone` and stopping at every known sibling makes the rule a pure
  function of creation tickets and the range, but all four runs of "merge the
  split sibling back and delete, against splitting it" then diverge with "d"
  live on one replica only. Dropping the `canDelete` gate so the cascade also
  runs on a lost LWW fails all five `TestTreeSplitSiblingCascadeKeepsText`
  cases. Both escapes are therefore closed with the single-slot `removedAt`
  the data model keeps today; the two limitations above stand. Fixed here: the
  lazy `enclosed` memo no longer swallows its traversal error, which would
  have left an empty memo and a silently short cascade.
- Round 4 (what else writes the field the rule reads): two paths mutate a
  node's `removedAt` outside the walk. `Tree.Purge` is benign and provably
  so — `findFloorNode` compares `createdAt` exactly, so a purged ID is nil
  rather than a stale read; `Purge` relinks the predecessor onto the same
  successor the walk would have reached by passing the tombstone; and
  `Root.collect` purges only once the minimum synced version vector covers
  `removedAt`, the point at which the read answers true anyway. `Restore`
  is not: `unremove` clears the tombstone in place, so a delete concurrent
  with an undo cascades differently per replica. Both written down in §4.1
  and above. A field read for a causal decision needs its writers
  enumerated, not just its readers.
- Round 5 (making the residue visible): the residue is divergence in shape as
  well as XML — the product is alive on one replica
  (`span#3:2:…:0[]`) and tombstoned on the other (`…:0x[]`), the latter from
  `split.removedAt = n.removedAt` in `SplitElement`. A table flag that accepts
  divergence reads as a passing test; a skipped test that asserts the
  convergence the rule does not reach reads as the open problem it is.
  `TestTreeSplitSiblingCascadeResidueConverges` added for that, skipped.
  Un-tombstoning the born-tombstoned product was considered and rejected: the
  inheritance is what keeps the cascade convergent for products the deleter
  did not know, so flipping it diverges the ordinary cascade instead.
- Round 6 (external review): the chain walk gained the text-node guard the
  other chain walks carry — `Node.Split` links products of the same kind, so
  an element chain holds elements and it never fires, but without it a text
  node would end the walk by accident, since `enclosed` records element
  Start tokens only and answers false for every text node. The docs gained
  what a behaviour reversal owes the reader: a Key Design Decisions row, a
  Fix 28 entry, a cross-implementation note (unlike §9.6 this is not a
  strict narrowing of an already-divergent set), a caveat on the two
  coverage tables that predate the rule, and a paragraph saying plainly
  that the convergence-for-text trade is a maintainer judgement this
  document cannot settle. The "Enter + type vs Enter" residue was measured
  against `main` rather than argued: `main` diverges on the same shape and
  loses "de" on top of "y" (`<p><span>abc</span></p>…` against
  `…<span>y</span><span>de</span>…`), so it predates the rule;
  `TestTreeSplitProductBornTombstonedConverges` is the skipped reproducer
  for the whole of it.

