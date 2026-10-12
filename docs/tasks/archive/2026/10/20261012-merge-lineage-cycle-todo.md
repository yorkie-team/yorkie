**Created**: 2026-10-12

# Refuse a merge that would move a node under itself

Fixes #2172. The JS half is yorkie-team/yorkie-js-sdk#1478.

## Problem

`mergeNodes` resolves its destination through `resolveMergeTarget`, which
follows `mergedInto`. `rebuildMergeState` derives `mergedInto` from the
`MergedFrom` an element payload (Set/Add/ArraySet) keeps, so a client can
point a tombstoned merge source at any element. When that element is a child
the merge moves, or lies inside one, `dest.Index.MoveChild(node.Index)`
re-parents the child under itself. `index.Node.MoveChild` has no cycle check,
and `UpdateAncestorsLength` walks `Parent` without a seen set, so the edit
never returns. It spins while holding the document lock, and the stored
change hangs every later snapshot rebuild of that document.

Only a crafted client reaches it: an honest SDK never sends `MergedFrom` on
an element payload that names an ancestor of the merge's own children.

## Layer decision

- **Decode time cannot decide it.** Whether the lineage forms a cycle
  depends on the document the change lands on.
- **Push-time rejection would not help stored changes**, the same as #2168.
- **Apply time, deterministic refusal.** In `mergeNodes`, do not move a
  child whose destination is the child itself or one of its descendants.
  Tombstone it in place with its subtree instead, book a GC pair for each
  node, and count them in `RemovedSize`; every later child of the same
  merge is stranded the same way, since the destination now sits inside a
  tombstone. Leaving the child live under the source this edit tombstoned
  would be the detached-but-registered state of #2008 once that source is
  purged. The check reads only the tree every replica holds when it applies
  the same change, so all replicas decide alike.
- **No new error from `index.MoveChild`.** An error there would hard-fail
  the change, which bricks the document the way #2119 describes. The skip in
  the only caller that can be steered is enough; `Split`'s `MoveChildBefore`
  moves a childless split product, which cannot be anyone's ancestor.

## Plan

- [x] Read issue, `mergeNodes`, `resolveMergeTarget`, `rebuildMergeState`,
      `index.MoveChild`
- [x] RED: crdt-level test that forges the lineage through `NewTree` (the
      converter's path) for both shapes (destination is the moved child,
      destination inside it) and runs the merge `Edit`; both hang on main
- [x] Fix: skip the child in `mergeNodes`
- [x] GREEN: both cases return, the child stays under its old parent
- [x] `make verify`
- [x] Self review (1 round, no blocking findings)
- [x] JS mirror in yorkie-js-sdk with the same test
- [x] Open PRs and link them (#2186, js-sdk#1478)

## Review

Self review plus four panel rounds (see lessons). The JS mirror,
yorkie-js-sdk#1478, was brought back to this exact behaviour after its own
panel took a different design; a probe on shared fixtures printed identical
parent, tombstones, GC pair order, removed size and XML on both SDKs.

## Out of scope

- Go accepts a live element as a merge source, so an undo can make the
  server erase merged text that JS clients keep: #2189.
- Concurrent multi-level deletes diverge by arrival order in both SDKs:
  #2190.
- #2187 reworks `mergeNodes` with per-child destinations and still hangs on
  this PR's test without the guard; the guard has to move with it.

