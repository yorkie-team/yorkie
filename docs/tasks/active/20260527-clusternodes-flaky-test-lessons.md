**Created**: 2026-05-27

# ClusterNodes Leadership Revocation Flaky Test — Lessons

## Check whether a recent production change redefined the invariant the test asserts

The test was written assuming that "DB reconnect → next cycle → new
lease token". #1804 ("Skip leadership write when active leader exists")
later added a `CountDocuments` pre-check that intentionally counts the
caller's own row, so a node with no in-memory lease but a still-valid
row in MongoDB waits for natural expiry instead of racing to refresh.
The test was not updated, and the small window between `updated_at`
expiry (200ms) and lease expiry (300ms) became a race that surfaced
intermittently.

**Rule**: when a test starts flaking after a production change, read
the commit that touched the relevant invariant before adjusting the
test. The test's wrong assumption may have been correct against the
old behavior.

## In-memory and on-disk leadership state can diverge

`becomeFollower()` clears `isLeader` and `lease` in memory but does
not touch MongoDB. The on-disk row keeps `is_leader=true` and the old
`lease_token` until the lease expires or another node takes over.
`FindClusterNodes` filters by `updated_at` window, so the row becomes
invisible without being cleared, and `updateClusterFollower`'s
`$setOnInsert` clause means a re-floated row is silently restored to
visibility with its old leadership fields intact.

When the predicate is "leader exists and looks healthy", a resurfaced
stale row trivially passes it. The fix was to widen the predicate to
include "and the token is different from what we last saw", which is
the property the test actually wanted to assert.

## Strengthen `Eventually` predicates with the property you are testing, not just liveness

The original predicate `len(infos) > 0 && infos[0].IsLeader` is true
for any leader-looking row, including a stale one. Adding
`infos[0].LeaseToken != prvToken` makes the predicate match the test's
intent: "a new leadership was acquired", not just "there is a leader".

The general pattern: if the test is verifying that state X changed,
the `Eventually` predicate should check for the change, not for the
post-change state alone. Otherwise a stuck pre-state that happens to
look like post-state passes.

## Snapshot timing matters when comparing against a live value

The first version of the fix captured `prvToken` from the result of
the first `Eventually`, but `Eventually`'s predicate read MongoDB at
some earlier point and a renewal cycle could have rotated the token
between that read and `SetDisconnected(true)`. A resurfaced stale row
holding the post-read token would then satisfy `LeaseToken != prvToken`
falsely.

The fix is to re-read the live row immediately before flipping the
disconnect switch, narrowing the race window from
`~renewalInterval` (100ms) to a few microseconds. The general rule:
when you compare a captured value against a live one, capture the
captured value as close to the change-of-state as possible.

## `assert.Eventually` timeout should cover the slowest legitimate path

The original 1s timeout was tight against the worst-case acquire delay
after reconnect:

```
worst case = renewalInterval (renewal phase) + leaseDuration
           + renewalInterval (acquire cycle) + window (200ms for row
             to reappear in FindClusterNodes) + polling jitter (50ms)
         ≈ 750ms
```

That left little headroom for GC pauses or MongoDB latency on loaded
CI. Bumping to 2s is generous but defensible. The rule is to size the
timeout from the slowest legitimate path the system is allowed to
take, not from the median.
