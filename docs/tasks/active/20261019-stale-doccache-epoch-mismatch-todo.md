# A stale docCache entry fails an attach with ErrEpochMismatch

**Created**: 2026-10-19

Found as an integration-lane failure on an unrelated branch
(`TestAuthWebhookPresenceOnly/a reader attaching alone keeps the bound
schema`, CI run 39215563752) and **pre-existing on `main`**: nothing in that
branch touches `server/`, epochs, or the document cache, and the same test
passed on the commit before it. Filed separately because the fix is in the
Mongo `docCache` and the housekeeping compactor, and because containing it
means deciding how a cached `DocInfo` is allowed to go backwards.

## Problem

One `AttachDocument` RPC reads the document twice:

- `documents.FindOrCreateDocInfo` (`server/rpc/yorkie_server.go:324`), whose
  `docInfo.Epoch` seeds the client row in `clients.AttachDocument` →
  `ClientInfo.AttachDocument` (`server/backend/database/client_info.go:230`).
- `pushPack`'s own `be.DB.FindDocInfoByRefKey` under `DocPushKey`
  (`server/packs/pushpull.go:400`), whose `currentDocInfo.Epoch` is compared
  against the client row.

`FindDocInfoByRefKey` reads through an LRU with no version guard
(`server/backend/database/mongo/client.go:1730-1755`): a miss issues the
Mongo read, then unconditionally `docCache.Add`s whatever came back. A read
that started before a compaction therefore repopulates the entry with the
pre-compaction `DocInfo` *after* the compactor removed it, and the entry
stays stale until the next write or eviction.

Housekeeping compaction is what raises `Epoch`, and it opens that window
itself: `CompactChangeInfos` drops the cache entry *before* the `$inc` that
bumps the epoch (`mongo/client.go:2137-2149`; the memory backend does the
same at `memory/database.go:1955`), so a read issued anywhere between the
`Remove` and the commit is already doomed to cache a pre-compaction copy. The
two reads of the attach then straddle the window in the one order the epoch
check cannot explain:

```text
WARN  r41  discarding 1 changes from stale epoch: client(1) != doc(0)
INFO  r41  RPC : "/yorkie.v1.YorkieService/AttachDocument" => "client epoch(1)
           != document epoch(0): epoch mismatch"
```

The client row is *ahead* of the document. The seeding read saw the
compacted document (epoch 1); the locked read in `pushPack` was served the
resurrected cache entry (epoch 0). `pushpull.go:390-396` already names half of
this hazard — "a concurrent goroutine's stale MongoDB read can overwrite a
fresher cache entry, causing `ErrConflictOnUpdate` in `CreateChangeInfos`" —
and takes the push lock to contain it. The lock does not help here: the stale
write comes from a goroutine that is not holding it, and the two reads being
compared are in different lock scopes to begin with.

The attach's `RLock` on `DocKey` (`yorkie_server.go:299`) is no defense
either, for the same reason.

## Blast radius

`ErrEpochMismatch` on an attach is not a retry-and-recover for a client that
is attaching fresh: the document it attached is handed no ruleset, the detach
that follows reports `resource is not attached`, and the client's later
`DeactivateClient` fails with the `ErrConflictOnUpdate` the same stale entry
produces in `CreateChangeInfos`:

```text
RPC : "/yorkie.v1.ClusterService/DetachDocument" => "create changes of
      Document (...): conflict on update"
RPC : "/yorkie.v1.YorkieService/DeactivateClient" => "failed_precondition:
      create changes of Document (...): conflict on update"
```

So one stale entry strands the client row in `attaching` and leaves the
deactivation half done. Reaching it needs a compaction to land inside the
attach, which the test config makes easy (`HousekeepingInterval` 500ms,
`CompactionMinChanges` 5 in `test/helper/helper.go`) and a production
interval makes rare — rare, not impossible, and silent until a client fails
to detach.

## Tasks

- [ ] Stop `FindDocInfoByRefKey` from repopulating the cache with a read that
      is older than what is already there, or than an invalidation that
      arrived while it was in flight. A monotonic guard on `server_seq`
      /`epoch` at `docCache.Add`, or a generation counter bumped by every
      `docCache.Remove`, both fit; the second does not assume the fields only
      ever rise.
- [ ] Have `CompactChangeInfos` invalidate after its commit point as well as
      before it. Removing only before the `$inc` leaves every read in flight
      across the update free to cache the old copy; a second `Remove` after
      `MatchedCount` is checked closes the half of the window that no
      ordering guard on the read side needs to cover.
- [ ] Decide whether the epoch an attach seeds and the epoch `pushPack`
      checks should come from one read at all. Comparing two reads of the
      same document inside one RPC is what turns a stale entry into a
      mismatch; threading the `DocInfo` the attach already has into
      `pushPack` removes the second read instead of hardening it.
- [ ] Audit the other `docCache.Add` sites for the same shape
      (`client.go:2074` writes the post-update `docInfo`, which is fine; the
      read path is the one with no ordering).
- [ ] Pin it with a test that compacts a document between the two reads of
      one attach and asserts the attach succeeds — today it needs the
      housekeeping race to reproduce, which is why it surfaced as a flake on
      someone else's branch.
