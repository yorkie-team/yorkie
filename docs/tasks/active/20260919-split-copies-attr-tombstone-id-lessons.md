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

## Registering garbage changed what a buffer meant

Two of the three defects review found have the same shape: a path that was
correct only because a buffer was always empty. `RGATreeSplit.retombstone`
never drained `pendingGCPairs`, and that was right for as long as splitting a
live piece buffered nothing. `Tree.Retombstone` is in the same position today
and stays correct only because `SplitText` passes nil attributes.

Adding a producer to a shared buffer is not a local change. The question to
ask is not "does my new code drain?" but "which existing readers assumed this
was empty?" -- and the answer is found by listing every caller of the thing
that now buffers, not by reading the diff.

## The first fix for the ledger was rejected by a test I did not write

Collection subtracted the child's size read at purge time, which moves when a
sibling is purged first, so the ledger came out differently depending on Go's
map iteration order. Recording the amount charged at registration and giving
exactly that back looks like the obvious repair, and it is wrong:
`TestTextRestoreDocSizeAccounting` covers a registered node that is later
split and partly revived, which redistributes its bytes across three pairs,
and the recorded figure stops matching any of them.

The repair that works attacks the double charge instead of its symptom. A text
node's `DataSize` counts its removed attributes and each of those is a
registered pair in its own right, so the node was charged for bytes another
pair already covered. Netting the nested size out at both ends is stable under
any purge order, because purging an attribute removes it from `DataSize` and
from the nested total in the same step. It also fixed the pre-existing shape
(no split at all), which had been leaking 38 times in 40 on `main`.

## Three lenses, one finding, and that is the signal

All three reviewers independently reported the same order-dependent GC leak,
and two of them proposed the same fix. Convergence from prompts that shared no
vocabulary -- one was asked about convergence, one about the ledger, one about
structure -- is worth more than any single report's confidence rating. The two
findings only one reviewer reached (the undrained buffer, the silently
optional interface) are the argument for running more than one lens at all.

One classification needed correcting: the leak was reported as introduced
here. It is not -- the same shape without a split fires on `main` -- and the
difference matters, because "this PR adds one more instance of a pre-existing
defect" and "this PR creates a defect" lead to different decisions about
whether to fix it here. Measuring `main` in the same run is what settled it.
