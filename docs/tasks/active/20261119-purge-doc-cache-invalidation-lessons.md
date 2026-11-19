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

## Self review

Not run: this branch was produced by the autonomous agent workflow, which is
granted no tool that can launch a reviewer subagent. Review is CI, `@claude
review` and a human on the PR.
