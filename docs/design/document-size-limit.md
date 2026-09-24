---
title: document-size-limit
target-version: 0.7.24
---

# Document Size Limit

> **Status: proposal.** Nothing in this document is implemented yet. It records
> the gap between where `MaxSizePerDocument` is enforced today and where it
> would have to be enforced to bound an untrusted client, together with the cost
> of each candidate gate. It exists so the decision is made once, in the open,
> rather than re-litigated in every review that touches document accounting.

## Problem

`MaxSizePerDocument` is a per-project quota on a document's size
(`server/backend/database/project_info.go:135`, default 10 MiB). It is sent to
the client in the attach response
(`server/rpc/yorkie_server.go:353` → `client/client.go:518`) and enforced there,
and only there: `Document.Update` compares the clone's
`resource.DocSize.Total()` against `MaxSizeLimit` and refuses the local update
with `ErrDocumentSizeExceedsLimit` (`pkg/document/document.go:258`).

The server never re-checks it. `pushPack` filters already-pushed changes,
validates clientSeq continuity, serverSeq ordering and epoch, and hands the
remainder to `CreateChangeInfos` (`server/packs/pushpull.go:316`). No branch on
that path reads the document's size, because no branch on that path has a
document at all.

So the quota is advisory. A client that does not run the check — a modified SDK,
or a direct Connect call — can grow a document without bound, and the only
things standing in the way are `maxRequestBytes` (`server/rpc/server.go:45`),
which bounds one push and not the total, and MongoDB's 16 MB BSON limit on the
snapshot (see [snapshot-overflow.md](snapshot-overflow.md)), which bites long
after the quota was meant to.

### Goals

- Bound the size of a document held on the server regardless of client behavior.
- Keep the common push path free of whole-document rebuilds.
- Give a client that is refused a defined way back to a usable state.

### Non-Goals

- Byte-exact agreement between the server's bound and the client's check. The
  client is the fast, exact gate; the server gate is the backstop.
- Replacing the client-side check. It stays: it is what gives an honest client a
  synchronous error at the edit that would exceed the quota, before the edit is
  applied locally.

## Design

The gate has two halves that can be decided separately: **where the number comes
from** and **what refusing does to the client**. The second is the harder one.

### Where the number comes from

The only way to obtain a document's size server-side today is to build the
document: `BuildInternalDocForServerSeq` (`server/packs/snapshot.go:67`) — a
snapshot load, a `FindChangesBetweenServerSeqs` range query, an
`ApplyChangePack`, a `GarbageCollect` and two whole-document `DeepCopy` calls.
That runs today only when a pull crosses the snapshot threshold. Putting it in
`pushPack` would run it on every push, which is not acceptable.

The workable shape is a **lagging gate**: have the path that already builds the
document persist its size, and have `pushPack` read the persisted number.

- `storeSnapshot` (`server/packs/snapshot.go:166`) already materializes the
  document every `SnapshotInterval` changes and already writes a row. It would
  additionally write `doc.Root().DocSize()` onto `DocInfo`.
- `pushPack` already fetches `currentDocInfo` under `DocPushKey` whenever there
  is anything to push (`server/packs/pushpull.go:287`), so reading the field
  there is free.

This requires a new `DocInfo` field carried through both backends (`mongo` and
`memory`), `DocInfo.DeepCopy`, and an update on the snapshot write.

**What it bounds.** The size the gate sees is at most `SnapshotInterval` changes
stale, so a document can overshoot the quota by whatever one snapshot interval's
worth of pushes adds, each push itself capped by `maxRequestBytes`
(`server/rpc/server.go:45`, 16 MiB) — the `connect.WithReadMaxBytes` cap on
every handler. That is a bound; the status quo has none. The overshoot is the
price of not rebuilding on every push, and it should be stated in the quota's
documentation rather than hidden.

**What it does not bound.** Compaction rebuilds a document from scratch and
rewrites only `server_seq`, `compacted_at` and `epoch`
(`server/backend/database/mongo/client.go:2004-2017`, and the memory driver's
`CompactChangeInfos`). A gate that reads a persisted size must therefore reset
that field on the compaction path too, or it goes on refusing growth on a
document compaction just shrank. The same holds for any other path that purges
document internals.

### What refusing does to the client

This is the part that makes the gate a protocol change rather than a patch.

By the time the server can refuse, the client has already applied the change
locally and is asking for it to be durable. Yorkie has no "your change was
rejected, roll it back" message: a client whose push is refused retries the same
pack forever and its checkpoint never advances.

Worse, a naive gate deadlocks the document. `DocSize.Total()` is
`Live + GC` (`pkg/document/resource/resource.go:26`), and deleting content does
not shrink `Total` — it moves bytes from `Live` to `GC`, where they stay until
every peer's version vector has moved past the tombstone. So a client cannot
edit its way back under an all-changes-refused gate: the push that would delete
content is refused for the same reason the push that added it was.

The deadlock is not, however, a semantic the server would be inventing. The
client's own gate compares the post-update `Total()`
(`pkg/document/document.go:257-258`), so a stock SDK *already* refuses a
deletion on an over-quota document with `ErrDocumentSizeExceedsLimit`. An
honest client is therefore already stuck in exactly the way a blanket
server-side refusal would get a dishonest one stuck. What the server gate adds
is not a new failure mode but the need to reach that failure mode over the
wire, where no rejection message exists.

Any acceptable design therefore has to answer this, e.g.:

1. **Refuse growth only.** Admit pushes whose changes cannot increase `Live`.
   Needs a cheap, conservative per-change classification on the push path, where
   there is no root to execute against — operation kind alone, not measured
   bytes.
2. **Refuse everything, define the recovery.** A distinct error code the SDK
   handles by surfacing a terminal "document over quota" state, plus an
   out-of-band way to shrink (compaction, admin deletion). Simple on the server,
   a real SDK change in both `yorkie` and `yorkie-js-sdk`.
3. **Do not refuse; detach.** Treat an over-quota document as a housekeeping
   target and remove or freeze it out of band, so the push path stays untouched.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Gate deadlocks a document that is already over quota | Decide the refusal semantics first; option 1 or 3 above, never a blanket refusal without a recovery path |
| Lagging size lets a document exceed the quota by one snapshot interval | Document the overshoot as part of the quota's contract; tighten `SnapshotInterval` for projects that care |
| A `DocInfo` schema addition has to be safe on documents written before it existed | Zero value means "unknown", which the gate treats as "admit"; the first `storeSnapshot` after upgrade populates it |
| Server and client disagree on the number, so an honest client is refused | Only refuse strictly above the limit, and keep the client check as the primary gate; the [rebuild-drift work](../tasks/) that makes the accumulator agree with a rebuild is a prerequisite for the two to be comparable at all |
| Compaction shrinks a document without touching the persisted size, so the gate goes on refusing growth on a document that is now small | Reset the field on every path that rebuilds or purges document internals — `CompactChangeInfos` in both drivers — as part of landing the gate, not after |
| A size source that rides the `docCache` inherits that cache's concurrency contract | `CreateChangeInfos` caches the caller's `DocInfo` by reference (`server/backend/database/mongo/client.go:1957`), so writing the field races every `FindDocInfoByRefKey` deep-copy, and invalidating the entry turns a later cache hit into an unlocked Mongo read that overwrites fresher state; pick a size source that is not the `docCache` |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Lagging gate over an exact one | An exact gate means building the document on every push; the whole point of the push path is that it is document-free |
| Persist on `DocInfo`, not in the snapshot cache | `be.Cache.Snapshot` is populated only by `BuildInternalDocForServerSeq`, which a push-only client never reaches, so a cache-backed gate is never armed in exactly the case that matters |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Cap the bytes of a single change pack | Covered by `maxRequestBytes` (`server/rpc/server.go:45`), and it bounds one push, not a quota reached over many |
| Read the size off `be.Cache.Snapshot` in `pushPack` | Free, but never warm for a client that only pushes — `storeSnapshot` builds its own document and does not populate that cache |
| Rebuild the document in `pushPack` | Correct and exact, but adds a snapshot load, a range query, a GC pass and two `DeepCopy` calls to every push |
| Leave enforcement client-side | What exists today; the quota is then advisory against anything but a stock SDK |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents. The
accounting half — making the running `DocSize` accumulator agree with a rebuild,
so that whatever number a gate reads means the same thing on both sides — is
tracked separately and is a prerequisite for, not part of, this proposal.
