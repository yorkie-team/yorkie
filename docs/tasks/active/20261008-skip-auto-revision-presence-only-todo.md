# Skip auto revisions when only presence changed since last snapshot

**Created**: 2026-10-08

## Context

Issue #2152. Presence-only changes take a `server_seq` but are never written
to the `changes` collection — they live in the in-memory presence cache only.
The snapshot interval is measured in `server_seq`
(`server/packs/snapshot.go:196`), so a client that sends its cursor every
100 ms drives a snapshot roughly every 50 s with the default interval of 500,
even when nobody edits.

`storeSnapshot` rebuilds the document from the changes persisted between the
previous snapshot and `docInfo.ServerSeq`. When only presence moved, that list
is empty and the new snapshot carries the same root as the previous one.
`storeRevision` then runs unconditionally, and because new projects have
`AutoRevisionEnabled: true`, every such snapshot stores a revision whose
content is byte-for-byte the previous revision.

## Goal

Proposal 1 from the issue (the low-risk half): skip `storeRevision` when the
persisted change list since the previous snapshot is empty. Nothing is lost —
restoring either revision yields the same root.

Proposal 2 (skipping the snapshot itself, or counting the interval in
operation changes) is explicitly a discussion in the issue, not a requested
fix. Out of scope here; the PR body carries the reasoning back.

## Plan

- [x] Read `server/packs/snapshot.go` and confirm `changes` is exactly the
      persisted-change list between the two snapshots.
- [x] Guard the `storeRevision` call on `len(changes) > 0`, with a NOTE
      explaining why an empty list means an identical revision.
- [x] Integration test in `test/integration/revision_test.go`: after a
      snapshot with real operations, drive `SnapshotInterval` presence-only
      syncs and assert the auto-revision count does not grow.
- [x] `make lint`.

## Non-goals

- Changing when snapshots are written (Proposal 2).
- Changing the meaning of `SnapshotInterval`.
- Any API or protobuf change.

## Notes

- Edge case: when no snapshot exists yet (`info.ID == ""`) and no change was
  ever persisted, the document is empty and the skip means no revision is
  written. A revision of an empty root carries nothing, so this is the same
  "nothing is lost" argument.
- Garbage collection during `ApplyChangePack` cannot make an empty-change
  snapshot differ in revision content: the revision serialises the visible
  root (`yson.FromCRDT(doc.RootObject())`), and GC only drops tombstones.
