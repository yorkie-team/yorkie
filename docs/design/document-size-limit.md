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
  `CreateSnapshotInfo` (both drivers) now stores the document's **live size**
  — `DocSize().Live.Total()`, data plus metadata, without garbage — as
  `live_size` on the snapshot row (`SnapshotInfo.LiveSize`).
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

**Live, not Total.** `DocSize.Total()` is `Live + GC`, and deleting content
moves bytes from `Live` to `GC` without shrinking `Total`. A gate on `Total`
cannot see a document get smaller until garbage collection runs, which needs
every peer's version vector to move past the tombstones. `Live` drops as soon
as the deletion is measured. Since `Live <= Total`, the server's gate is also
never stricter than the SDK's own check against the same limit, so an honest
client's local check trips first.

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

- When the recorded live size is **strictly above** `MaxSizePerDocument`,
  `pushPack` refuses a pack if any of its changes **can grow** the document.
  Classification is by operation kind (`canGrow`), because the push path has
  no root to execute against, and is conservative: everything grows except
  - `Remove`;
  - `Edit` with no content, no attributes and no restore spans (a text
    deletion);
  - `TreeEdit` with no contents, no split and no restore spans (a tree
    deletion);
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
pack mixes deletions with growth: the whole pack is refused, and the SDK
currently retries the same pack. Handling the refusal in the SDKs (stop
retrying, surface a terminal over-quota state, let the user detach) is a
follow-up tracked in the task.

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

All three call `packs.CheckLiveSize` on the document they hold, which measures
`DocSize().Live` against `MaxSizePerDocument` and raises the same
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
| Gate deadlocks a document that is already over quota | Refuse growth only, gate on `Live`, and always let detach and remove through |
| Lagging size lets a document exceed the quota by one snapshot interval | Stated as the quota's contract above; tighten `SnapshotInterval` for projects that care |
| A size source on `DocInfo` races the `docCache` | The size lives on the snapshot row, written once and read with its own query |
| Snapshots written before the field existed carry no size | Zero means unknown and admits; the next snapshot records it |
| Compaction shrinks a document while an old size keeps refusing growth | Compaction purges snapshots, so the size goes back to unknown |
| Server and client disagree on the number, so an honest client is refused | The server compares `Live`, which is at most the client's `Total`; making the accumulator agree with a rebuild is tracked by the rebuild-drift work |
| A pack mixing deletions with growth is refused whole and the SDK retries it | SDK follow-up: treat `ErrDocumentSizeExceedsLimit` from a push as terminal |
| One more read on the push path | Only for packs that can grow, and only the snapshot's metadata through an index |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Lagging gate over an exact one | An exact gate means building the document on every push; the whole point of the push path is that it is document-free |
| Size on the snapshot row, not on `DocInfo` | A field on the cached `DocInfo` inherits the `docCache` concurrency contract, which the reverted first attempt broke; the snapshot row is immutable and compaction already purges it |
| Live size, not Total | Deleting content shrinks `Live` at once; `Total` waits for garbage collection, which would hold a shrunk document over the quota |
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
