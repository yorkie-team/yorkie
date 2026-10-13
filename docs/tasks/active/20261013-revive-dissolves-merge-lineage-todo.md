**Created**: 2026-10-13

# Dissolve the merge lineage when a merge source is revived

Fixes #2189. The JS half already landed as yorkie-team/yorkie-js-sdk#1406.

## Problem

Deleting an empty paragraph can erase text a concurrent merge moved out of
it, on the Go side only. From `<r><p>ab</p><p>cd</p></r>`:

1. d0 deletes the second `<p>` whole (`Edit(4,8)`) while d1 merges the two
   paragraphs (`Edit(3,5)`). Sync.
2. d0 `Undo()`s, reviving the second `<p>` by identity. Sync.
3. d1 deletes the now-empty `<p></p>` (`Edit(6,8)`).

Go lands on `<r><p>ab</p></r>` — "cd" is gone. yorkie-js-sdk lands on
`<r><p>abcd</p></r>`. A randomized run (10k seeds x 40 rounds) hit the state
1263 times, so it is not a corner case.

The revived `<p>` is live yet still carries `mergedInto` naming the first
`<p>`, and the moved "cd" still carries `MergedFrom`/`MergedAt` naming the
revived one. Go treats a *live* element as a merge source:

- `TreeNode.unremove` keeps `mergedInto`, and `Tree.Restore` leaves the
  moved children's stamps in place.
- `rebuildMergeState` and `mergeNodes` derive `mergedInto` for any element a
  child's `MergedFrom` names, without requiring that element to be removed.

So in step 3 the §6.2 cascade follows the stale pointer from the deleted
`<p>` and tombstones "cd".

## Invariant

A merge source is a tombstone. Every reader of `mergedInto` already requires
`removedAt` (§1.1 redirect, §6.2 propagation, `resolveMergeTarget`), so the
only gap is that the two *derivation* paths do not, and that revive does not
dissolve what it makes stale.

## Plan

1. `TreeNode.unremove` clears `mergedInto`: a revived element is live, so it
   is no longer a source.
2. `Tree.Restore` calls a new `Tree.dissolveMergeSource` before reviving an
   element, clearing `MergedFrom`/`MergedAt` on the children the merge moved
   into the target `mergedInto` names. Without this, a snapshot round trip
   re-derives the pointer from the surviving stamps.
3. `rebuildMergeState` requires `src.removedAt != nil` before deriving
   `mergedInto`, and clears the stale stamps when the source is live. Old
   snapshots already hold undissolved stamps; clearing them on load is what
   keeps a snapshot-loaded replica agreeing with an in-memory one (the
   in-memory one dissolved at revive time) and what keeps the next snapshot
   written clean.
4. `mergeNodes` requires the source it recovers from a moved child's
   `MergedFrom` to be removed before pointing its `mergedInto` at the
   destination. Honest merges always tombstone the source in Phase 5, so
   this only refuses a crafted `MergedFrom` naming a live element.

Match the JS fix exactly (it clears the pointer and the stamps on revive and
requires `removedAt` on both derivation paths), so the two ports stay
identical.

## Out of scope

- No proto change: `MergedFrom`/`MergedAt` stay as they are.
- Old snapshots are cleaned on load only; nothing rewrites stored changes.
- `declaredBoundaries` and the §7.1 split skip read `MergedFrom` directly;
  with the stamps dissolved there is nothing left for them to misread, so
  they are untouched.

## Verification

- `pkg/document/tree_revive_merge_source_test.go`: the three-step repro
  across two replicas, asserting both replicas and a server-side replay
  land on `<r><p>abcd</p></r>`.
- `pkg/document/crdt`: unit coverage that revive clears pointer and stamps,
  and that a snapshot carrying a live source's lineage loads dissolved.
- `go test ./pkg/document/...`, `make lint`.
