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
  known limitations: the client lock is held across one MongoDB round trip
  and ignores ctx; several server nodes still keep separate client caches,
  as before.
- Later review rounds (panel and loop fixers) found real gaps in how a
  cached `ClientInfo` authorizes requests across nodes, and each fix
  exposed the next one: an expiring cache, conditional write-backs, an
  uncached read before a push writes, then Watch/Broadcast checks, then
  the `ClusterService.Broadcast` bypass. Eight rounds later the PR was
  changing the server's authorization model. The maintainer re-scoped it
  back to the flakes and the per-client cache race, reverted the rest, and
  filed yorkie-team/yorkie#2113 with the evidence.
- When review findings keep growing past the stated scope, especially into
  behavior that existed before the PR, stop and ask for a scope decision
  instead of fixing one more round. File an issue with the reproduction.
- Deleting a cache fill removes a refresh path as well as a bug. Here it was
  the only path that refreshed a client written by another node; that gap
  is part of #2113.
- The striped lock became a per-client `pkg/locker` lock: same guarantee,
  no false sharing between unrelated clients, and the locking utility the
  server already uses.
