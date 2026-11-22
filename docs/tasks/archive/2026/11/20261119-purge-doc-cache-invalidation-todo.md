# Invalidate the document cache on purge

**Created**: 2026-11-19

## Context

Issue #2346. `Client.PurgeDocument` (`server/backend/database/mongo/client.go`)
deletes the document row with `DeleteOne` and drops the change, presence and
vector caches through `purgeDocumentInternals`, but never drops the per-node
`docCache` entry. `FindDocInfoByRefKey` answers from that cache first, so after
a purge the document still reads back as present on any node that had read it
— including the purging node itself, since `clusterServer.PurgeDocument` reads
the docInfo (and so fills the cache) right before purging.

`types.CacheTypeDocument` already exists and already round-trips through the
converters, but nothing implements it: `Client.InvalidateCache` handles only
`CacheTypeProject` and `CacheTypeClient`, and no purge path broadcasts.

## Goal

A purged document reads as gone on every node, not just in MongoDB.

## Tasks

- [x] Add `DocRefKey.CacheKey()` and `ParseDocRefKey` to
      `api/types/resource_ref_key.go`, mirroring the client ref key pair.
- [x] Handle `types.CacheTypeDocument` in `mongo.Client.InvalidateCache`.
- [x] Drop the `docCache` entry in `mongo.Client.PurgeDocument`.
- [x] Broadcast `CacheTypeDocument` invalidation from `packs.Purge` after the
      purge lands; log failures rather than returning them, as the client
      broadcast does — the delete has already happened.
- [x] Add `docLocks`, the `clientLocks` equivalent for the document cache, and
      hold it in `FindDocInfoByRefKey` across the MongoDB read and the `Add`
      that follows, and at both removal sites. Removing after the delete is
      not enough on its own: a read that fetched the row first can `Add` that
      pre-purge copy afterwards, and `docCache` has no TTL to expire it.
- [x] Tests: ref-key parse round trip; warm the document cache in
      `testcases.RunPurgeDocument` before purging, and run that testcase
      against the mongo client, which is the only backend with a document
      cache; cross-node invalidation and a read racing a purge in
      `mongo/client_test.go`.

## Out of scope

- The non-purge `docCache.Remove` sites (`UpdateDocInfoStatusToRemoved`,
  `FindOrCreateDocInfo`). They now race the lock-holding read rather than an
  unlocked one, which is no worse than before, but bringing them under
  `docLocks` touches write paths this change does not otherwise reach.
- Invalidating the snapshot, change, presence and vector caches cluster-wide.
  Only `docCache` is broadcast here; the rest are dropped on the purging node
  by `purgeDocumentInternals`.
