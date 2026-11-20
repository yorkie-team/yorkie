# Lessons: Invalidate the document cache on purge

**Created**: 2026-11-19

## Notes

- `types.CacheTypeDocument` and its converters already existed; only the
  backend and the purge path were missing. No protobuf change was needed.
- The purging node is the one most certain to hold a stale entry:
  `clusterServer.PurgeDocument` reads the docInfo through
  `documents.FindDocInfoByRefKey` to compare the key before purging, which
  fills that node's `docCache`. So the local `Remove` matters even in a
  single-node deployment, where `BroadcastCacheInvalidation` may reach nobody.

- Removing the cache entry after the delete is not by itself enough. The read
  path is an unlocked miss → `FindOne` → `Add`, so a read that fetched the row
  before the delete can land its copy after the `Remove`. `clientCache` solved
  the same problem with `clientLocks`; `docCache` now has `docLocks`, held by
  the read across its `FindOne` and `Add` and by both removal sites.
- `testcases.RunPurgeDocument` was wired only to the memory database, which
  has no cache, so warming the cache in it asserted nothing. A testcase that
  exists to pin cache behavior has to be run against the backend that caches.

## Self review

Not run: this branch was produced by the autonomous agent workflow, which is
granted no tool that can launch a reviewer subagent. Review is CI, `@claude
review` and a human on the PR.
