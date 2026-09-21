# Lessons — a split copies an attribute tombstone under the same id

**Created**: 2026-09-19

The plan is in `20260919-split-copies-attr-tombstone-id-todo.md`.

## Two errors that cancelled to a plausible number

The live document reported one collectable tombstone where there were two, and
the rebuilt document reported zero. Only the second number looks wrong, so the
obvious reading is that the rebuild is broken and the live document is the
reference. It is not: the missing registration and the colliding id are
separate defects, and fixing only the id would have made the rebuilt document
report two against the live document's one — a new divergence in place of the
old one.

What kept this honest was asserting both directions at every step rather than
just at the end: count, GC size and Live size, live against rebuilt, before
the split, after the split, and after collection.

## The issue named one CRDT; the defect was in two

The report was about `Tree`. `Text` has the same shape — `TextValue.Split`
deep-copies the value's attributes, `RHT.DeepCopy` preserves the id — and it
reproduces identically. Nothing in the report pointed there; what pointed
there was reading `RHT.DeepCopy`'s callers instead of `SplitElement`'s. When
the root cause is in a shared primitive, the call sites are the search space,
not the reported symptom.

Reaching it took a detour: a text attribute can only be tombstoned by undoing
a `Style` that introduced the key, because the reverse operation is the only
producer of `attributesToRemove` that reaches `Text.RemoveStyle`. A first
attempt used `Style(key, "")`, which overwrites rather than removes, and
produced no tombstone and a clean bill of health.

## The same repair, opposite accounting, in the two places

Both split sites register the copied tombstone with `GCOnlySize`, and the
reasons are inverses. `TreeNode.DataSize` skips removed attributes, so the
copy was never charged to `Live` and `GCOnlySize` is literally what the field
means. `TextValue.DataSize` counts them, so the copy *was* charged — and
`GCOnlySize` is still right, because the original it was copied from is
carried in `Live` the same way, and the rebuilt document computes `Live` from
the same content. Matching the original beats matching the field's
description.

That asymmetry is a real defect in the text ledger (a tombstone charged to
`Live` and `GC` at once, and stranded in `Live` when purged). Half-fixing it
from inside this change would have traded a GC divergence for a Live one.

## Interface values make serviceable map keys

`gcNodePairMap` needed (parent, child) and no GC parent carries an id. Adding
`IDString()` to `GCParent` would have meant giving one to `RGATreeList`,
`RGATreeSplit` and `TextValue`, none of which know their element's `createdAt`
— a lot of plumbing to produce a string whose only consumer is a map key.
A struct key holding the interface value does the same work: every
implementation is a pointer, so it is comparable, and the pair already held
the same reference. The cost is that the key is not stable across document
instances, which nothing needs — registration and lookup always happen within
one root.

## The repair that had to be un-repaired

Review found that registering the text copies made the GC ledger depend on
collection order, so the next commit netted out the bytes a nested pair
already covered. The netting is recomputed from the child's current state at
registration and again at collection, which is sound only while that state
holds still. It does not: `canStyle` deliberately permits styling an
already-tombstoned node, and reviving a key drops the removed `RHTNode`, so a
node charged `D − A` is refunded `D` and `docSize.GC` finishes negative. Fifty
runs in fifty, where `main` and the commit before the fix both finish at zero.

The earlier attempt — record what was charged, refund exactly that — fails on
the mirror-image case, a registered node that is later split and partly
revived. Between them the two cover the whole design space for reconciling a
charge with a refund, and both fail because the underlying quantity is not
stable. That is the signal that the defect is not here: `TextValue.DataSize`
counts removed attributes and `TreeNode.DataSize` does not, so the text ledger
double-counts before this change touches it. The Tree half needs none of this
machinery and has been clean through both review rounds.

Knowing when a fix is downstream of another one is worth more than landing it.

## Twelve thousand seeds missed a five-operation bug

Two round-2 reviewers reached opposite conclusions. A differential fuzz over
~12 000 seed-runs reported no regression attributable to the change, having
classified the branch-only negative-`GC` seeds as map-order flake after
re-running *different* sequences. A targeted reviewer reasoning from
`canStyle`'s comment found a five-operation sequence that is negative 50 times
in 50.

The fuzz was not wrong about what it measured; its generator never emitted
"style an already-tombstoned node to revive its removed attribute", so the
shape was outside its reach. Breadth bounds how much of a space you sample,
not which parts of it you can reach at all. When a broad sweep and a narrow
probe disagree, the probe's concrete reproducer settles it — re-running the
exact sequence on both sides, fifty times, cost a minute and was decisive.

