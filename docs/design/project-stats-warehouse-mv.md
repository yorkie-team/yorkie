---
title: project-stats-warehouse-mv
target-version: 0.7.17
---

# Project Stats Warehouse Materialized Views

## Problem

`GetProjectStats` renders six warehouse-backed metrics — active users, active
documents, active clients, active channels, sessions, and peak sessions per
channel — each as a daily series plus a total. That is 12 queries against the
event tables the [OLAP stack](olap-stack.md) fills, issued from
`server/backend/warehouse/starrocks.go`, and every one of them is a distinct
count over a whole event table.

[Project Stats Cache](project-stats-cache.md) fixed the MongoDB half of the same
endpoint and explicitly left the warehouse alone, on the grounds that StarRocks
"is already OLAP-optimized". That holds only up to a point. Running the 12
queries concurrently with `APPROX_COUNT_DISTINCT` instead of exact distinct
(v0.7.15) is a constant-factor win: each request still full-scans its base
table, so cost stays O(rows). For a project with more than 100M events in the
window a single approximate distinct is still multi-second, and the peak
sessions per channel query alone runs past the dashboard's 3s budget.

The durable fix is to stop reading raw events on the request path.

### Goals

- Make the warehouse reads volume-independent: latency driven by the number of
  days in the window, not the number of events in it.
- Keep the numbers identical to what `APPROX_COUNT_DISTINCT` already returns.
- Require no new refresh job, no new table to keep in sync, and no change to the
  `Warehouse` interface.

### Non-Goals

- Exact distinct counts. HLL error (~0.2–1.7%) was already accepted in v0.7.15.
- Event retention. Purging old events interacts with this design (see Risks) but
  no purge exists today.
- Caching warehouse results in MongoDB the way `stats_clients_count` is cached.
  Pre-aggregation inside the warehouse is cheaper and never goes stale.

## Design

Store one HLL sketch per `(project, day[, channel])` and let StarRocks maintain
it at ingest time. StarRocks calls this a **synchronous materialized view**: a
rollup index on the base table, updated as part of the write, with no refresh
job and no rebuild. The dashboard query then reads a handful of summary rows and
unions the daily sketches.

The critical property is that nothing in the Go code reads the view directly.
StarRocks rewrites the existing query onto the rollup, so the sketch union
happens inside the engine. There is no hand-written `HLL_UNION_AGG`.

### Rewrite conditions

StarRocks only rewrites a query onto the view when all three hold:

1. The aggregate is HLL-based — `APPROX_COUNT_DISTINCT`, already true.
2. The date predicate and grouping use **`DATE(timestamp)`**, matching the
   expression the view is grouped by.
3. The view exists on that base table.

Condition 2 is the code change. The predicate moves from `timestamp` to
`DATE(timestamp)` in all 12 queries:

```go
// before: predicate on the raw timestamp -> no rewrite, base full scan
//   WHERE project_id = '%s' AND timestamp >= '%s' AND timestamp < '%s'
// after: predicate on DATE(timestamp) -> rewritten onto the rollup
//   WHERE project_id = '%s' AND DATE(timestamp) >= '%s' AND DATE(timestamp) < '%s'
```

`from` and `to` are already formatted as dates, so day-boundary semantics are
unchanged. Miss condition 2 and the query silently falls back to a base full
scan — correct results, no error, no log line. That silence is why the
expression is worth a comment at the call site.

### The views

A synchronous view inherits the base table's distribution and replication, so it
takes no `buckets` or `replication_num` clause. They live in
`build/charts/yorkie-analytics/.../starrocks/configmap.yaml` (deployed clusters)
and `build/docker/analytics/init-create-mv.sql` (local stack):

```sql
CREATE MATERIALIZED VIEW mv_user_hll_daily AS
    SELECT project_id, DATE(timestamp) AS dt,
           HLL_UNION(HLL_HASH(user_id)) AS user_hll
    FROM user_events
    GROUP BY project_id, DATE(timestamp);
```

`mv_document_hll_daily` and `mv_channel_hll_daily` follow the same shape.
`mv_session_hll_daily_ch` additionally groups by `channel_key`, which lets one
view serve both the sessions metric and peak sessions per channel.
`mv_client_hll_daily` is the only one that carries `event_type`, because its
query filters on `event_type = 'client-activated'`; the other four omit it so
that `GROUP BY DATE(timestamp)` maps to a clean key prefix.

The column aliases are cosmetic. StarRocks renames the view's columns to
`mv_dt`, `mv_hll_union_user_id`, and so on — nothing can reference them by name,
which is another way of saying the rewrite is the only interface.

### Base table alignment

Four event tables key and distribute on `project_id`. `user_events` did not: it
was `DUPLICATE KEY(user_id)` / `DISTRIBUTED BY HASH(user_id) BUCKETS 10`, so a
per-project query could not prune anything. Measured on 200k synthetic rows
across 50 projects, querying one project:

| layout | rewritten query | fallback (no view) |
|---|---|---|
| `DUPLICATE KEY(user_id)` | 60 rows scanned | 200,000 rows scanned |
| `DUPLICATE KEY(project_id, user_id, timestamp)` | 6 rows scanned | 4,000 rows scanned |

Both return the same value. The view helps either way, but the misaligned layout
scans ten times more summary rows, and its fallback path reads every project's
events instead of one project's. `user_events` is therefore redefined to match
the other four.

`CREATE TABLE IF NOT EXISTS` does not alter an existing table, so this only
takes effect on new installs. Existing clusters keep the old layout until
someone migrates them — create the table under a new name, `INSERT INTO ...
SELECT`, then swap with `ALTER TABLE ... RENAME`. Nothing breaks in the
meantime; the queries stay correct and still hit the view.

### Deployment sequencing

Views without the code change mean no rewrite — harmless. The code change
without the views means the base-scan fallback, which is correct but not
automatically as fast as before: wrapping the column in `DATE()` can cost
partition pruning if the base table is partitioned on `timestamp`. The event
tables carry no `PARTITION BY` today, and measurement confirms the fallback
scans exactly the same rows as the raw predicate, so the order is not load
bearing on the current schema. It becomes load bearing the day the tables are
partitioned. Safe order per environment:

1. Create and build the views (a one-time base full scan, ~80ns/row).
2. Confirm ingest-time maintenance.
3. Deploy the server with the `DATE(timestamp)` predicate.
4. Confirm `ScanRows` in the FE audit log is small for the six metrics.

### Verification

`EXPLAIN` naming the rollup index tells you the plan picked the view;
`ScanRows` in `fe.audit.log` tells you the base table was actually skipped. Both
are worth checking, because a plan can read the rollup with
`PREAGGREGATION: OFF` — the peak sessions query does exactly that, since the
outer `MAX` sits over a derived aggregate — and still avoid the base scan.

Measured on the public cluster (StarRocks 3.3.9), before and after creating the
views, with every value identical across the two runs:

| metric | rows scanned before | after |
|---|---|---|
| active users | 26,047 | 1,422 |
| active documents | 23,781 | 199 |
| active clients | 33,533 | 195 |
| active channels | 66 | 3 |
| sessions | 82 | 33 |
| peak sessions per channel | 82 | 33 |

Events ingested after the views were built are reflected without any rebuild.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A base `DELETE` on `timestamp` fails with error 5509 while a view is attached, because `timestamp` is not a column of the view | No retention purge exists today. When one is added: delete on a column the view carries (`project_id` works and stays consistent), partition the base tables and drop partitions, or drop the views, purge, and recreate them. The rebuild is a single base scan. |
| The 5509 check only fires when the table holds rows | Never validate a purge against an empty table — it will pass and teach you the wrong thing. |
| Rewrite silently stops if someone rewrites the predicate back to a raw `timestamp` | Comment at the call site in `starrocks.go`; `ScanRows` in the FE audit log is the check. |
| Aggregate rollups produce frequent small writes under continuous ingest, and compaction on the internal cluster has deadlocked before | Summary volume is tiny next to the base table, and the compaction score stayed at zero on the public cluster — but public ingest is a trickle, so this has to be re-measured under production ingest before rollout there. |
| Expressions in synchronous views need StarRocks 3.1 or newer | The chart pins `3.3-latest` and the local stack pins 3.3.9. Check the version before creating the views in any other environment. |
| The init hook re-runs on every chart upgrade and `CREATE MATERIALIZED VIEW IF NOT EXISTS` still errors when the view exists | The init script runs the view DDL with `mysql --force` and then polls `desc <table> all` for each index. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Synchronous view rather than an asynchronous one | Maintained at ingest time, so there is no refresh interval to tune and no staleness window. An async view would reintroduce exactly the freshness question the MongoDB cache had to answer. |
| Let the rewrite union the sketches, instead of querying the view directly | The only Go change is the predicate. Reading the view by name would hard-code the schema into the server and break every cluster that has not created it. |
| One view per event table, keyed on `(project_id, day)` | Matches how the dashboard slices the data. Days are the finest granularity the API exposes. |
| `event_type` in the client view only | Its query is the only one that filters on `event_type`. Adding the column elsewhere would push `dt` out of the key prefix for no gain. |
| Channel key in the session view | One view serves both the sessions metric and peak sessions per channel, instead of two views over the same table. |
| Realign `user_events` in the DDL, without an automatic migration | New installs get the good layout at no cost. Rewriting a live event table is a separate operation with its own risk, and the old layout still works. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| A manually maintained daily rollup table | Needs a scheduled job, a backfill, and reconciliation after gaps. The synchronous view gets the same result from the ingest path already in place. |
| An asynchronous materialized view with a refresh interval | Adds a staleness window and a refresh job, in exchange for flexibility this query shape does not need. |
| Hand-written `HLL_UNION_AGG` over a sketch table in Go | Puts the warehouse schema in the server. Any cluster without the table breaks, instead of falling back to a correct base scan. |
| Cache the warehouse results in MongoDB, as `stats_clients_count` does | A second staleness budget for numbers the warehouse can produce fresh in tens of milliseconds. |
| Partition the event tables by day and rely on partition pruning alone | Helps the scan, but the request still counts distinct values over every event in the window. Pruning and pre-aggregation are complementary; pre-aggregation is what removes the O(rows) term. |
| Raise the dashboard timeout | Moves the failure rather than removing it. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
