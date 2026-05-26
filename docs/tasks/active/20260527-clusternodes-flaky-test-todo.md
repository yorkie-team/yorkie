**Created**: 2026-05-27

# Flaky Test Fixes: Leadership Revocation and LRU Hit Rate

Branch: `fix-clusternodes-flaky-test`

## Goal

Eliminate two pre-existing flakes in the test suite, both caused by
tests making assumptions that hold only under specific timing or
hashing luck. No production code changes.

1. `TestClusterNodes/Leadership_revocation_and_reacquisition_after_temporary_DB_disconnection_test`
   flaked ~25–35% due to a race between `FindClusterNodes`'s
   `updated_at` window (200ms) and `lease_duration` (300ms), combined
   with the intentional "wait for natural expiry" behavior added in
   #1804.
2. `TestLRUWithStats/hit_rate_calculation` flaked ~18% due to using a
   cache size smaller than the shard count (sharded LRU floors
   per-shard capacity at 1; `maphash` randomizes shard mapping per
   process so hash collisions between the three added keys cause
   evictions in ~18% of CI runs).

## Done

- [x] Root-cause investigation:
  - [x] Confirm `becomeFollower()` only mutates in-memory state and does
        not clean up the node's own row in MongoDB
  - [x] Confirm `tryAcquireLeadership` pre-check counts the caller's own
        row as an active leader (intentional per #1804)
  - [x] Confirm `updateClusterFollower` uses `$setOnInsert`, so a
        re-floated row keeps the old `lease_token` and `is_leader=true`
  - [x] Confirm the race window: `updated_at` window (200ms) expires
        before lease (300ms), so the test reconnects while the old lease
        is still considered active
- [x] Test fix in `test/integration/clusternodes_test.go`:
  - [x] Strengthen second `Eventually` predicate with
        `infos[0].LeaseToken != prvToken`
  - [x] Bump second `Eventually` timeout from 1s to 2s to cover natural
        lease expiry plus next renewal cycle
  - [x] Add NOTE comment explaining the design constraint
  - [x] Re-read leader immediately before `SetDisconnected(true)` so
        `prvToken` reflects MongoDB's live value (eliminates the
        secondary race noted in self-review)
- [x] Verify: 30 consecutive runs of the subtest pass; 3× full
      `TestClusterNodes` pass; `make lint` clean
- [x] Self-review via code-review subagent (no Critical/Important; one
      Minor applied)
- [x] LRU hit-rate flake investigation:
  - [x] Confirm sharded structure (`numShards = 16`) and per-shard
        flooring in `pkg/cache/lru_with_stats.go:40-60`
  - [x] Confirm `var hashSeed = maphash.MakeSeed()` randomizes per
        process, explaining CI-only flake
  - [x] Reproduce locally with `go clean -testcache` between fresh
        runs: 6/30 fail (20%, matches the 18% theoretical rate)
- [x] LRU hit-rate fix in `pkg/cache/cache_test.go`:
  - [x] Bump test cache size from 5 → 64 so per-shard capacity (4) is
        large enough to hold all three added keys even on worst-case
        shard collision
  - [x] Comment explains the sharded sizing constraint
- [x] Verify LRU fix: 30/30 fresh processes pass; full `pkg/cache`
      package green; `make lint` clean

## Remaining

- [ ] PR review and merge

## Notes

- Production code is intentionally not modified. #1804 explicitly
  documents that a node with empty in-memory token waits for natural
  lease expiry instead of racing to refresh, to preserve the
  "at most one process believes it holds the lease" invariant.
- A future enhancement could let a node skip the wait for its own row
  (e.g., exclude `rpc_addr == self` from the pre-check, or have
  `becomeFollower` clear the row), but that would touch a recently
  hardened code path and needs its own design discussion.
