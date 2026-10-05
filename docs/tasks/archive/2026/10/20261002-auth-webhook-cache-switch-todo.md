**Created**: 2026-10-02

# Auth webhook cache switch and cache TTL floor (#2068, PR #2076)

A cached allow outlives a revocation for up to one `AuthWebhookCacheTTL`
on every RPC. Operators asked for a way to turn the cache off, and a TTL
of 0 already looked like one: hashicorp's expirable LRU reads it as
"never expire" instead.

## Plan

- [x] `--auth-webhook-cache-disabled` (`Backend.AuthWebhookCacheDisabled`):
      `verifyAccess` neither reads nor writes the cache
- [x] Floor every expirable-LRU TTL at `cache.MinTTL`, both in
      `NewLRUWithExpires` and in config validation, so startup names the flag
- [x] Merge main (#2095 Watch revalidation): skip the cache read on
      `recheck || disabled`, the write on `disabled`

## Review follow-ups (maintainer rework)

- [x] Restore the guard the merge dropped: no cache key or cache-generation
      read when the cache is disabled
- [x] Raise `MinTTL` from 1ms to 100ms: the LRU ticks at TTL/100, so 1ms
      still woke a 10µs ticker
- [x] Share the TTL rule through `cache.ParseTTL` instead of two copies
- [x] Default `Mongo.ProjectCacheSize`/`ProjectCacheTTL`; move them to the
      `Mongo` section of `config.sample.yml`, where they are read
- [x] `DropCachedDecisions` returns at once when the cache is disabled
- [x] Integration test handler reports with `assert`, not `require`
- [x] `docs/design/watch-access-revalidation.md` names the switch

## Known limitations

- The auth webhook LRU, its ticker and its metrics still exist when the
  cache is disabled; the metrics then read 0 hits and 0 misses.
- Config errors name the CLI flag even for a YAML value, as every other
  field in these `Validate` methods does.
