# Collection changes where a later insert lands

**Created**: 2026-09-12

Permanent replica divergence, and separately hard apply failures, caused by
garbage collection. Pre-existing on `main`; not introduced by the containment
release (`f2533292`). Present in **all three** RGA-shaped structures — `Array`,
`Text` and `Tree`.

Two fixes were attempted and both were refuted by measurement. This filing
records what they established so a third attempt does not repeat them.

## Problem

RGA decides where an insert lands by walking forward from its anchor, skipping
nodes whose positioning ticket is newer than the incoming operation. The walk
reads the nodes **currently linked into the list, tombstones included**:

    pkg/document/crdt/rga_tree_list.go:526   node.next.PositionedAt().After(executedAt)   (Array)
    pkg/document/crdt/rga_tree_split.go:437  node.next.createdAt().After(updatedAt)       (Text)
    pkg/document/crdt/tree.go:2998           !next.id.CreatedAt.After(editedAt)           (Tree)

Collection unlinks exactly those nodes (`RGATreeList.release`, and the
equivalents). So **collection mutates the input to the insertion rule**, and the
insertion point stops being a function of state the replicas agree on — which is
what RGA convergence requires.

Two replicas that differ *only* in whether collection has run therefore order the
same later insert differently, and nothing reconverges them.

The general statement: **tombstones are load-bearing for the forward skip.** A
position slot abandoned by `MoveAfter` is simply another species of tombstone
that inherits this; the defect is not move-specific and a plain `Delete`
reproduces it.

## Measurements

A fuzz harness with a push/pull model faithful to `pushpull.go` — each client's
row stored from its request's version vector at push time, own changes filtered
out of the pull, `minVV` the element-wise minimum over rows, and collection run
only by `Document.ApplyChangePack` with the vector that pull delivered. No
`helper.MaxVersionVector` anywhere.

On `main`:

| workload | result |
|---|---|
| 300 seeds, all operations, collection **on** | 43 diverged or errored |
| 300 seeds, all operations, collection **off** | 0 — converges |
| 1000 seeds, insert + delete only | 4 diverged, 75 `ErrChildNotFound` |
| 1000 seeds, all operations | 109 diverged, 26 `ErrChildNotFound` |

The collection-off control is the load-bearing one: the same seeds converge when
nothing is collected, so collection is the sole cause.

Note the second failure mode. Beyond divergence, the walk can be handed an
anchor that collection removed, and the operation then fails to apply —
`ErrChildNotFound` in the array, "offset should be less than or equal to length"
in text. An operation that cannot apply is worse than one that applies
differently: the server rebuilds documents and snapshots by replaying the change
log, so a stored change that fails to apply makes the document unloadable.

## Attempt 1 — refuted

Barrier on the node being purged: an element a move rehomed may not be purged on
its `removedAt` alone. Guarded the `gcElementPairMap` loop in
`Root.GarbageCollect` and left the `gcNodePairMap` loop, on the argument that a
move's gate (the move ticket) is always causally after its barrier (the
element's original insert ticket).

**True only for the first move.** `MoveAfter` abandons the node the *first* move
created, so a second move's dead slot has barrier `m1` and gate `m2`. Concurrent
moves by different actors leave those unordered, and a legitimate `minVV` can
cover `m2` while `m1` is in flight. Refuted independently by two reviewers with
the same counterexample.

## Attempt 2 — a real improvement, still not a fix

Reframed the barrier as a property of the **successor**, not of the node being
purged:

> A purge may unlink a node only if the node that would become the forward
> skip's new stopping point is itself causally stable.

This is the better framing — it needs no case analysis about what a particular
slot's barrier is, which is exactly what made attempt 1 wrong, and one rule
covers both purge loops and all three structures.

Measured against `main` on the same fuzz:

| | main | attempt 2 |
|---|---|---|
| 300 seeds, collection on | 43 | **33** |
| 1000 seeds, insert+delete: diverged | 4 | **0** |
| 1000 seeds, insert+delete: `ErrChildNotFound` | 75 | **40** |
| 1000 seeds, all ops: diverged | 109 | **84** |
| 1000 seeds, all ops: `ErrChildNotFound` | 26 | **18** |

Better in every category, and it eliminates insert/delete divergence entirely.
Retention cost measured at +5.2% mean collected pairs on a move-heavy workload
and byte-identical to `main` on ordinary ones; it drains within one sync round
and is charged to `DocSize.Total`, so `MaxSizeLimit` accounts for it.

**It does not close the bug.** Purging a tombstone destroys two things and the
successor barrier addresses one:

1. the skip's stopping point — closed by attempt 2;
2. the **anchor** that an operation concurrent with the removal still
   references — untouched, because the successor can be perfectly stable while
   an in-flight operation points at the node being removed.

## Tasks

- [ ] Decide whether attempt 2 ships on its own. It is strictly better in every
      measured category and costs little, but it is a partial fix for a
      convergence bug, and both mechanisms share a root — a complete fix may
      have to undo its shape. The artifacts are preserved (see below)
- [ ] Close the anchor mechanism, or establish that it is unreachable through
      the real push/pull path. Attempt 2's author argued unreachability from
      reading `pushpull.go` and `document.go` and was explicit that it was an
      argument and not a measurement; the fuzz above reaches it
- [ ] Decide about `findNextBeforeExecutedAt` itself. Making the skip read
      nothing collection can remove means each node carrying its own anchor id
      and a comparison over anchors — the YATA/Yjs shape. That is a permanent
      per-node ticket on every array, text and tree node, on the wire and in
      snapshots, plus a rewritten insertion rule in both SDKs. Rejected in
      attempt 2 on cost; it is the only direction that removes the dependency
      rather than constraining collection
- [ ] Whatever is chosen, the JS SDK needs the identical decision — the same
      three skips exist there
- [ ] Keep the fuzz as the acceptance criterion. "Fixed" means the
      collection-on run matches the collection-off control

## Reproduction and artifacts

`pkg/document/gc_rga_fuzz_test.go` in this filing holds the harness, skipped
because it fails on `main` — that is the point of it. Remove the skip to run it.

Attempt 2 is preserved on the branch `wip/rga-successor-barrier`, not for merge.
It carries the successor-barrier change and four targeted regression tests —
array remove+move, concurrent moves with a server-computed `minVV`, the same
defect in `Text`, the same defect in `Tree` — each verified red on `main` and
green with the fix. A third attempt should start from there rather than from
scratch.

## See Also

- `docs/tasks/active/20260912-undo-discards-concurrent-peer-edit-todo.md` — the
  other divergence found in the same investigation, on the content side
