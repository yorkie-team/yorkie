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

