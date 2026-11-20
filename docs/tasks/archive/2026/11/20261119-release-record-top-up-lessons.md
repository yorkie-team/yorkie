# Lessons: keep a release record zero when nothing will collect the charge

## The filed location was one caller off

The issue pointed at `moveSizeToGC` and named a guard
(`ChargeToRemovedElement`) that does not exist in `main` — the function it
quotes is `AccMovedElement`, from #2037, and it carries no guard at all. The
described defect is real, but it belongs to that function, not to
`moveSizeToGC`.

What separates the two is not the top-up, it is what stands behind it. A
top-up is only residue when no collection entry covers the element afterwards.
`moveSizeToGC`'s three callers all register one (or are covered by an
ancestor's `Descendants` walk in `deregisterElement`); `AccMovedElement`
registers nothing, because a move is not a removal. Reading "tops up a zero
record" as the defect would have produced a change that broke the balanced
path and left the unbalanced one alone.

Generalisation worth keeping: in this accounting code the question is never
"does this write a charge?" but "who subtracts it, and can they still reach
it?". `release` is the one operation that answers *nobody* — and that is
exactly what the zero record encodes.

## The test had to fail for the right number

The regression is 24 bytes — one ticket — in `docSize.GC`, against a root
whose rebuild has none. Nothing exported shows it: `GarbageLen` is zero either
way, because the residue has no pair, and the document marshals identically.
`assertRebuildsSame` is what sees it, which is why the existing fixture was
the right place to put the test rather than a new one at the `json`/`document`
layer.

Confirmed by reverting only `root.go` and re-running: before the guard,
`GC.Meta` is 24 and the rebuild's is 0.

## A release record is not a tombstone's alone

The first guard sat after `AccMovedElement`'s `RemovedAt() == nil` shortcut, so
it only ever saw released tombstones. But `UnregisterRemovedElementPair`
releases the pair element *and every descendant*, and most of an orphaned
subtree is live: `release` subtracts those elements' bytes from `Live` and
writes them a zero record all the same. A move applied to one of them took the
unguarded branch and added the movedAt ticket back to `Live`, which is no
longer holding the element — the same unsubtractable residue, on the commoner
of the two paths. Reading the record before `removedAt`, not after, is what
covers both.

The corollary for this file: `sizeInGC` membership, not `removedAt`, is what
says which ledger holds an element. `removedAt` only distinguishes the two
*charged* states once membership has already answered that there is a charge.

A neighbouring gap this change does not close: an element a peer *adds* into an
already-released orphan is charged to `Live` by the add itself, and nothing
collects the orphan to take it back out. That charge is raised on the operation
path, not in `AccMovedElement`, so it is a separate fix.

## Round log

- **Verification**: `make lint` (0 issues), `make verify` green,
  `go test ./pkg/document/... -count=1` green. `/self-review` was not run —
  this was an autonomous one-shot run with no reviewer subagent available, so
  review is CI plus the PR's human reviewers.
- **Round 2 (review feedback)**: the guard was moved ahead of the
  `RemovedAt() == nil` shortcut so it also covers live members of a released
  subtree, with `TestMoveOfLiveReleasedElementChargesNeither` pinning it
  (24 bytes of `Live` residue before the reorder, confirmed by reverting only
  `root.go`). Two assertions in the existing move test were tightened: a map
  index on a missing key also yields the zero `DataSize`, so each now has a
  `require.Contains` in front of it.
