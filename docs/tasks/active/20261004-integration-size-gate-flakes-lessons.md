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
