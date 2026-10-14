**Created**: 2026-10-13

# Lessons — Dissolve the merge lineage when a merge source is revived

## Liveness cannot record that an element stopped being a source

The middle iteration of this fix dropped only `mergedInto` and left the
`MergedFrom`/`MergedAt` stamps, on the reasoning that the pointer is a
runtime cache and `removedAt == nil` is already persisted on the source, so
both derivation paths could simply decline on a live source and runtime and
snapshot would agree without destroying anything.

They agree only until the revived element is deleted again. Liveness is a
property of the moment, not a verdict on the merge: the moment an ordinary
edit re-tombstones that element the surviving stamps name a *removed* source
once more, and the next decode rebuilds the very pointer the revive dropped.
The in-memory replica, which cleared the pointer and never re-sets it
outside `mergeNodes`, keeps the text; a replica loaded from the snapshot
written after that delete cascades §6.2 over it. That is #2189 again, now
reachable by an ordinary reload rather than by an undo.

A durable statement needs durable state, so the stamps go too — the same
conclusion the JS port reached (yorkie-js-sdk#1406), which matters on its
own: a Go replica and a JS replica that disagree about what a merge source
is do not converge.

## What clearing the stamps costs

It is not free, and the cost is worth writing down. §7.1's split
partitioning reads `MergedFrom`/`MergedAt` directly, and §9.4's per-node
filter fails open on any node carrying a stamp; neither goes through
`mergedInto`. A split that never saw the merge is concurrent with the undo
that revives its source, so whether it reads the stamps depends on which
arrived first — two replicas, two partitions.

Both horns are divergence, so the choice is which is reachable. The
undissolved-lineage horn needs only a snapshot reload after a revive and a
delete, both ordinary. The split horn needs an unseen-merge split or style
concurrent with an undo of exactly that merge boundary. Clearing takes the
narrower one. Closing it properly needs a durable "no longer a source"
marker on the source itself, which is a proto change and a separate task;
§6.4 records it as a known limitation.

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

## Round 1 (panel): the lineage is not all under the target

The first implementation walked the children of the node `mergedInto` names.
That is where a merge *put* the stamped children, but not where they
necessarily still are: §7.1's `SplitElement` partitions a merged parent and
carries merge-stamped children into the other half of the pair, so a
children-of-target walk misses that half. The recreate branch of `Restore`
was missed for the mirror reason — a purged element comes back live under
its original ID with no pointer at all, so there was nothing to walk from,
while stamps elsewhere still named it. Both leftovers are re-derivable: they
name a live source now, and the next edit that tombstones the revived
element makes them name a removed one again.

`dissolveMergeSources` is keyed on the revived elements' own IDs and sweeps
the whole tree once per `Restore` call instead. The lesson is that
`mergedInto` is a cache of where the lineage *was*, so it is the wrong index
for finding the lineage; the source ID is the stable key, and keying on it
also drops the dependence on a pointer that may be a client's.
