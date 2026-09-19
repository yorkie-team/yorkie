# A split copies an attribute tombstone under the same id

**Created**: 2026-09-19

Tracked as yorkie#2002.

## Problem

Splitting a node that carries a **removed attribute** leaves the live document
and a document rebuilt from it disagreeing about garbage, and the rebuilt one
can never collect that tombstone.

Two independent errors produce it, and they mask each other:

1. **The copy is never registered.** `SplitElement` deep-copies the node's
   `RHT` into the new element — tombstones included — and no GC pair is
   created for the copies. They are garbage that no removal path produced, so
   the live document under-counts.
2. **The copy shares the original's id.** `RHT.DeepCopy` preserves `updatedAt`
   and `key`, which are exactly what `RHTNode.IDString()` is made of. When
   `NewRoot` walks a rebuilt tree it finds both tombstones and registers both,
   and since `Root.RegisterGCPair` reads a second registration under a known
   id as an *un*-registration, the second cancels the first.

So the live document counts one where there are two, and the rebuilt document
counts zero.

Copying the tombstone is not itself the bug. The copy has to reject the same
stale styles the original does: a concurrent `Style` whose ticket precedes the
removal must lose on both halves, or two replicas that apply the split and the
style in different orders end with different attributes on the two halves and
never reconverge. What is wrong is that the copy is an unregistered, unnamed
piece of garbage.

## Measurement

`<doc><p><span>abcdefghij</span></p></doc>`, then
`Style(1, 13, {color: red})` and `RemoveStyle(1, 13, [color])`:

| state | live GC / count | rebuilt GC / count |
|---|---|---|
| before the split | `{16 24}` / 1 | `{16 24}` / 1 |
| after `EditByPath([0,0,1], [0,0,1], nil, 1)` | `{16 24}` / 1 | `{0 0}` / 0 |
| after `GarbageCollect` | purges 1 | purges 0 |

The same defect is in the Text CRDT, which the issue did not name.
`TextValue.Split` deep-copies the value's attributes the same way. A text
attribute is tombstoned by undoing a `Style` that introduced the key — the
reverse operation carries `attributesToRemove`, the only route that reaches
`Text.RemoveStyle` today — and then any edit inside the styled run splits it:

| state | live GC / count | rebuilt GC / count |
|---|---|---|
| before the split | `{4 24}` / 1 | `{4 24}` / 1 |
| after `Edit(5, 5, "X")` | `{4 24}` / 1 | `{0 0}` / 0 |

Measured on `main` at a23ba9b8. Identical before and after #2000 and #2001.

## Tasks

- [x] Key `gcNodePairMap` on (parent, child) rather than the child's id alone.
      The parent is the discriminator because it is what `Purge` is called on:
      two pairs that share a parent and a child id name the same collectable
      thing, two that differ in either do not. Held as the `GCParent`
      interface value rather than an id string — no GC parent carries an
      identifier today, and every one of them is a pointer, so it is
      comparable and stable for as long as the registration lives.
- [x] Register the tombstones an element split copies, from
      `(*TreeNode).Split`, through the `pendingGCPairs` buffer the born-
      tombstoned path already uses. `TreeNode.DataSize` skips removed
      attributes, so the split's diff never charged these to `docSize.Live`:
      `GCOnlySize` sends each straight to `docSize.GC`, and `collect`
      subtracts the same amount back.
- [x] Register the tombstones a text split copies, from
      `(*RGATreeSplit[V]).splitNode`, reached through a `gcPairProvider`
      capability check on the split value. `GCOnlySize` here too, for the
      opposite reason — see Non-Goals.
- [x] Tests in `pkg/document/gc_attr_split_test.go`: the tree case and the
      text case, a multi-level split, a split of a split, a later `Style` that
      revives the key on both halves (the collision's other face — with the
      old key the second un-registration re-added the entry the first
      removed), and a two-replica exchange for each. Every one fails on
      `main`; the two-replica ones only once they assert the count rather than
      just that the replicas agree, since the leak was symmetric.
- [ ] Mirror in `yorkie-js-sdk`.

## Non-Goals

**The text attribute ledger.** `TextValue.DataSize` counts removed attributes
and `TreeNode.DataSize` does not, so a text attribute tombstone is charged to
`docSize.Live` and to `docSize.GC` at the same time, and purging it subtracts
only from GC — stranding its size in `Live` permanently. Both are visible on
`main` and neither is caused by this change:

```
after GarbageCollect, text case
  main:  live Live {30 192} | rebuilt Live {26 168}   1 tombstone stranded
  here:  live Live {30 192} | rebuilt Live {22 144}   2 tombstones stranded
```

The count doubles here only because the second tombstone is now counted at
all; the per-tombstone error is unchanged. This is why the text registration
uses `GCOnlySize` despite the copy having been charged to `Live`: the original
it was copied from is carried in `Live` too, so charging both the same way is
what keeps the live and rebuilt documents equal. Taking only the copy out
would half-fix the ledger and break the invariant this task is here to
restore. Filed separately.

**Text nodes inside a tree.** `SplitText` hands the right-hand node `nil`
attributes rather than a copy. That is correct, not a third instance of this
bug: `TreeNode.canStyle` returns false for text nodes, so a tree text node can
never carry an attribute.

## See Also

- `docs/tasks/active/20260919-tree-split-live-size-dropped-todo.md` — where
  this was found and recorded as out of scope
- `docs/design/concurrent-merge-split.md` — the specification of the split
  this changes
