# Tree split drops the size of the element it creates

**Created**: 2026-09-19

Tracked as yorkie#1998.

## Problem

`(*Tree).split` — Phase 7 of `Tree.Edit` — discards the `resource.DataSize`
that `(*TreeNode).Split` returns:

```go
if _, err := parent.Split(t, offset, issueTimeTicket, versionVector); err != nil {
```

`SplitElement` computes that diff correctly (one `time.TicketSize` for the new
element node, plus its attributes), and every other `(*TreeNode).Split` caller
propagates it — `isolateTextRange` and `FindTreeNodesWithSplitText` both do.
Phase 7 was the only site dropping it, because `(*Tree).split` was declared
`) error` and had nowhere to put it. So `docSize.Live` never carried the
elements a split creates.

## Measurement

Seed `<doc><p><span>abcdefghij</span></p></doc>`, then cycle
`EditByPath([0,0,1], [0,0,1], nil, 1)` (split) and
`EditByPath([0,0,1], [0,1,0], nil, 0)` (merge it back):

| cycle | after split | after merge |
|---|---|---|
| start | — | `{Data:20 Meta:168}` |
| 0 | `{20 192}` | `{20 168}` |
| 1 | `{20 168}` | `{20 144}` |
| 2 | `{20 144}` | `{20 120}` |

`Live.Meta -= 24` per cycle, without bound: 100 cycles reach `{20 -2208}`.
`Data` never drifts. Cycle 0 nets to zero only because its text split (Phase 1,
accounted) masks the missing element ticket; later cycles split at a position
that is already a text-node boundary, so the deficit shows.

The merge side is correct: it tombstones the split-born `<span>`, and
`AdjustDiffForGCPair` books `-48 + 24 = -24` to `Live` — right for a node
`Live` was holding, which it never was.

This is the number the server's document size limit reads, and a rich-text
editor cycles a split and a merge on every Enter and Backspace.

Same defect from the other side: after one split,
`doc.DocSize().Live` is `{20 192}` while
`doc.InternalDocument().DeepCopy().DocSize().Live` is `{20 216}` — `NewRoot`
recomputes the size from the tree, so a snapshot rebuild and the live document
report different sizes for identical content.

## Tasks

- [x] Give `(*Tree).split` a `resource.DataSize` return and accumulate each
      `(*TreeNode).Split` diff in its loop, so a level-2 split contributes both
      tickets. Add the result to `Edit`'s `diff` **before** the error check,
      the way Phase 1 adds `diffFrom` before returning on the `to`-resolution
      error: a multi-level split that fails partway through has still created
      the elements it got to.
- [x] Keep the born-tombstoned path untouched: `(*TreeNode).Split` already
      returns a zero diff for a piece that inherits `removedAt` from a
      concurrently deleted parent, routing its size to `docSize.GC` through
      `GCPair.GCOnlySize`. Accumulating `Split`'s return value inherits that;
      recomputing the size here would not.
- [x] Regression tests in `TestDocumentSize` covering the split/merge cycle's
      steady state, a level 2 split (which pins the loop's accumulation — with
      the diff assigned rather than accumulated, a level 1 test still passes),
      and the `DeepCopy` agreement. That last assertion is the cheap invariant
      for this whole class of bug (incremental vs. recomputed size), and no
      tree-split test carried it before.
- [x] Stop charging `docSize.Live` for attribute tombstones it never held.
      `RHT.Remove` mints a tombstone even for a key the element never carried
      — deliberately, so a remove that arrives before its set still wins — and
      supersedes an existing tombstone with a fresh one when the same key is
      removed twice or set again. None of those had a live predecessor, yet
      `AdjustDiffForGCPair` debited `Live` for each. Measured on
      `<doc><p>abc</p></doc>`, `Live` starting at `{6 144}`:

      | edit | before | after |
      |---|---|---|
      | `RemoveStyle` of an absent key | `{6 144}` | `{-12 120}` |
      | `RemoveStyle` of the same key twice | `{6 144}` | `{-2 96}` |
      | `Style`/`RemoveStyle` toggled, per cycle | — | `-{4 24}` |

      The last one is a rich-text editor toggling bold, so it runs without
      bound; the reviewer's 2000-cycle probe reached `Total() = -211638`, and
      a negative `Live` means the document size limit has stopped applying.
      The GC ledger was already right — superseding a tombstone correctly
      takes the old one back out of GC — so the repair is only to route these
      through `GCOnlySize`, the same way a born-tombstoned split piece goes.
- [x] Mirror in `yorkie-js-sdk`: `crdt/tree.ts`'s split phase is a close port
      and drops the same diff (`tree.ts` inlines Phase 7 and discards
      `CRDTTreeNode.split`'s tuple). `MaxSizeLimit` is enforced client-side, in
      `Document.Update`, so until the mirror lands a JS client and a Go client
      enforce different limits on the same document — the JS one under-counting
      by a ticket per split-born element. A correctness follow-up, not a
      tidiness one. Landed as yorkie-js-sdk#1359 — two of the three cases
      reproduced there; toggling a key was already correct in that SDK,
      because the restyle credits live for the node it revives.

## Non-Goals

The steady state after a merge is `{20 192}`, not the pre-split `{20 168}`:
the merge rejoins the element but leaves the text as two nodes, so one extra
live text node persists. GC never reclaims it — it is live, not garbage — so
the charge is permanent and correct: a one-time ticket per text node ever
split, not a per-cycle drift. yorkie#1998's "expected" figure assumes
otherwise; the fix does not chase it.

The `DocSize() == DeepCopy().DocSize()` invariant is asserted here only for
the shapes these tests drive. This task closes the split-side gap; it does not
survey every path a document can reach, so the invariant is not claimed to
hold everywhere. One counterexample sits in the split region itself and is
worth naming, since it is the same assertion with one attribute added.

`SplitElement` deep-copies the whole `RHT`, tombstones included, and
`RHT.DeepCopy` preserves `updatedAt` and `key` — which are exactly what
`RHTNode.IDString()` is made of. So the copied tombstone is indistinguishable
by id from the original, and `RegisterGCPair` reads a second registration
under an existing id as an *un*-registration. Splitting an element that
carries a removed attribute therefore leaves the two documents disagreeing:

```
<doc><p><span>abcdefghij</span></p></doc>
Style(1, 13, {color:red}); RemoveStyle(1, 13, [color])
  live and rebuilt agree: GC {16 24}, 1 collectable
EditByPath([0,0,1], [0,0,1], nil, 1)
  live:    GC {16 24}, 1 collectable, purges 1
  rebuilt: GC { 0  0}, 0 collectable, purges 0
```

In a rebuilt document the tombstoned attribute can never be collected. It is
the GC ledger rather than the Live one, it is identical on `main`, and the
repair is an identity question — mint a distinct id for the copied tombstone,
or key `gcNodePairMap` on (parent, child) — so it is not this task's to make.
Tracked as yorkie#2002; it reproduces in the JS SDK too.

Also out of scope: the issue's note that `DocSize()` hands back a reference
rather than a copy. `resource.DocSize` and `resource.DataSize` are plain value
structs and every accessor returns by value, so a captured "before" value
cannot move in Go. If that finding is genuine it belongs to the JS SDK.

## See Also

- `docs/tasks/active/20260817-docsize-snapshot-rebuild-drift-todo.md` — the
  other incremental-vs-rebuilt size divergence, element-level and independent
- `docs/design/concurrent-merge-split.md` — the specification of Phase 7's
  split loop, the code this changes
