---
title: project-stats-long-retention
target-version: 0.7.18
---

# Project Stats Long-Retention Windows

## Problem

[Project Stats Warehouse MV](project-stats-warehouse-mv.md) put the six
warehouse-backed metrics — active users, documents, clients, channels, sessions,
and peak sessions per channel — on synchronous HLL materialized views
(`mv_*_hll_daily`). A synchronous view is a rollup index physically part of its
base table: it shares the base partitions and the base lifetime. StarRocks
rewrites the `DATE(timestamp)` queries onto it at read time, so nothing in Go
reads the view directly.

That design left event retention as an explicit Non-Goal, and its Risks section
flagged what happens when retention arrives. The event tables carry no
`PARTITION BY` and no TTL today, so nothing is purged and every metric window is
served from the full history. The dashboard renders windows up to 12 months.

Retaining raw events for 12 months is expensive — the largest event table is
already past a billion rows — while the daily HLL summary is orders of magnitude
smaller (a billion `session_events` rows roll up to a few million daily sketch
rows, most tables to a few thousand). The moment event retention is added
(partition the base tables by day, drop old partitions), a synchronous rollup
loses those days with its base, and any window longer than the base retention —
the 3- and 12-month views — breaks.

**The daily summary has to outlive the base table.**

### Goals

- Serve windows up to 12 months after raw-event TTL is enabled, with latency
  driven by the number of days in the window, not the number of events.
- Keep the numbers identical to what the MV path returns today.
- Bound raw-event storage to a short retention while retaining daily summaries
  for the full product window.

### Non-Goals

- Exact distinct counts. HLL error (~0.2–1.7%) was accepted in v0.7.15.
- Changing the metric set, the API, or the `MetricPoint` shape.
- Per-project or viewer-chosen local-day boundaries. Days stay UTC, as in the
  MV design; the reserved ingest-time-local-date path there still applies.

## What doesn't work: an asynchronous MV

The obvious idea is a StarRocks asynchronous materialized view — a separate table
with its own partitioning and TTL and automatic query rewrite. Tested on
StarRocks 3.3.9:

- Auto query-rewrite works well, even rewriting raw-timestamp predicates onto
  the MV.
- Retention does **not** decouple. A partitioned async MV tracks its base
  partitions: when a base partition is dropped, the next refresh (manual `SYNC`
  or triggered by ingest) drops the corresponding MV partition too. The async MV
  cannot keep a day whose base partition was purged.
- Minimal repro: partitioned base + partitioned async MV with
  `partition_ttl_number`; drop an old base partition; `REFRESH ... WITH SYNC
  MODE`; the MV loses that partition.

The TTL knobs also point the wrong way. `partition_ttl` / `partition_ttl_number`
retain only the *most recent* N partitions, and StarRocks documents that outdated
MV partitions "will not be involved in the query plan, and the query will be
executed on the base tables to guarantee the consistency of data"
([partitioned MV docs][smv]). That is the opposite of what long retention needs —
the MV holding *fewer* days than the base and deferring the rest to a base that,
once purged, has nothing left to give back. An async MV is structurally a
hot-data optimization, not a long-retention store.

So the summary cannot be anything StarRocks maintains as a dependent of the base
table. It has to be a plain table the base cannot reach.

## Design: decoupled daily summary + dual read

Keep the synchronous MVs for the fresh path. Add an independent per-metric
summary table with its own long TTL, fill it with a scheduled idempotent job,
and split every read at the summary's coverage boundary: history from the
summary, the days it does not hold yet from the MV.

### Summary tables

One `AGGREGATE KEY` table per event table, mirroring the five MVs, each holding a
day-grained `HLL_UNION` sketch:

```sql
CREATE TABLE sum_user_hll_daily (
    project_id VARCHAR(64),
    dt         DATE,
    user_hll   HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES (
    "replication_num"       = "1",
    "partition_live_number" = "465"   -- ~15 months
);
```

`sum_document_hll_daily` and `sum_channel_hll_daily` follow the same shape.
`sum_session_hll_daily_ch` adds `channel_key` to the key, so it serves both the
sessions metric and peak sessions per channel. `sum_client_hll_daily` adds
`event_type`, matching the MV that carries it.

#### The session summary carries a coarse rollup

`channel_key` in the key is what peak sessions per channel needs, and it is also
what makes the plain sessions total and series expensive: they union every
channel-day sketch of a project just to count distinct sessions. On a project
with high channel cardinality that is millions of sketches per read, which alone
overruns the dashboard's per-query budget.

The table therefore declares a rollup keyed by `(project_id, dt)`:

```sql
CREATE TABLE sum_session_hll_daily_ch (
    project_id  VARCHAR(64),
    dt          DATE,
    channel_key VARCHAR(128),
    session_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt, channel_key)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
ROLLUP (rl_session_daily (project_id, dt, session_hll))
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");
```

`(project_id, dt)` is a key prefix, so StarRocks picks `rl_session_daily` for any
read that does not mention `channel_key` — the sessions total and series — and
keeps the base index for the peak metrics that do. Verified with `EXPLAIN`: the
sessions history half reads `rollup: rl_session_daily`, one row per day, while
peak still reads `rollup: sum_session_hll_daily_ch`.

A rollup rather than a second summary table: StarRocks maintains it on insert, so
the refresh job, the read path, and the retention property all stay as they are —
no second table to backfill, schedule, and keep in sync. A cluster whose summary
table predates this rollup adds it in place, which is one scan of the summary:

```sql
ALTER TABLE sum_session_hll_daily_ch
    ADD ROLLUP rl_session_daily (project_id, dt, session_hll);
```

This is the pattern StarRocks recommends directly: for HLL distinct counts, "when
the data volume is large, it is better to create a corresponding rollup table for
high frequency HLL queries" ([HLL docs][hll]). The summary table is that rollup,
kept independent so it can also outlive the base.

Two properties do the work:

- **A plain table, not a base-tracking view.** Nothing links it to the base
  partitions, so a base partition drop cannot touch it. This is the whole point
  the async MV could not deliver.
- **`AGGREGATE KEY` + `HLL_UNION` makes inserts idempotent.** Re-inserting a day
  merges its sketch into the existing one instead of duplicating it, so the
  ingest job can reprocess a day safely.

Retention is 15 months — the 12-month product window plus a buffer — via
`partition_live_number` on the daily partitions.

### Daily ingest job

A scheduled, idempotent insert per metric, reprocessing a 7-day lookback so late
ingestion and retries are covered:

```sql
INSERT INTO sum_user_hll_daily
SELECT project_id, DATE(timestamp) AS dt, HLL_UNION(HLL_HASH(user_id))
FROM user_events
WHERE DATE(timestamp) >= <today-7> AND DATE(timestamp) < <today>
GROUP BY project_id, DATE(timestamp);
```

The `GROUP BY` needs `HLL_UNION` around `HLL_HASH` — a bare `HLL_HASH` under a
`GROUP BY` is rejected as "must be an aggregate expression", the same shape the
`mv_*_hll_daily` DDL uses. `HLL_UNION` on the target column then merges the
re-inserted days, so the 7-day window is safe to repeat every run. A one-time
backfill over the full base history seeds the table before the job takes over.

The job runs as a **Kubernetes CronJob** — a `starrocks/fe-ubuntu` container
running the idempotent SQL through the MySQL client, the same image and access
path the MV migration Jobs already use — not a StarRocks native `SUBMIT TASK`.
The reasons: retries, run history, alerting, and deployment ownership are native
to a CronJob and to the existing analytics ops tooling, whereas a native task
fails silently inside the cluster. The manifests live beside the MV migration in
the analytics deployment (`build/charts/yorkie-analytics/.../starrocks/`), with
`concurrencyPolicy: Forbid` and history limits.

### Read path (dual read)

Split the requested window at the day the summary's coverage ends. The summary
and the base never overlap by day — the summary serves `[from, boundary)` and the
MV serves `[boundary, to)` — so their union is exact with no double counting.

**The boundary is the summary's own coverage, not today.** Splitting at a fixed
today assumes the summary is complete through yesterday, but it is only complete
through the days the refresh job has processed. With a once-daily job the summary
sits at `today - 2` for most of the day, and the day in between — yesterday —
lands in neither half: not in the summary, which has no row for it, and not in
the base half, which starts at today. It reads as zero, a V-shaped dip in the
series, and drags every total that includes it down with it.

So the boundary is `MAX(dt) + 1` per summary table, clamped to today:

- `MAX(dt) + 1` is the first day the summary does not hold, whatever the refresh
  cadence, and it self-corrects after a missed or failed run.
- Clamping to today keeps a backfill that included the running day from serving
  a partial sketch for today.
- An empty summary covers nothing, so the whole window is read from the base.

`MAX(dt)` is read per summary table, since a partly-failed refresh can leave them
at different days, but in one round trip for all five and cached for a minute:
`GetProjectStats` fans out twelve metric queries at once, and the value moves at
most once per refresh run. A stale-by-a-minute boundary only means a day is read
from the base that the summary could already have served — slower for one query,
never wrong.

**Series** metrics (`GetActiveUsers`, …) are per-day and independent, so no
cross-day work is needed. Read the covered days from the summary and the rest
from the base, then concatenate:

```sql
-- history, from the summary
SELECT dt, HLL_UNION_AGG(user_hll) AS v
FROM sum_user_hll_daily
WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'   -- [from, boundary)
GROUP BY dt ORDER BY dt;
-- the fresh days, from the base (MV rewrite path)
```

`HLL_UNION_AGG` already returns the merged cardinality (a bigint), so it is not
wrapped in `HLL_CARDINALITY` — doing so reads the count back as an HLL and yields
zero. Use `HLL_UNION_AGG(col)` to count, or `HLL_RAW_AGG(col)` / `HLL_UNION(col)`
when a merged sketch is needed.

**Totals** (`GetActiveUsersCount`, …) are a distinct over the whole window, so
the fresh days and the history must be **unioned, never summed** — a subject
active in both halves must count once. The union happens in the engine, over a
`UNION ALL` of the summary sketches and the fresh days' own, with cardinality
taken exactly once:

```sql
SELECT HLL_UNION_AGG(sketch) FROM (
    SELECT user_hll AS sketch FROM sum_user_hll_daily
     WHERE project_id = '%s' AND dt >= '%s' AND dt < '%s'          -- [from, boundary)
    UNION ALL
    SELECT HLL_UNION(HLL_HASH(user_id)) AS sketch FROM user_events
     WHERE project_id = '%s'
       AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'      -- [boundary, to)
     GROUP BY DATE(timestamp)                                      -- one sketch per day: the MV's own shape
) t;
```

`HLL_UNION_AGG` accepts both a stored `HLL` column and a merged `HLL_UNION(...)`
sketch, so the two halves compose losslessly. Adding two cardinalities across the
boundary would over-count; this design never does.

### Keeping the fresh half on the rollup

The fresh half is only cheap while StarRocks serves it from the sync MV, and two
things decide whether it does. Both were measured with `EXPLAIN` on 3.3
(single-partition bases, as deployed):

- **Reference `timestamp` only through `DATE(timestamp)`.** `mv_*_hll_daily`
  carries `mv_dt = DATE(timestamp)` and no raw `timestamp` column, so a query
  that mentions raw `timestamp` drops out of the rollup's candidate set:
  `rollup: client_events` instead of `rollup: mv_client_hll_daily`. Raw bounds do
  prune partitions on a date-partitioned base (`partitions=1/7` vs `7/7`), but
  that is a false economy — the MV holds one row per (project, day), so all of
  its partitions together are far cheaper than one raw partition of the base.
- **Aggregate inside each `UNION ALL` branch.** A branch that projects one
  `HLL_HASH(id)` per row leaves the `HLL_UNION_AGG` above the union, and
  StarRocks cannot push that into the rollup — the branch full-scans the base
  even with a DATE-only predicate. `HLL_UNION(HLL_HASH(id)) GROUP BY
  DATE(timestamp)` matches the MV's own shape and rewrites. (`HLL_RAW_AGG` does
  not.) HLL union is associative, so merging one sketch per day instead of one
  per row is the same distinct count.

The series and peak builders already aggregate in-branch, so for them the
DATE-only predicate is enough; the totals need both.

**Peak sessions** needs no boundary union: it is `MAX` over independent
`(day, channel)` distinct counts. Read per-`(dt, channel)` cardinality from
`sum_session_hll_daily_ch` for the history and from the base for the fresh days,
then take the daily `MAX` (series) or the window `MAX` (total).

**Fallback.** The fallback is the `SummaryEnabled` flag, which gates the whole
dual read and defaults off. A cluster that has not created the summaries leaves
it off and runs the base-scan path unchanged — byte-identical to today. The flag
is turned on per environment only after the summary tables exist and are
validated (see Deployment sequencing). Unlike the MV design's rewrite — which
falls back to a base scan per query automatically — this dual read names the
summary table directly, so a misconfiguration (flag on, table missing) surfaces
as a loud read error rather than a silent slow path; that is deliberate, since
the flag is only ever enabled behind the validation gate. An automatic per-query
fallback (catch a missing-table error, retry the retained base query) is a
reserved hardening if a cluster ever needs the flag on before every summary
exists.

**The flag-off path is only a lossless default before raw TTL.** It is
byte-identical and complete only while the base still holds the full history —
i.e. before step 6 enables the 90-day TTL. Once the base is trimmed to 90 days,
turning `SummaryEnabled` off no longer reproduces the long windows: a 3-/12-month
read then silently returns the last 90 days without erroring. So disabling the
flag is a valid rollback only up to the TTL step; after it, rollback means
repairing or backfilling the summaries (or temporarily restoring raw retention),
not just flipping the flag. This is the other reason TTL is sequenced last and
gated on the summary being trusted.

This is the one place the design reverses a decision from
[project-stats-warehouse-mv](project-stats-warehouse-mv.md): that design kept the
warehouse schema out of the Go server and let the rewrite union the sketches.
Automatic rewrite cannot span two tables with different lifetimes, so the
long-retention read has to name the summary table and write the `HLL_UNION_AGG`
by hand. The fallback is what keeps that from hard-coding the schema into every
cluster.

### Base partitioning and TTL

The read path and summary are correct with the base tables exactly as they are
today; partitioning and TTL are what make the summary *necessary* and what
realise the storage saving. They come last.

The event tables are `DUPLICATE KEY / DISTRIBUTED BY RANDOM BUCKETS 16 /
replication_num = 1`, with no `PARTITION BY`. StarRocks cannot add partitioning
to an existing table with `ALTER`, so each table is recreated with expression
partitioning and reloaded:

```sql
CREATE TABLE user_events_p ( ... same columns ... ) ENGINE = OLAP
DUPLICATE KEY(project_id, user_id, timestamp)
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM BUCKETS 16
PROPERTIES ("replication_num" = "1", "partition_live_number" = "90");  -- 90-day raw TTL
```

The routine load binds to the base table by name, so the swap follows the
`session_events` redistribution playbook: `PAUSE ROUTINE LOAD`, `INSERT INTO
..._p SELECT`, `ALTER TABLE ... RENAME` to swap, `RESUME ROUTINE LOAD`. On the
billion-row tables this is done in a low-ingest window with replica status
watched — `replication_num = 1` has a tablet-quorum-stall history. TTL is
enabled last, only after the summary is backfilled and validated.

**Partition pruning under `DATE()`.** The MV design warned that wrapping
`timestamp` in `DATE()` can lose partition pruning once the base is partitioned,
and it does: measured on 3.3, a `DATE(timestamp)` range reads every partition.
Carrying raw `timestamp` bounds alongside it to recover the pruning is not the
answer — it takes the query off the MV entirely, which costs far more than the
pruning saves (see *Keeping the fresh half on the rollup*). Measured on a
day-partitioned copy of `client_events`:

```
raw + DATE : rollup: client_events_p        partitions=1/7   -- pruned, base scan
DATE only  : rollup: mv_client_p_hll_daily  partitions=7/7   -- unpruned, one row per day
```

So the fresh half stays DATE-only after partitioning too. What `EXPLAIN` must
confirm on the deployed version is that it reads `mv_*_hll_daily`, not how many
partitions it touches.

### Deployment sequencing

Each step is lossless and reversible; the base scan is always a correct
fallback. Per environment:

1. Create the summary tables (empty, partitioned, 15-month TTL). A cluster whose
   tables predate `rl_session_daily` also needs `ALTER TABLE
   sum_session_hll_daily_ch ADD ROLLUP …` — `CREATE TABLE IF NOT EXISTS` carries
   the clause but skips it on an existing table. The rollup builds
   asynchronously; wait for it in `desc … all` before step 3.
2. Backfill from the base — staged per table, `session_events` last and in a
   low-ingest window, as with the MV builds. Idempotent, and it stops before the
   running UTC day so the summary never holds a partial day for the read path to
   trust as complete.
3. **Validate**: while the base still holds full history, the dual-read result
   must equal the MV-only result for a set of projects and windows. Equality
   here is the proof the union math is right, because the two paths overlap
   completely before TTL.
4. Enable the daily ingest CronJob; observe a few days of runs.
5. Deploy the server and turn on `SummaryEnabled` (the flag defaults off, so the
   deploy itself changes nothing until it is flipped, and only after step 3/4).
6. Only now recreate the base tables partitioned and enable the 90-day TTL,
   per-table, TTL last.

Steps 1–5 change nothing a user sees; step 6 is the one that shortens raw
retention, and by then the summary has been serving and validated.

### Verification

- Dual-read total equals the MV-only total for windows inside current retention
  (step 3).
- `ScanRows` in the FE audit log stays small for the six metrics.
- After TTL: a 12-month window still returns a full series, and its values match
  the summary rather than collapsing to the 90-day base.

## Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Adding cardinalities across the boundary over-counts a subject active in both halves | Union sketches with `HLL_UNION_AGG`, take cardinality once. Summary `[from, boundary)` and base `[boundary, to)` split on the day, so there is no overlap to double count. |
| `DATE(timestamp)` loses partition pruning once the base is partitioned, so the fresh-day scan reads every partition | Accepted: the fresh half reads the MV, which holds one row per (project, day), so every partition of it is cheaper than one raw partition of the base. Raw `timestamp` bounds would prune but would also drop the query off the MV. |
| Repartitioning a live billion-row table (session) risks stalled ingest and quorum loss under `replication_num = 1` | New table + `INSERT SELECT` + rename swap under `PAUSE`/`RESUME ROUTINE LOAD`, in a low-ingest window, watching `ADMIN SHOW REPLICA STATUS`. Same playbook as the `session_events` redistribution. |
| A partial day in the summary would be trusted as complete, since the split trusts every day at or below `MAX(dt)` | Both writers stop before the running UTC day: the CronJob's window is `[today-7, today)` and the backfill carries the same `< DATE(UTC_TIMESTAMP())` guard. The read path's clamp to today is the second line of defense. |
| The ingest job misses a day or late events land after it runs | 7-day lookback reprocess every run; `HLL_UNION` makes repeats idempotent. CronJob history and alerting surface a failed run. The read path splits at the summary's actual `MAX(dt)`, so a day the job has not written yet is read from the base rather than reported as zero. |
| Summary drifts from the base over time | Periodic reconciliation comparing an overlap day's summary against a base recount; the 7-day lookback self-heals recent drift. |
| Backfill full-scans the billion-row tables | Staged per table, `session_events` in a low-ingest window; cost is the one-time base scan (~80ns/row), as measured for the MV builds. |
| Enabling raw TTL before the summary is trusted would lose history irrecoverably | TTL is step 6, gated on steps 1–5; validation in step 3 runs while both paths overlap. |
| Expression partitioning, `partition_live_number`, and the HLL functions need a recent StarRocks | Verify the engine clears the version floor per environment before creating the tables (deployed clusters run 3.3.x). |
| Raw-TTL enforcement may not drop partitions as expected on 3.3.x | `partition_live_number` has had drop bugs ([#39341][p39341]); the cleaner `partition_retention_condition` (Common Partition Expression TTL) is native-table-only from v3.5, past the deployed 3.3.x. Use dynamic partitioning / `partition_live_number` and confirm old partitions actually drop before relying on TTL for the storage saving. |

## Design Decisions

| Decision | Reason |
|----------|--------|
| An independent aggregate-key table, not an async MV | Only a table with no link to the base survives base partition drops; the async MV provably re-tracks and drops the same partition. |
| Kubernetes CronJob, not StarRocks `SUBMIT TASK` | Retries, run history, alerting, and ownership are native to the CronJob and the existing analytics ops tooling; a native task fails silently. |
| Hand-written `HLL_UNION_AGG` in the Go read path | Reverses the MV design's "no schema in the server", but automatic rewrite cannot union two tables with different lifetimes. The base-scan fallback keeps clusters without the summary correct. |
| 90-day raw retention, 15-month summary | 90 days keeps the quarter view answerable from the base fallback; 15 months is the 12-month product window plus buffer. |
| Split at the summary's `MAX(dt) + 1`, not at today | The days the refresh job has not reached stay exact from the base instead of falling into a gap between the halves; the boundary self-corrects after a missed run. |
| 7-day ingest lookback | Covers late ingestion and retries without a reconciliation job; `HLL_UNION` makes the repeat free of side effects. |
| UTC day buckets | Unchanged from the MV design; a local-day boundary is still the reserved ingest-time-date path there. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Asynchronous MV with its own TTL | Retention does not decouple — a dropped base partition drops the MV partition on the next refresh (measured on 3.3.9), and its `partition_ttl` retains the *most recent* N partitions while routing older queries back to the base, the opposite of long retention. |
| Archive raw events to an external store (S3/Parquet + a lakehouse table), as Grab does for Spark observability | Works for raw retention but adds an external store and a cross-system read on the historical path. The HLL summary is orders of magnitude smaller than raw, so long retention fits inside StarRocks with no external tier and no join across systems. |
| Retain raw events for 12 months, no summary | Storage on a billion-row table for a full year is the cost this design exists to avoid. |
| Summary table but keep automatic rewrite from `APPROX_COUNT_DISTINCT` | Rewrite targets the base's own rollup, which dies with the base. Serving a separate-lifetime table requires naming it and unioning by hand. |
| StarRocks native `SUBMIT TASK ... SCHEDULE` for ingest | Runs inside the cluster with no run history or alerting; a silent failure stops the summary without a signal. |
| Union the sketches in Go instead of SQL | HLL sketches cannot be merged outside the engine; the union must be a `HLL_UNION_AGG` in the query. |
| Cache the 12-month totals in MongoDB, as `stats_clients_count` does | A second staleness budget for a value that is not a small scalar and that the summary already produces cheaply. |
| Longer raw retention instead of a summary | Directly defeats the storage goal; the summary is what lets raw retention be short. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

## References

- [Use HLL for approximate count distinct][hll] — the `HLL_UNION` column,
  `HLL_HASH` on insert, `HLL_UNION_AGG` on read, and the rollup-table
  recommendation this design follows.
- [Create a partitioned materialized view][smv] — async MV `partition_ttl`
  semantics and base-table fallback for outdated partitions.
- [Building a Spark observability product with StarRocks][grab] — a production
  short-hot-retention + external long-history precedent, contrasted above.
- StarRocks [#39341][p39341] — `partition_live_number` drop bug.

[hll]: https://docs.starrocks.io/docs/using_starrocks/distinct_values/Using_HLL/
[smv]: https://docs.starrocks.io/docs/using_starrocks/async_mv/use_cases/create_partitioned_materialized_view/
[grab]: https://engineering.grab.com/building-a-spark-observability
[p39341]: https://github.com/StarRocks/starrocks/issues/39341
