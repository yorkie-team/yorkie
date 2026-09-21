# Lessons: a style on a tombstoned node

**Created**: 2026-09-21

## A two-SDK disagreement is not automatically a "pick one" question

The issue read as a binary: the server styles a tombstone, the SDK does not,
choose. Building the two-replica test first showed the server does not agree
with *itself* — `editedAt.After(removedAt)` decides on an actor-ID tie-break
while the issuing replica has already applied the style unconditionally. Both
candidate answers in the issue were wrong, and the measurement is what said so.

## `crossSync` aliases version vectors; `wireSync` does not

The in-process `crossSync` hands the receiver the sender's live `*change.Change`
objects. `change.ID` shares its `VersionVector` *map* with the document's
`changeID` (`NewID(..., id.versionVector)` stores the reference), so the
receiver's `SyncClocks` mutates the delivered change's vector in place. A
causality check that reads the change's VV then sees the sender's post-sync
state instead of what it knew when it edited.

This produced a clean false negative: the causally-known-removal rule looked
non-convergent under `crossSync` and was correct under `wireSync`
(`pkg/document/tree_split_undo_test.go`), which forces every pack through
`converter.ToChangePack`/`FromChangePack`. **Any test asserting on operation
causality has to use `wireSync`.**

## docSize.GC is `Σ registered Child.DataSize()`

`collect` subtracts `pair.Child.DataSize()` read at purge time, not the amount
registration added — `GCOnlySize` exists only to make *registration* land on
that same invariant when a child's bytes are already inside a sibling's charge.
The consequence is that any mutation to a registered child's size after
registration has to be mirrored into `docSize.GC`, or a rebuild disagrees.
Writing an attribute onto a tombstoned node is such a mutation.
