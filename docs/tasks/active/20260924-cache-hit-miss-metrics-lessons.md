**Created**: 2026-09-24

# Lessons — Expose cache hit/miss as Prometheus metrics

Paired with `20260924-cache-hit-miss-metrics-todo.md`.

## Self review

Reviewer: the harness's own `/code-review` at `high`, over
`git diff origin/main...HEAD`. **Not** the six-lens panel from
`.github/workflows/agent-review-on-demand.yml` — that runs in CI on
`@claude review` after the PR exists and has no local runner
(`docs/design/agent-command-verbs.md`).

### Round 1 — correctness, test adequacy

Three blocking findings, no hard correctness defect. All three fixed.

1. **The `hits_total` help text claimed more than the counter measures**
   (medium). `BuildInternalDocForServerSeq` increments the hit at
   `server/packs/snapshot.go:75` on key presence, then at `:87` re-checks
   `serverSeq < doc.Checkpoint().ServerSeq` and takes the full database path
   anyway, discarding the `DeepCopy` from `:77`. Confirmed against the code.
   Since the whole point is to judge whether the snapshot path is cheap, an
   "lookups served from the cache" help text overstates it.
   *Fixed* by saying what is counted — "lookups that found an entry" — and
   documenting the gap on `cacheCollector` and in the todo. Not fixed by
   reclassifying the stale-entry case as a miss: that needs the call site to
   mutate stats, which is exactly the double-counting this design exists to
   avoid.
2. **`yorkie_cache_entries` was not actionable** (low). The todo justified the
   gauge with "know whether the cache is full", but the sharded LRU's real
   capacity is `(size / 16) * 16` (`pkg/cache/lru_with_stats.go:41`) — 992 for
   the default 1000 — and per-shard eviction makes it plateau lower still under
   key skew. *Fixed by removing the gauge.* Emitting a companion capacity needs
   `Cap()` on `pkg/cache.StatsProvider`, which drags in `ProjectCache`
   (`server/backend/database/mongo/project_cache.go`) and all six Mongo caches;
   that is blast radius this branch should not take, so the pair ships in the
   follow-up.
3. **Duplicate cache names would 500 the whole `/metrics` endpoint** (low).
   `cache` is the only label telling two caches apart, so a collision makes
   `Registry.Gather()` error, and `promhttp.HandlerOpts{}` at
   `server/profiling/server.go:59` defaults to failing the entire response —
   every unrelated metric disappears. Latent today (three distinct names), but
   `RegisterCaches` is variadic and the follow-up adds six more.
   *Fixed* by rejecting duplicate names in `RegisterCaches`, with a test
   asserting the endpoint still gathers cleanly after the rejection.

Nothing disputed, nothing deferred except the capacity gauge above.

Re-verified after the fixes: `make lint` clean, `go test ./...` clean.

### Round 2 — design fit, simplification, blast radius

Two findings, both low, both about what this API shape does to the follow-up
rather than to this diff. Nothing disputed.

1. **The collector aliased the caller's slice** (low, latent). `Collect` walks
   `c.caches` on scrape goroutines for the life of the process. Today's only
   caller passes three explicit arguments, so Go allocates a fresh array and
   nothing can mutate it — but the planned follow-up builds a
   `[]cache.StatsProvider` for the Mongo caches and spreads it, which shares the
   backing array with a live collector. *Fixed* with `slices.Clone`, pinned by a
   test that overwrites the caller's slice after registering and asserts the
   originally registered cache is still the one reported. Verified the test
   fails without the clone.
2. **One-call registration blocks the follow-up** (low). `RegisterCaches`
   succeeds once per `*Metrics`; a second call describes the same metrics and
   gets `AlreadyRegisteredError`, which `backend.New` turns into a startup
   abort. Registration sits at step 02, while `mongo.Dial` builds the six Mongo
   caches at step 04. *Deferred, with the decision recorded:* the follow-up
   moves the single call below `mongo.Dial` and passes all nine caches, rather
   than making the collector append-capable. An append-capable collector needs a
   mutex around a slice read on every scrape, to buy a second call site that
   nothing needs — the registration reads better next to the caches it names.
   The contract is now stated on the method instead of being discoverable only
   by hitting the abort. Carried into the PR body as a known limitation.

Re-verified after the fix: `make lint` clean, `go test ./...` clean,
`go test -race ./server/profiling/prometheus/` clean.

### Round 3 — security, docs, design-doc consistency

**No blocking findings. The loop ends here.** Two non-blocking observations,
both carried into the PR body as known limitations rather than fixed:

1. `RegisterCaches` calls `Name()` on each element with no nil guard, so a nil
   `StatsProvider` would panic inside `backend.New`, and one that slipped
   through would panic on a scrape goroutine in `Collect`, where neither
   `Gather` nor `promhttp` recovers. Unreachable today — `backend/cache.New`
   errors out if any of the three caches fails to build. Reachable in the
   follow-up, which builds the list programmatically.
2. Observability wiring is now fatal to startup, and `backend.New` dereferences
   `metrics` during construction where it previously never did. Fail-fast on
   what can only be a programming error is the intended behavior, but it is a
   decision, not a side effect.

Docs checked clean: no Grafana dashboard, helm chart or `docs/design/` file
enumerates metric names, so the new metrics leave nothing stale. The
`yorkie-monitoring` chart's ServiceMonitor scrapes the endpoint whole, and its
only dashboard configmap belongs to the mongodb-exporter.

## Notes

- The reviewer reported that `ReportFindings` was unavailable and returned prose
  instead. Findings were still actionable; worth knowing the tool can be absent.
- A test that passes tells you nothing until you have seen it fail. Flipping an
  expected counter to a wrong value confirmed `testutil.GatherAndCompare` was
  actually comparing and not silently filtering every metric away.
