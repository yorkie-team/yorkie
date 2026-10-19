---
title: document-size-limit
target-version: 0.7.24
---

# Document Size Limit

> **Status: implemented.** The server enforces `MaxSizePerDocument` on push
> with the lagging, growth-only gate described below.

## Problem

`MaxSizePerDocument` is a per-project quota on a document's size
(`server/backend/database/project_info.go`, default 10 MiB). It is sent to the
client in the attach response (`server/rpc/yorkie_server.go` →
`client/client.go`) and enforced there: `Document.Update` compares the clone's
`resource.DocSize.Total()` against `MaxSizeLimit` and refuses the local update
with `ErrDocumentSizeExceedsLimit` (`pkg/document/document.go`).

Before this design, the server never re-checked it. `pushPack` filtered
already-pushed changes, validated clientSeq continuity, serverSeq ordering and
epoch, and handed the remainder to `CreateChangeInfos`
(`server/packs/pushpull.go`). No branch on that path read the document's size,
because no branch on that path had a document at all.

So the quota was advisory. A client that does not run the check — a modified
SDK, or a direct Connect call — could grow a document without bound, and the
only things in the way were `maxRequestBytes` (`server/rpc/server.go`), which
bounds one push and not the total, and MongoDB's 16 MB BSON limit on the
snapshot (see [snapshot-overflow.md](snapshot-overflow.md)), which bites long
after the quota was meant to.

### Goals

- Bound the size of a document held on the server regardless of client behavior.
- Keep the common push path free of whole-document rebuilds.
- Never wedge a document that is already over quota: a client must always be
  able to shrink it, detach from it or remove it.

### Non-Goals

- Byte-exact agreement between the server's bound and the client's check. The
  client is the fast, exact gate; the server gate is the backstop.
- Replacing the client-side check. It stays: it is what gives an honest client a
  synchronous error at the edit that would exceed the quota, before the edit is
  applied locally.

## Design

The gate has two halves: **where the number comes from** and **what refusing
does to the client**. Both are decided below.

### Where the number comes from

The only way to obtain a document's size server-side is to build the
document: `BuildInternalDocForServerSeq` (`server/packs/snapshot.go`) — a
snapshot load, a range query over changes, an `ApplyChangePack`, a
`GarbageCollect` and two whole-document `DeepCopy` calls. Putting that in
`pushPack` would run it on every push, which is not acceptable.

So the gate **lags**: the path that already builds the document records its
size, and `pushPack` reads the recorded number.

- `storeSnapshot` materializes the document every `SnapshotInterval` changes.
  `CreateSnapshotInfo` (both drivers) now stores the document's **size** —
  `DocSize().Total()`, `Live` plus `GC`, data plus metadata, what the
  document holds — as `live_size` on the snapshot row (`SnapshotInfo.Size`).
  The stored snapshot keeps its tombstones (`storeSnapshot` collects
  nothing), so the size is measured on a copy collected at the minimum
  synced version vector, as `BuildInternalDocForServerSeq` collects the
  document it serves (`collectableSize`): it counts the garbage some
  attached client still holds back, not every tombstone since the last
  compaction. The stored name predates `GC` being counted; a row written
  before then holds `Live` alone, which is at most the total, so it admits
  as much or more until the next snapshot replaces it.
- `checkDocumentSize` (`server/packs/size_gate.go`), called from `pushPack`
  under `DocPushKey` after the epoch and serverSeq checks, reads the latest
  snapshot's metadata with `FindClosestSnapshotInfo(..., false)`: one indexed
  read without the snapshot body, and only for packs that can grow the
  document.

The size lives on the snapshot row, not on `DocInfo`. An earlier attempt kept
it on `DocInfo` and was reverted: `CreateChangeInfos` caches the caller's
`DocInfo` by reference, so writing the field raced every cache hit's deep copy,
and invalidating the entry turned an unlocked read on the pull path into a
stale overwrite and `ErrConflictOnUpdate`. The snapshot row is written once
and never mutated, so it has no such contract. It also resets itself:
compaction purges a document's snapshots, so the size goes back to unknown
instead of going on refusing growth on a document compaction just shrank.

**Total, as the SDK gates.** `DocSize.Total()` is `Live + GC`, what the
document holds, and the SDK's own `MaxSizeLimit` check already reads it. The
gate first read `Live` alone, so that a deletion, which moves bytes from
`Live` to `GC`, would shrink the gated figure at once. But some growth is
booked to `GC` from the start: content a peer writes into a Tree element a
concurrent change removed goes to the element's GC charge, as a rebuild
counts it (below), and a gate on `Live` never saw it, so a client that
skipped its own check could grow a removed element without bound
(yorkie#2252). The gate now reads the total, the same figure the SDK reads,
so the two sides agree and nothing booked to `GC` escapes the quota.

The cost is on deletions. A deletion moves bytes to `GC` without shrinking
the total, so a document over the limit, or near it with a lot of
uncollected garbage, keeps refusing growth until a collection covers the
removals and the next snapshot measures the smaller document. Garbage
clears at the next collection the minimum synced vector covers, so this
lasts as long as the slowest attached client holds that vector back (a
client that has just attached counts until it first reports what it has
seen), the
same window in which the SDK's own check already refuses the client's
edits. The document is never wedged: deletions, detaches and removes are
admitted throughout (see below).

**Live has to equal a rebuild.** The stored size is the running ledger of
the document the server built, so it must match what rebuilding the same
document reports (a snapshot load), or the gate refuses writes for bytes
the document no longer holds. The ledger tests check the running ledger
against `Root.Rederive`, a copy whose ledger is derived from the content by
`NewRoot`'s scan rather than copied from the document's own
(`assertRebuildsSame`).

A collection leaves alone a pair whose child is not removed, which no
honest change produces: it purges nothing and books nothing for it, where
it would otherwise dereference a missing removal or purge a live node
(`TestCollectLeavesAPairOfALiveNode`, yorkie#2245).

Tree content a rebuild counts in `GC` must not reach `Live` on the running
replica:

- **Content born a tombstone.** Content an `Edit` inserts under a removed
  parent is born a tombstone and booked to `GC` alone. Main booked its
  pair as an ordinary one, which moved bytes from `Live` that had never
  been added to it, so `Live` fell below a rebuild by that content
  (`TestTreeInsertUnderARemovedParentKeepsLive`).
- **Content of a removed Tree element.** An edit or a style inside a Tree
  element that is itself removed, a revive, a recreate or an insert,
  charges what it makes live to the element's GC charge, not to `Live`, as
  a rebuild counts a removed element in GC whole (`Root.ChargeTreeOp`,
  which the local proxy and the replayed operations share,
  `TestTreeOpsInsideARemovedTreeElement`,
  `TestLocalTreeOpsOnARemovedElementKeepTheCloneWithTheRoot`, yorkie#2251);
  main charged it to `Live`, which kept it for good. The charge is exact
  for a Tree that has one: a Tree a peer adds into a container already
  removed never went through a removal, has no charge, and sits in `Live`,
  edits to it included, until its container is collected, as on main.
- **Content of a released Tree element.** A restore under a `createdAt` a
  tombstone already answers to orphans that tombstone's subtree: `release`
  takes its bytes out of whichever side was holding them and records a zero
  charge, and a rebuild, which can reach the orphan from nothing, counts it
  on neither side. An edit inside it is charged to neither. Not to `GC`:
  that would top the release record up past zero, make it
  indistinguishable from a tombstone's own charge and leave the subtree
  uncollectable (`index`). Not to `Live` either: a delete there would
  subtract bytes `release` has already taken out, dropping the figure the
  gate reads below the real live size, so the orphan's delta is dropped
  (`TestChargeToRemovedElementKeepsAReleaseRecordZero`).

**Unknown admits.** A zero `live_size` — no snapshot yet, a snapshot written
before the field existed, or a document just compacted — admits the push. The
first snapshot after that arms the gate.

### What refusing does to the client

By the time the server can refuse, the client has already applied the change
locally. Yorkie has no "your change was rejected, roll it back" message, and a
naive gate deadlocks the document: if every push is refused, the push that
would delete content is refused for the same reason the push that added it
was.

The decision is to **refuse growth only**:

- When the recorded size is **strictly above** `MaxSizePerDocument`,
  `pushPack` refuses a pack if any of its changes **can grow** the document.
  Classification is by operation kind (`canGrow`), because the push path has
  no root to execute against, and is conservative: everything grows except
  - `Remove`;
  - `Edit` with no content, no attributes and nothing to revive (a text
    deletion);
  - `TreeEdit` with no contents, no split and nothing to revive (a tree
    deletion);

  An identity-preserving edit revives the span set its restore mode names:
  a restore-mode edit its restore spans, a retombstone-mode edit its
  retombstone spans. The other set is removed, so only the revived set
  counts as growth (yorkie#2252).
  - a change with no operations (presence only).
- The refusal is `document.ErrDocumentSizeExceedsLimit`, the error the SDK
  already raises for its local check, now carrying the code
  `ErrDocumentSizeExceedsLimit` (status `ResourceExhausted`). Nothing is
  stored and the server seq does not move.
- **Detach and remove always go through.** On an over-quota document, a
  detach or remove pack whose changes would grow it has those changes (the
  whole pack's) discarded and the status change proceeds, as stale-epoch
  changes are. Admitting them instead
  would let a client grow the document without bound by attaching, pushing
  inside the detach pack and attaching again.

A deletion can still add a little node metadata where it splits a text or tree
node at the range boundary. That growth is bounded by the content already in
the document, since each position can be deleted only once.

The only client this refuses is one that skipped its own check, or one whose
pack mixes deletions with growth: the whole pack is refused. Resending it at
once gets the same answer, so both SDKs' realtime sync loops stop retrying the
document every round and keep syncing the client's other documents; an
explicit sync still reports the refusal. The refused change stays queued, so
deleting content on that client afterwards does not help either: a pack
holding the change is refused whole.

The refusal is not permanent, though. The gate reads the project's limit and
the document's latest snapshot size, and both can move without the refused
client being party to it: an operator raises the limit, or peers delete
content. So the Go client holds the document off for `RejectedPushRetryDelay`
(10 seconds by default) and then re-probes, rather than waiting for an
explicit sync an app driven by the watch stream alone never makes. A document
waiting out a refusal pulls nothing either, since push and pull share one
`PushPull` and the refused pack goes with every pull, so that delay is also
how stale its view of its peers can get. The JS SDK can also drop the refused
change through its resync; the Go client cannot yet.

### The server-side write paths

Three writes do not come from a client and do not fit the lagging gate,
because each one builds the whole root on the server and therefore knows the
result exactly:

- `documents.CreateDocument` (admin/MCP) writes the initial root straight
  through `DB.CompactChangeInfos` and never reaches `pushPack` at all.
- `documents.UpdateDocument` (admin) and `revisions.RestoreRevision` rebuild
  the document and push it with `Status: attached`. Their packs are
  `SetYSON`-shaped, so `canGrow` says they grow, and the lagging gate would
  refuse them on exactly the over-quota documents they exist to repair —
  including a restore that brings the document back under the limit.

All three call `packs.CheckDocSize` on the document they hold, which measures
`DocSize().Total()` against `MaxSizePerDocument`, as the push gate reads it,
and raises the same
`ErrDocumentSizeExceedsLimit`. The two push paths then set
`PushPullOptions.SizeChecked`, which skips the lagging gate for that push
only. `SizeChecked` is never set from a client request.

### Contract: the overshoot

The recorded size is the one the last snapshot measured, so the gate lags the
document by up to one `SnapshotInterval` of changes (default 500). Snapshots
are taken after pushes, in the background, and skipped while another one for
the same document is running. Each of those changes arrives in a push capped
by `maxRequestBytes` (16 MiB, `server/rpc/server.go`). So a document can
exceed `MaxSizePerDocument` by what one snapshot interval of changes adds
before the gate refuses further growth. That is part of the quota's contract;
projects that need a tighter bound lower `SnapshotInterval`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Gate deadlocks a document that is already over quota | Refuse growth only, and always let deletions, detach and remove through |
| Lagging size lets a document exceed the quota by one snapshot interval | Stated as the quota's contract above; tighten `SnapshotInterval` for projects that care |
| A size source on `DocInfo` races the `docCache` | The size lives on the snapshot row, written once and read with its own query |
| Snapshots written before the field existed carry no size | Zero means unknown and admits; the next snapshot records it |
| Compaction shrinks a document while an old size keeps refusing growth | Compaction purges snapshots, so the size goes back to unknown |
| Server and client disagree on the number, so an honest client is refused | Both read the total, `Live + GC`; making the accumulator agree with a rebuild is tracked by the rebuild-drift work |
| Growth booked to `GC`, inside a removed Tree element, escapes the quota | The gate reads the total, so it counts (yorkie#2252) |
| A document with uncollected garbage near the limit is refused growth | Garbage clears at the next collection the minimum synced vector covers, and the SDK's own check already refuses the same edits; deletions stay admitted, so the document is never wedged |
| A pack mixing deletions with growth is refused whole and the SDK retries it | The SDKs stop resending it every round; the Go client re-probes once per `RejectedPushRetryDelay`, so a limit raised later is picked up; see above |
| One more read on the push path | Only for packs that can grow, and only the snapshot's metadata through an index |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Lagging gate over an exact one | An exact gate means building the document on every push; the whole point of the push path is that it is document-free |
| Size on the snapshot row, not on `DocInfo` | A field on the cached `DocInfo` inherits the `docCache` concurrency contract, which the reverted first attempt broke; the snapshot row is immutable and compaction already purges it |
| Total size, not Live | It is what the document holds and what the SDK's check reads; a gate on `Live` let growth booked to `GC`, inside a removed Tree element, past the quota (yorkie#2252). A deletion then shrinks the gated size only once garbage collection covers it |
| Refuse growth only | A blanket refusal deadlocks an over-quota document |
| Drop growth from detach and remove packs instead of admitting it | Admitting it would turn attach, detach-with-changes, attach into a way around the gate |
| Reuse `ErrDocumentSizeExceedsLimit` | It is the error the SDKs already know for the same quota |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Cap the bytes of a single change pack | Covered by `maxRequestBytes` (`server/rpc/server.go`), and it bounds one push, not a quota reached over many |
| Read the size off `be.Cache.Snapshot` in `pushPack` | Free, but never warm for a client that only pushes — `storeSnapshot` builds its own document and does not populate that cache |
| Persist the size on `DocInfo` | Tried and reverted; see above |
| Rebuild the document in `pushPack` | Correct and exact, but adds a snapshot load, a range query, a GC pass and two `DeepCopy` calls to every push |
| Refuse everything and define a recovery | Needs a terminal SDK state for every client, including honest ones mid-deletion |
| Do not refuse; detach or freeze out of band | Leaves the push path unbounded between housekeeping runs |
| Leave enforcement client-side | The quota is then advisory against anything but a stock SDK |

## Tasks

Implemented under
`docs/tasks/active/20261003-server-side-document-size-gate-todo.md`, which also
lists the SDK follow-up.

The accounting half — making the running `DocSize` accumulator agree with a
rebuild, so that the number the gate reads means the same thing on both sides —
is tracked separately.
