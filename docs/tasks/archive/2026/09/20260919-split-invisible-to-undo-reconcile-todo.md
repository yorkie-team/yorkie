# A split reports no index growth to undo/redo reconciliation

**Created**: 2026-09-19

Tracked as yorkie#1999.

## Problem

Undoing a split is a boundary deletion over the integer range
`[fromIdx, fromIdx + 2*splitLevel]` (`toSplitReverseOperation`). While that
reverse sits on the undo stack, `applyChanges` reconciles its indices against
every remote edit that lands, through

```go
from, to := op.NormalizePos()
d.history.ReconcileTreeEdit(op.ParentCreatedAt(), from, to, op.GetContentSize())
```

Both inputs are blind to a split:

- `NormalizePos` reports `PreEditFromIdx, PreEditFromIdx + RemovedSize`. A
  split removes nothing, so the range is zero-width.
- `GetContentSize` reported `InsertedContentSize`, which `Tree.Edit` sums over
  the content nodes Phase 8 accepted. A split creates boundaries instead of
  inserting nodes, so it contributed nothing.

So a remote split reached `ReconcileOperation` as a zero-width, zero-growth
edit, and Case 1 (remote entirely left of the stacked range) shifted the
stacked entry by `-0 + 0`. The reverse then ran at pre-remote indices.

## Measurement

`<doc><p><span>abcde</span></p></doc>` on two replicas:

```
d1: Edit(3, 3, nil, 1)   // split after `a`,  d1's own reverse -> [3, 5]
d2: Edit(6, 6, nil, 1)   // split before `e`, d2's own reverse -> [6, 8]
settled (both):  <doc><p><span>a</span><span>bcd</span><span>e</span></p></doc>

d2.Undo()
want:  <doc><p><span>a</span><span>bcde</span></p></doc>
got:   <doc><p><span>a</span><span>b</span><span>e</span></p></doc>
```

d1's split opened two tokens to the left of `[6, 8]`, so that range should
have reconciled to `[8, 10]` — the boundary d2 opened. Left at `[6, 8]` it
names `c` and `d` in the settled tree, and deletes them. Both replicas
converge on the loss, so nothing surfaces it to either application.

Instrumenting the reconcile call site shows the whole mechanism:

```
remote TreeEdit splitLevel=1 normalize=(3,3) contentSize=0   <- d1's split, seen by d2
remote TreeEdit splitLevel=1 normalize=(8,8) contentSize=0   <- d2's split, seen by d1
remote TreeEdit splitLevel=0 normalize=(6,8) contentSize=0   <- the undo that ran: "cd"
```

The last line is also the counter-proof that the rest of the path is sound: a
*boundary deletion* reports its width correctly through `RemovedSize`. Only
the split direction was unaccounted for.

Undoing d1's split instead is correct, and always was: d2's split is to the
right of `[3, 5]`, which is Case 2 — no shift needed, and none applied. Two
boundaries in one node is not the trigger; a remote split landing to the left
of a stacked split reverse is. Two *sequential* splits on one replica undo
correctly.

## Tasks

- [x] Report the growth: `TreeEditReverseInfo.SplitSize`, measured across
      Phase 7 as the change in `t.Root().Len()`. Measured rather than computed
      as `2*splitLevel`, so a split whose product is born tombstoned reports
      the zero growth it really produced — and so a `splitLevel` the tree has
      no room for reports what it could actually split.
- [x] Make `Root().Len()` worth reading, in two places. `SplitElement` added the new
      element's padded length to its ancestors' `VisibleLength`
      unconditionally, so a piece born tombstoned lengthened live ancestors
      that could never drain it again: the field assignment never passes
      through `remove()`, which is what maintains the same invariant from the
      other side. The inflation was latent until this task gave the cached
      length a correctness-critical reader — with it, a born-tombstoned remote
      split reported a growth of two and shifted the stacked reverse past the
      boundary it opened, deleting the text beyond it. Guarded on
      `split.removedAt == nil`; `TotalLength` counts tombstones and still
      grows either way. The same guard then exposed the other half: §7.4's
      Empty Sibling Re-Parenting moved the split with `DetachChild` plus
      `InsertBefore`, neither of which is tombstone-aware, so the source lost
      two tokens it never held while the destination gained two it must not
      have — the old unconditional inflation had been cancelling the first of
      those by accident. Moved through a new `MoveChildBefore` instead, which
      carries the semantics `MoveChild` already documents for exactly this.
      `MoveChild` and `MoveChildBefore` share one detach/attach pair rather
      than stating the tombstone rule twice — restating it per call site is
      how the two length dimensions drifted apart in the first place — and
      `MoveChildBefore` decides both of its failures before anything moves,
      so a refused move cannot leave a child belonging to no parent.
      Nothing under `go test ./pkg/...` reaches §7.4, so the new primitive is
      pinned directly by `TestIndexTreeMoveChildBefore`. `test/complex`'s
      `TestTreeConcurrency*` (build tag `complex`) does reach it, and its
      counts are identical on `main` and here — 1612 pass, 0 skip, 0 fail.
      That is the baseline a CRDT change owes that suite: it skips on
      divergence, so a regression there hides as a new SKIP rather than a
      failure.
- [x] Sum it into `GetContentSize`, whose single reader is the reconciliation
      loop and whose contract is "how far did the indices to the right of this
      edit move". `InsertedContentSize` keeps its own meaning — it also sizes
      the copy-reinsert reverse's range, which must not grow by boundaries.
- [x] Regression tests in `TestTreeSplitUndoConcurrent`: the reported case,
      the mirror case that must not over-shift, the redo direction (a re-split
      carries the same indices and is reconciled the same way), an L2 remote
      split, whose shift is four rather than two, a remote split with no
      visible effect, which must shift nothing, and a remote split landing
      inside a stacked range, which is reconciliation Case 4 — the range grows
      to cover it, so the undo absorbs that boundary too. That last one is the
      six-case semantic Text already had, newly reachable for a split.
- [x] Size the split's own reverse range from `SplitSize` too, not from
      `2*e.splitLevel`. The loop stops when it runs out of ancestors to split,
      so a level the tree has no room for asked for more boundaries than it
      opened, and the reverse covered tokens the split never touched. No
      concurrency needed:

      ```
      <r><p>abcd</p><p>0123456789</p></r>
      Edit(3, 3, nil, 3)   // only one level is splittable
        -> <r><p>ab</p><p>cd</p><p>0123456789</p></r>   (correct)
      Undo()
        want: <r><p>abcd</p><p>0123456789</p></r>
        got:  <r><p>ab0123456789</p></r>                 `cd` gone
      ```

      A split that opened nothing now produces no reverse at all, rather than
      a range the `toIdx > tree.Root().Len()` guard only sometimes caught.
- [x] Correct `docs/design/tree-split-undo-redo.md`, whose edge-case table
      claimed reconciliation already handled a concurrent split.
- [x] Mirror in `yorkie-js-sdk`, all three halves. `TreeEditOperation.getContentSize`
      reports only `insertedContentSize`, so the reconciliation defect
      reproduces there identically; it is recorded as a skipped case in
      `packages/sdk/test/integration/history_tree_concurrent_test.ts`
      (`KNOWN: undo one of two concurrent splits of the same node`), added in
      yorkie-js-sdk#1358. `util/index_tree.ts`'s `splitElement` also calls
      `clone.updateAncestorsSize(clone.paddedSize())` unconditionally, so the
      born-tombstoned inflation is latent there too — and it would become
      load-bearing the moment the first half lands. That in turn forces the
      §7.4 change: `crdt/tree.ts` still re-parents with `detachChild` plus
      `insertBefore` and `util/index_tree.ts` has no `moveChildBefore`, so the
      guard on its own leaves the source two short and the destination two
      long. The three have to land together. Landed as yorkie-js-sdk#1360,
      with the over-deep reverse range as a fourth.

## Non-Goals

Two other edits still misreport their index movement to the same loop, both
identical on `main` and neither reachable through a split: `RemovedSize` does
not count the nodes `propagateMergeDeletes` tombstones, since those never
enter `toBeRemoveds`, and `InsertedContentSize` counts content that is born
tombstoned on the way into a removed parent, which moves no visible index.
Both are the same class of defect as this one and want their own measurement
and their own issue.

## See Also

- `docs/design/tree-split-undo-redo.md` — the reverse-as-boundary-deletion
  design this corrects
- `docs/design/concurrent-merge-split.md` — the forward-convergence side of
  the same interaction
