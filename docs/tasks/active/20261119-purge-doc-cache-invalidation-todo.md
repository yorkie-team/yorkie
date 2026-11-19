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

- [ ] Add `DocRefKey.CacheKey()` and `ParseDocRefKey` to
      `api/types/resource_ref_key.go`, mirroring the client ref key pair.
- [ ] Handle `types.CacheTypeDocument` in `mongo.Client.InvalidateCache`.
- [ ] Drop the `docCache` entry in `mongo.Client.PurgeDocument`.
- [ ] Broadcast `CacheTypeDocument` invalidation from `packs.Purge` after the
      purge lands; log failures rather than returning them, as the client
      broadcast does — the delete has already happened.
- [ ] Tests: ref-key parse round trip; warm the document cache in
      `testcases.RunPurgeDocument` before purging so a cache-first read of a
      purged document fails the suite.

## Out of scope

- A per-document cache lock (the `clientLocks` equivalent). Every existing
  `docCache.Remove` has the same read-fills-after-remove window; closing it is
  a separate change.
- `UpdateDocInfoStatusToRemoved` and the other non-purge write paths; they
  already remove their entry locally.
