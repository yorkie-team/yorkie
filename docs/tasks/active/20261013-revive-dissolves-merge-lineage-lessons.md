**Created**: 2026-10-13

# Lessons — Dissolve the merge lineage when a merge source is revived

## Why revive has to clear both halves

Clearing `mergedInto` alone fixes the in-memory replica and nothing else.
The pointer is a runtime cache: `rebuildMergeState` re-derives it from the
children's `MergedFrom` on the next snapshot load, so a replica that reloads
after the revive resurrects the stale source and cascades again. The stamps
on the moved children are the persisted half of the same fact, and both have
to go for the two paths to agree.

## Why old snapshots are cleaned rather than merely ignored

Requiring `removedAt` in `rebuildMergeState` without clearing the stamps
would be self-inconsistent over time. The source is live at load, so no
pointer is derived — but when a later edit tombstones that source and the
document is snapshotted again, the *same* undissolved stamps now name a
removed element and the pointer comes back. One replica (in memory, stamps
dissolved at revive) would keep the text while another (loaded from the old
snapshot) tombstones it. Clearing on load makes the load idempotent.

## The two derivation paths are not symmetric

`mergeNodes` can only meet a live source through a client-supplied
`MergedFrom` on an element payload: an honest merge tombstones the source in
Phase 5 (`collectBetween` appends every `toBeMergedNodes` entry to
`toBeRemoveds` when `canDelete` passes, and when it does not the node was
already removed). So the guard there is a crafted-input guard, while the one
in `rebuildMergeState` is a real-data guard for snapshots written before
this fix.

## Self review

Not run: this task was executed in the autonomous one-shot harness, which is
granted no tool that can launch the reviewer subagent `/self-review`
requires. Recorded here so a skipped round is not read as a clean one — the
branch's review is CI, `@claude review`, and the human reviewer on the PR.
