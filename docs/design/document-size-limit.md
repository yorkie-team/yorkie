---
title: document-size-limit
target-version: 0.7.24
---

# Document Size Limit

> **Status: implemented.** The lagging, growth-only gate described below is in
> `server/packs/docsize.go`. The two alternatives it was chosen over are kept in
> "What refusing does to the client" so the decision is not re-litigated in
> every review that touches document accounting.

## Problem

`MaxSizePerDocument` is a per-project quota on a document's size
(`server/backend/database/project_info.go:136`, default 10 MiB). It is sent to
the client in the attach response
(`server/rpc/yorkie_server.go:353` → `client/client.go:518`) and enforced there:
`Document.Update` compares the clone's `resource.DocSize.Total()` against
`MaxSizeLimit` and refuses the local update with `ErrDocumentSizeExceedsLimit`
(`pkg/document/document.go:258`).

Until this design landed that was the *only* enforcement. `pushPack` filtered
already-pushed changes, validated clientSeq continuity, serverSeq ordering and
epoch, and handed the remainder to `CreateChangeInfos`. No branch on that path
read the document's size, because no branch on that path has a document at all.

So the quota was advisory. A client that does not run the check — a modified
SDK, or a direct Connect call — could grow a document without bound, and the
only things standing in the way were the per-request byte cap and MongoDB's
16 MB BSON limit on the snapshot (see
[snapshot-overflow.md](snapshot-overflow.md)), which bites long after the quota
was meant to.

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

The shape that ships is a **lagging gate**: the path that already builds the
document persists its size, and `pushPack` reads the persisted number.

- `storeSnapshot` (`server/packs/snapshot.go`) already materializes the document
  every `SnapshotInterval` changes and already writes a row. It now also writes
  `doc.Root().DocSize().Total()` onto `DocInfo` via `UpdateDocInfoSize`.
- `pushPack` already fetches `currentDocInfo` under `DocPushKey` whenever there
  is anything to push, so reading the field there costs nothing extra.

`DocInfo.DocSize` is carried through both backends (`mongo` and `memory`) and
`DocInfo.DeepCopy`. Its zero value means "never measured" and the gate admits
it, which is also what every document written before the field existed reads as.

The size write is deliberately *not* part of the push path's compare-and-set: it
is a `$set` on `doc_size` alone, with no `server_seq` guard, and it drops the
`docCache` entry rather than re-adding a `DocInfo` read outside `DocPushKey`
(re-adding could overwrite a fresher entry and make the next
`CreateChangeInfos` fail with `ErrConflictOnUpdate`).

**What it bounds.** The size the gate sees is at most `SnapshotInterval` changes
stale, so a document can overshoot the quota by whatever one snapshot interval's
worth of pushes adds, each push itself capped by the request byte limit. That is
a bound; the status quo has none. The overshoot is the price of not rebuilding
on every push, and it should be stated in the quota's documentation rather than
hidden.

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

Three answers were on the table:

1. **Refuse growth only.** Admit pushes whose changes cannot increase the
   document. Needs a cheap, conservative per-change classification on the push
   path, where there is no root to execute against — operation kind alone, not
   measured bytes.
2. **Refuse everything, define the recovery.** A distinct error code the SDK
   handles by surfacing a terminal "document over quota" state, plus an
   out-of-band way to shrink (compaction, admin deletion). Simple on the server,
   a real SDK change in both `yorkie` and `yorkie-js-sdk`.
3. **Do not refuse; detach.** Treat an over-quota document as a housekeeping
   target and remove or freeze it out of band, so the push path stays untouched.

**Option 1 is what ships**, because it is the only one that needs no wire
change. `mayGrowDocument` (`server/packs/docsize.go`) classifies a change by
operation kind and payload and errs toward "grows":

- `Remove` never grows — it moves an element's bytes from `Live` to `GC`.
- `Edit` and `TreeEdit` do not grow when they carry no content, no attributes,
  no split level and no restore/retombstone spans; that is a pure deletion.
- Everything else — `Set`, `Add`, `Move`, `Increase`, `ArraySet`, `Style`,
  `TreeStyle`, and anything added later — counts as growth by default.
- A change with no operations (presence-only) cannot touch the root.

A pack is refused whole rather than partially, because `clientSeq` continuity is
validated per pack and dropping one change out of the middle would break it. The
refusal is `ErrDocumentSizeExceedsLimit`, a `pkg/errors` `StatusError` with
status `ResourceExhausted`, so `connecthelper` attaches the custom code as
`ErrorInfo` metadata and the SDK sees the same name its local check raises.

An honest client is never refused by this gate before its own check has fired:
the server's number lags and the client's does not, so the client's exact
comparison against the same limit always trips first.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Gate deadlocks a document that is already over quota | Option 1: deletions and presence are always admitted, so the server never refuses the push that would shrink the document |
| Lagging size lets a document exceed the quota by one snapshot interval | Document the overshoot as part of the quota's contract; tighten `SnapshotInterval` for projects that care |
| A `DocInfo` schema addition has to be safe on documents written before it existed | Zero value means "unknown", which the gate treats as "admit"; the first `storeSnapshot` after upgrade populates it |
| Server and client disagree on the number, so an honest client is refused | Only refuse strictly above the limit, and keep the client check as the primary gate; the [rebuild-drift work](../tasks/) that makes the accumulator agree with a rebuild is a prerequisite for the two to be comparable at all |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Lagging gate over an exact one | An exact gate means building the document on every push; the whole point of the push path is that it is document-free |
| Persist on `DocInfo`, not in the snapshot cache | `be.Cache.Snapshot` is populated only by `BuildInternalDocForServerSeq`, which a push-only client never reaches, so a cache-backed gate is never armed in exactly the case that matters |
| Classify by operation kind, never by measured bytes | The push path has no root; measuring would mean rebuilding, which is the cost the lagging gate exists to avoid |
| Refuse the whole pack, not the growing change | `validateClientSeqContinuity` requires the pack's clientSeqs to be contiguous, so a partial accept would corrupt the client's checkpoint |
| Return a `StatusError`, not a pre-wrapped `connect.Error` | `connecthelper.ToStatusError` returns an already-`connect.Error` untouched, which would drop the `ErrorInfo` metadata carrying the custom code |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Cap the bytes of a single change pack | Already effectively covered by the request size limit, and it bounds one push, not a quota reached over many |
| Read the size off `be.Cache.Snapshot` in `pushPack` | Free, but never warm for a client that only pushes — `storeSnapshot` builds its own document and does not populate that cache |
| Rebuild the document in `pushPack` | Correct and exact, but adds a snapshot load, a range query, a GC pass and two `DeepCopy` calls to every push |
| Leave enforcement client-side | What existed before this document; the quota is then advisory against anything but a stock SDK |

## Known Limitations

- The gate is armed only once a document has been snapshotted at least once. A
  document that reaches the quota inside its very first `SnapshotInterval` is
  not refused until the first `storeSnapshot` runs.
- The bound is `quota + one snapshot interval of pushes`, not the quota. Projects
  that need a tighter bound tighten `SnapshotInterval`.
- A non-cooperating client whose push is refused has no protocol-level way to
  roll the change back; it retries and its checkpoint does not advance. That is
  the same terminal state a stock SDK reaches locally
  (`pkg/document/document.go:258`), reached over the wire instead. Turning it
  into a clean, recoverable state is the SDK-side work option 2 describes.

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents. The
accounting half — making the running `DocSize` accumulator agree with a rebuild,
so that whatever number this gate reads means the same thing on both sides — is
tracked separately.
