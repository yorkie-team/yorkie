**Created**: 2026-10-02

# Lessons — Auth webhook cache switch and cache TTL floor

## Notes

- hashicorp's expirable LRU starts its cleanup ticker at `ttl / 100`
  (`numBuckets`). A floor that only prevents the zero-interval panic
  (ttl < 100ns) does not prevent a busy ticker: 1ms gives a 10µs ticker,
  100k wakeups a second for the server's lifetime. 100ms keeps it at 1ms.
- Expiry on read does not depend on the ticker (`Get` compares
  `ExpiresAt`), so a short TTL in tests is safe at any floor; only memory
  reclamation waits for the ticker.
- A conflict resolution can silently drop one side's optimization: the
  merge kept `!cacheDisabled` on both branches of the logic but built the
  cache key and took the generation lock unconditionally. Diff the merged
  function against both parents, not only against main.
- `ProjectCacheTTL` in `config.sample.yml` sat under `Backend`, which has no
  such field, so YAML silently dropped it, and a `Mongo` section without the
  key exited in `ParseProjectCacheTTL("")`. Validation skipped the empty
  value, so nothing caught it before startup.

## Review

- Round 1 (`/code-review high`, merged tree): no correctness bugs in the
  conflict resolution; ten findings. Fixed the dropped guard, the TTL floor,
  the Mongo defaults, the handler `require`, the duplicated TTL rule, the
  drop-when-disabled scan, and the design doc.
- Kept: a TTL of 0 or below the floor fails startup. The release note marks
  it "Action required" and points at the new switch; mapping 0 to "disabled"
  would keep the trap the floor removes.
- Kept: config errors name the flag only. It is the convention of every
  field in `backend.Config.Validate` and `mongo.Config.Validate`; changing
  it belongs in its own PR across all fields.
- Kept: disabling at the cache layer (no LRU at all). `be.Cache.AuthWebhook`
  is read without nil checks in several places; the two flag checks in
  `verifyAccess` plus the early return in `DropCachedDecisions` cover every
  path that touches the cache.
