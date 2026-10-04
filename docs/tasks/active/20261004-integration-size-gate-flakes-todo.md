# Integration flakes after the server-side size gate

**Created**: 2026-10-04

After #2108 two integration tests turned agent-loop CI red:

- `TestDocument/document_tombstone_test` failed with "document not
  attached" on a client's own PushPull (CI run 37169502104), and under
  `-count=5` failed on every rerun.
- `TestDocumentSizeGate/a_remove-only_pack...` sometimes timed out waiting
  5s for the live size to drop.

## Root causes

1. **Client cache race (server).** `mongo.Client` writes a client row and
   then puts the row it got back into `clientCache`, in two steps. Two
   requests of one client (the realtime sync loop pushing the watch-test
   document and the attach of the tombstone document) can finish out of
   order, so an older row lands in the cache after a newer one. A cache miss
   that reads the row while a write runs does the same. Every later request
   then reads the stale row: the attach exists in MongoDB, but
   `EnsureDocumentAttached` fails. Reproduced at the database layer by
   `TestClient_ClientCacheUnderConcurrentWrites`: 5 of 5 runs failed before
   the fix.
2. **Load from a leftover 18MB document (test).** The compaction test added
   in #2108 leaves an 18MB document in the shared database. Housekeeping
   on every server started against that database (the default one and the
   size gate's own) rebuilds it to fail compaction again, up to a minute
   each on CI. That CPU and MongoDB load widened the race window above;
   this is why the flake appeared with #2108.
3. **Skipped snapshot (size gate test).** `storeSnapshot` gives up when
   another snapshot of the document is being written. The attach and the
   delete in the remove-only subtest push back to back, so the delete's
   snapshot could be skipped and the live size never dropped: the 5s wait
   could not succeed at any length. Seen in 1 of 40 runs, with no
   `SNAP ... serverSeq: 3` in the log.
4. **Names reused across `-count` runs (test).** `helper.TestKey` returned
   `t.Name()`, so a rerun attached the previous run's documents, and the
   size gate test created a project with a fixed name.

## Plan

- [x] Serialize each client's row read/write with its cache update under a
      striped lock; stop filling the cache from the bulk
      `FindAttachedClientInfosByRefKey` read.
- [x] Regression test at the mongo layer.
- [x] Purge the oversized document when the compaction test ends.
- [x] Size gate test: wait for a snapshot that covers the last push, and
      re-trigger it with a presence-only change when it was skipped. Keep
      the size assertions, now exact.
- [x] `TestKey` and `TestSlugName`: suffix the run number from the second
      run of a test on; the size gate test names its project with
      `TestSlugName`.
- [x] Verify: `-count=10` of `TestDocumentSizeGate|TestDocument$`, 60 runs
      of the size gate test, full integration suite, lint, unit tests.

## Client cache staleness across nodes (review rounds)

Each node keeps its own client cache, and only the node that performs a
write updates it. The model this PR leaves:

- The cache expires (`--mongo-client-cache-ttl`, default 1m), so a node that
  did not perform a write reads a stale client row for at most the TTL.
- RPC gates (`EnsureActivated`, `EnsureDocumentAttached`) still read the
  cache, so within the TTL a stale node can admit a request.
- A push with changes, or one that removes the document, re-reads the client
  row from MongoDB under the document's push lock before writing, and is
  refused with `ErrClientNotActivated` / `ErrDocumentNotAttached` if the
  database no longer has the client activated with the document attached.
  The remaining window is that one read to the write, not the TTL.
- The client-row update after PushPull is conditional for everything but a
  detach (`status: activated` and the document attached or attaching), so a
  stale copy cannot write itself back. A detach stays unconditional.
- Pushes without changes are admitted on the cache alone. They write nothing
  to the document, and the conditional update stops them from changing the
  client row.
- With consistent hashing, a deactivation detaches each document through the
  node that owns it, so that node's cache sees the detach right away; the
  checks above cover requests that reach another node.

- [x] Pre-write client check in `pushPack`; conditional write-back in both
      backends; precondition stated on the `Database` interface.
- [x] Shared testcases for stale updates on a deactivated client and on a
      detached document; two-node integration test
      (`TestStaleClientCache`): without the pre-write check its changes land
      (stored change count 1 -> 3), with it they do not.

## Known limitation (not fixed here)

The skipped snapshot is also a product gap: an over-quota document whose
shrinking push loses its snapshot keeps refusing growth until some other
push writes a snapshot. The design doc says the gate lags "by up to one
SnapshotInterval"; with the skip it can lag until the next push.
