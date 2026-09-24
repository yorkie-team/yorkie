**Created**: 2026-09-24

# Expose cache hit/miss as Prometheus metrics

**Issue:** yorkie-team/yorkie#1979. Related: #1957 (benchmarking the changes
path against the snapshot path).

`SnapshotThreshold` and `SnapshotInterval` are both fixed at 500
(`server/backend/database/project_info.go`) for every project, with no recorded
measurement behind either value. The signal you would need to tune them is the
snapshot cache's hit rate, and it is not observable today.

The cost asymmetry is the point. In `BuildInternalDocForServerSeq`
(`server/packs/snapshot.go:76`) a hit is one `DeepCopy` of the cached document;
a miss is `FindClosestSnapshotInfo` -> decompression -> deserialization
(`snapshot.go:88-102`) and then a replay of every change after the snapshot
(`snapshot.go:104-119`). Which of the two a deployment actually pays is
invisible.

**Goal:** `yorkie_cache_hits_total{cache,hostname}` and
`yorkie_cache_misses_total{cache,hostname}` on `/metrics`, without touching the
hot path.

## Why nothing exposes it today

The numbers are already collected. `pkg/cache/lru_with_stats.go:71,73` counts
hits and misses on every `Get`, and `pkg/cache/stats.go` exposes accessors.
Both exits are blocked:

1. **Registration.** The only caches passed to `RegisterCache` are the six
   MongoDB-layer ones (`server/backend/database/mongo/client.go:111-143`). The
   snapshot cache lives in a different struct that also happens to be called
   `Manager` — `server/backend/cache.Manager` holds the caches as fields
   (`server/backend/cache/manager.go:34-44`), while `pkg/cache.Manager` is the
   stats *reporter*. They are unrelated types, so the snapshot cache was never a
   candidate for registration.
2. **Output.** Even registered, `pkg/cache.Manager`'s only sink is
   `logger.Infof` behind `CacheStatsEnabled` (`pkg/cache/manager.go:77-92`).
   `server/profiling/prometheus/metrics.go` has no cache metric at all.

## Approach

A **custom `prometheus.Collector`** that reads `Stats()` at scrape time. The
alternatives and why not:

- *Incrementing at the call sites* — counts the same event in two places, and
  every future cache has to remember to do it.
- *`CounterFunc` per cache* — `...Func` variants take no label dimensions, so
  each cache needs its own instrument built with `ConstLabels`. One collector
  serves N caches through a `cache` label instead.

Reading at scrape time means the hot path keeps its two `atomic.AddInt64`s and
gains nothing else, and the counter cannot double-count.

Hits and misses only. `Len()` is free off the same interface, but an entry
count is not actionable without the capacity to compare it against, and the
sharded LRU's effective capacity is `(size / 16) * 16`, not `size`
(`pkg/cache/lru_with_stats.go:41`) — so `entries` and a companion capacity gauge
ship together in the follow-up, which needs `Cap()` on `StatsProvider` and
therefore also touches `ProjectCache`. Cache size and policy stay untouched
either way (#1979 non-goals).

**Labels:** `cache` and `hostname` only. Existing metrics carry
`project_id`/`project_name`, but these caches are node-level resources keyed by
`DocRefKey` — there is no project dimension to report, and synthesizing one
would multiply series by project count.

**Scope:** `StatsProvider` is satisfied by both `LRU`
(`pkg/cache/lru_with_stats.go:107-119`) and `LRUWithExpires`
(`pkg/cache/lru_with_expires.go:95-107`), so the collector is cache-agnostic.
Register the three backend caches now (`snapshots`, `auth-webhook`,
`session-count`); the six MongoDB caches are a follow-up, since they are built
inside `mongo.Dial`, which never receives `*prometheus.Metrics`.

## Checklist

- [x] `server/profiling/prometheus/cache.go` (new): `cacheCollector` over
      `[]cache.StatsProvider`, emitting hits and misses as counters labelled
      `cache` + `hostname`. Metric names via `prometheus.BuildFQName` off the
      existing `namespace` constant.
- [x] `server/profiling/prometheus/metrics.go`: `cacheLabel` constant, and
      `RegisterCaches(hostname string, caches ...cache.StatsProvider) error`
      registering the collector on the instance's own registry.
- [x] `server/backend/backend.go`: register `cacheManager.Snapshot`,
      `.AuthWebhook` and `.SessionCount` right after the cache manager is built
      (step 02, `:112`), using the `conf.Hostname` resolved in step 01.
- [x] Unit test in `server/profiling/prometheus`: a fake `StatsProvider`,
      gathered through `testutil`, asserting names, label values and that the
      values track the source counters. No DB, so `go test ./...` covers it.
- [x] `make lint`, `go test ./...`.
- [x] Scraped a locally running server (memory DB) and confirmed the three
      caches appear under the expected names and labels.
- [ ] Self review (`/self-review`), lessons in the paired file.

## Notes

- Only `Get` counts. `Peek`/`Contains` deliberately do not
  (`lru_with_stats.go:85,91`); the snapshot path uses `Get`, so the metric
  covers it.
- **A hit is not always a cheap path.** `BuildInternalDocForServerSeq` counts
  the hit at `snapshot.go:75`, then at `:87` discards a cached document whose
  checkpoint is ahead of the requested `serverSeq` and reads the snapshot from
  the database anyway — the `DeepCopy` at `:77` is thrown away. So the hit rate
  is an upper bound on how often the snapshot path stayed cheap. Reading a past
  revision (`server/documents/documents.go`) is the case that triggers it.
  Counting it as a miss would mean mutating stats from the call site, which is
  the double-counting this design avoids; the help text and the collector's
  doc comment state what is actually measured instead.
- `Stats.Reset()` (`stats.go:41`) would look to Prometheus like a process
  restart and break `rate()` across the gap. Nothing in production calls it
  today — leave it that way.
- Counters resetting to zero on restart is expected and `rate()` handles it.

## See Also

- yorkie-team/yorkie#1957 — a benchmark can set cache state directly; only
  production metrics say how often each case actually occurs.
