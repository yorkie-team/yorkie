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
2. `Tree.Restore` revives through `unremove`, so the pointer is dropped
   there; nothing else is written. The `MergedFrom`/`MergedAt` stamps on the
   moved children stay — they are persisted CRDT state recording a merge
   that did happen, and §7.1's split partitioning and §9.4's interloper
   guard read them without going through `mergedInto`.
3. `rebuildMergeState` requires `src.removedAt != nil` before deriving
   `mergedInto`, and leaves the stamps alone otherwise. Deriving from the
   same evidence under the same rule is what makes a snapshot-loaded replica
   agree with an in-memory one that revived the source: stamps intact, no
   pointer, on both.
4. `mergeNodes` requires the source it recovers from a moved child's
   `MergedFrom` to be removed before pointing its `mergedInto` at the
   destination. Honest merges always tombstone the source in Phase 5, so
   this only refuses a crafted `MergedFrom` naming a live element.

Like the JS fix, require `removedAt` on both derivation paths so the two
ports agree on what a merge source is.

## Out of scope

- No proto change: `MergedFrom`/`MergedAt` stay as they are.
- Nothing rewrites persisted state: no stamp is cleared, at load or at
  revive.
- `declaredBoundaries`, the §7.1 split skip and the §9.4 interloper guard
  read `MergedFrom` directly. The stamps they read are unchanged by this
  fix, so they are untouched.

## Verification

- `pkg/document/tree_revive_merge_source_test.go`: the three-step repro
  across two replicas, asserting both replicas and a server-side replay
  land on `<r><p>abcd</p></r>`.
- `pkg/document/crdt`: unit coverage that a restore drops the pointer and
  keeps the stamps, that a reload of that tree derives the same, and that
  neither derivation path (`rebuildMergeState`, `mergeNodes`) points a live
  element named by a `MergedFrom`.
- `go test ./pkg/document/...`, `make lint`.
