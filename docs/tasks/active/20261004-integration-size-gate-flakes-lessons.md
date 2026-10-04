# Lessons: integration flakes after the server-side size gate

**Created**: 2026-10-04

- `-count=N` failures and CI failures had different causes. The `-count`
  ones (key reuse) were easy to reproduce and would have hidden the CI one
  (a cache race) if fixed alone. Read the CI log's error, not just the test
  name.
- A flake that appears with an unrelated change can be load, not logic. The
  CI log had `failed to compact ... deadline exceeded` and a one-minute
  `CompactDocument` RPC next to the failure; that pointed at the leftover
  18MB document.
- "Write to MongoDB, then put the returned row in the cache" is two steps.
  Any cache filled that way needs the write and the fill under one lock per
  key, or a version to compare. A bulk read cannot take those locks, so it
  should not fill the cache.
- A stress test at the database layer (three goroutines on one client row)
  reproduced the race 5 of 5 times in under a second, where the full
  integration path needed hundreds of iterations.
- A wait on a background snapshot is not a timeout problem when the server
  may decide not to take the snapshot. Wait for the condition that makes
  the assertion meaningful (a snapshot at the current server seq) and drive
  it.

## Self review

- Round 1 (correctness/tests): no blocking findings. One should-fix: the
  first `waitForSnapshot` nudged on every poll where the snapshot lagged,
  and each nudge moves the server seq, so a snapshot slower than the poll
  interval could never catch up. Fixed: fix the target before waiting and
  nudge only when no snapshot landed since the last poll; the loop also no
  longer calls `require` inside `assert.Eventually`'s goroutine. Also fixed:
  `TestKey` dropped the run suffix for names cut at 100 characters. Kept as
  known limitations: the striped lock is held across one MongoDB round trip
  and ignores ctx; several server nodes still keep separate client caches,
  as before.
- Round 2 (review panel): two blocking findings, both on the removed bulk
  cache fill. The removal was a real behavior change with no test, and it
  also removed the only path that ever refreshed a client row on a node
  that did not write it — the LRU had no TTL and no cross-node
  invalidation, so a stale activation or attachment could sit in a node's
  cache until it was evicted by size. Fixed by giving `clientCache` a TTL
  (`DefaultClientCacheTTL`, 1m, configurable via `--mongo-client-cache-ttl`)
  instead of restoring the fill: expiry drops entries rather than writing
  old ones, so the staleness window is bounded without reopening the race.
  `ActivateClient` now also takes the client's stripe lock, so every
  clientCache write pairs with its row write under one lock.
- Round 3 (review panel): one blocking finding, raised by both the
  blast-radius and the security lens — the database confirmation added for
  push/pull covered only the paths that write, so `Watch`, `WatchDocument`,
  `WatchChannel` and `Broadcast` still admitted on the cached client row. A
  client deactivated on another node kept receiving document events and peer
  presence and kept broadcasting for up to `ClientCacheTTL`, and on a node
  that never writes nothing would ever disprove the entry. Fixed by reading
  the client row with `skipCache` at those four entry points
  (`confirmActiveClient`) and confirming the document attachment on that row
  for document watches (`confirmWatchTarget`, attaching allowed so a watch
  racing its own attach is not rejected, server clients exempt as in
  `pullPack`). `TestStaleClientCacheWatch` covers both.
- A write-path guard is not a gate. The read paths — streams, broadcasts —
  disclose the same resource and have no later conditional write to catch
  them, so they need the authoritative read themselves.
- Deleting a cache fill removes a refresh path as well as a bug. Ask what
  else was keeping the entry fresh before deciding the removal is free; for
  a cache nothing invalidates across nodes, the answer is usually "nothing,
  and it needs a TTL".
