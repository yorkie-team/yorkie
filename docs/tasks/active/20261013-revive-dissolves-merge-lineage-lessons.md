**Created**: 2026-10-13

# Lessons — Dissolve the merge lineage when a merge source is revived

## Dissolve the derived half, not the persisted one

The first cut cleared both halves at revive time: the `mergedInto` pointer
and the `MergedFrom`/`MergedAt` stamps on the children the merge had moved.
The pointer is a runtime cache, so clearing it is a local statement about
state that is recomputed anyway; the stamps are persisted CRDT state, and
clearing them is an edit to history.

That edit reaches further than the readers the fix was about.
`SplitElement`'s §7.1 rule partitions merge-moved children by reading
`MergedFrom`/`MergedAt` directly, and the §9.4 interloper guard fails open
on any node carrying a stamp — neither goes through `mergedInto`. Stripping
the stamps therefore changes how a later concurrent split or style resolves,
and whether it changes depends on whether the restore arrived before or
after: two replicas, two answers.

The symmetric fix is to make both *derivation* paths judge the same way
instead: `rebuildMergeState` and `mergeNodes` derive a pointer only onto a
removed source. A snapshot of a tree whose source was revived then loads to
exactly what the in-memory replica holds — stamps intact, no pointer — with
nothing destroyed and no dependence on when the restore was applied.

## A runtime cache is the right place to encode "not a source"

Worth noting why the persisted half looked necessary at first: `mergedInto`
is not in the snapshot, so "this element stopped being a source" seemed to
need somewhere durable to live. It does not, because the condition is not a
new fact — it is `removedAt == nil`, which is already persisted on the
source itself. Deriving the pointer under that condition carries the whole
statement, and the derivation runs on every load.

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
