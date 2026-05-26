**Created**: 2026-05-27

# ClusterNodes Leadership Revocation Flaky Test

Branch: `fix-clusternodes-flaky-test`

## Goal

Eliminate the ~25–35% flake rate of
`TestClusterNodes/Leadership_revocation_and_reacquisition_after_temporary_DB_disconnection_test`
without touching production code, since the underlying behavior was
deliberately introduced by #1804 and the test's assumption was the
incorrect side.

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
