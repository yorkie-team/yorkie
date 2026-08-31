**Created**: 2026-08-31

# Project Stats Long-Retention Windows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve project-stats windows up to 12 months after raw-event TTL is
enabled, by reading a decoupled daily HLL summary for history and the fresh MV
for today.

**Architecture:** Independent per-metric `AGGREGATE KEY` summary tables in
StarRocks, filled by an idempotent daily job, read by splitting each window at
today (history from the summary, today from the existing MV, unioned with
`HLL_UNION` for totals). A `SummaryEnabled` config flag ships the read path dark;
it is flipped on only after backfill and validation. Base partitioning + 90-day
TTL is a separate operational migration, gated on validation.

**Tech Stack:** Go (`server/backend/warehouse`), StarRocks SQL (HLL, aggregate
tables, expression partitioning), Kubernetes CronJob (internal devops repo).

**Spec:** `docs/design/project-stats-long-retention.md`

## Global Constraints

- Apache 2.0 license header on every new Go file.
- Package comment on every package; follow the Uber Go Style Guide.
- StarRocks has no prepared statements — queries are built with `fmt.Sprintf`
  and `//nolint:gosec`, exactly as the existing methods do. Dates format as
  `2006-01-02`.
- Day boundaries are UTC, matching the MV design. "today" = `time.Now().UTC()`
  truncated to the day.
- Numbers must stay identical to the MV path for windows inside current
  retention — HLL sketches are unioned, never summed.
- StarRocks is NOT in the Go test harness (`project_stats_test.go` passes a nil
  warehouse → `DummyWarehouse`). Go tests cover pure logic and query strings;
  real StarRocks behavior is verified by cluster rehearsal (Task 8), as the MV
  task did.
- Code PR scope excludes the base repartitioning/TTL migration (Task 7 is a
  runbook, executed separately after validation).

## File Structure

- `build/docker/analytics/init-create-summary.sql` — 5 summary table DDLs (local stack).
- `build/docker/analytics/init-backfill-summary.sql` — one-time backfill INSERTs.
- `build/charts/yorkie-analytics/templates/starrocks/configmap.yaml` — same DDL for deployed clusters (mirror of the MV configmap).
- `server/backend/warehouse/window.go` — pure window-split helper (`splitWindow`).
- `server/backend/warehouse/window_test.go` — unit tests for `splitWindow`.
- `server/backend/warehouse/metrics.go` — per-metric descriptor table (base table, id column, summary table, hll column, extra predicate).
- `server/backend/warehouse/starrocks.go` — rewrite the 12 methods to dual-read via the descriptor + helper; add `SummaryEnabled`.
- `server/backend/warehouse/starrocks_query_test.go` — golden-string tests for the built queries (no DB).
- `server/backend/warehouse/warehouse.go` — add `SummaryEnabled bool` to `Config`.
- Internal devops repo `k8s/apps/yorkie-analytics/starrocks/summary/` — ingest CronJob + configmap + backfill Job (Task 6), mirroring `starrocks/mv/`.

---

### Task 1: Summary table DDL

**Files:**
- Create: `build/docker/analytics/init-create-summary.sql`
- Modify: `build/docker/analytics/docker-compose.yml` (mount/run the new SQL after tables, before/after MV — order-independent, it reads base only at backfill)
- Modify: `build/charts/yorkie-analytics/templates/starrocks/configmap.yaml` (add the same DDL under a new key)

**Interfaces:**
- Produces: tables `sum_user_hll_daily(project_id, dt, user_hll)`,
  `sum_document_hll_daily(project_id, dt, document_hll)`,
  `sum_channel_hll_daily(project_id, dt, channel_hll)`,
  `sum_session_hll_daily_ch(project_id, dt, channel_key, session_hll)`,
  `sum_client_hll_daily(project_id, event_type, dt, client_hll)`. All
  `AGGREGATE KEY`, `HLL_UNION` sketch column, `PARTITION BY date_trunc('day', dt)`,
  `partition_live_number = 465`.

- [ ] **Step 1: Write the DDL** mirroring `init-create-mv.sql` metric-for-metric, e.g.:

```sql
USE yorkie;

-- Decoupled daily HLL summaries that outlive the base event tables so
-- long-retention dashboard windows survive raw-event TTL. Filled by the
-- daily ingest job; read by the dual-read path in
-- server/backend/warehouse/starrocks.go. See
-- docs/design/project-stats-long-retention.md.

CREATE TABLE IF NOT EXISTS sum_user_hll_daily (
    project_id VARCHAR(64),
    dt         DATE,
    user_hll   HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

-- document, channel identical shape; session adds channel_key; client adds event_type:

CREATE TABLE IF NOT EXISTS sum_session_hll_daily_ch (
    project_id  VARCHAR(64),
    dt          DATE,
    channel_key VARCHAR(128),
    session_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt, channel_key)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_client_hll_daily (
    project_id VARCHAR(64),
    event_type VARCHAR(32),
    dt         DATE,
    client_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, event_type, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");
```

- [ ] **Step 2: Bring up the local analytics stack** and confirm the tables exist.

Run: `docker compose -f build/docker/analytics/docker-compose.yml up --build -d`
then `mysql -h127.0.0.1 -P9030 -uroot -e "SHOW TABLES FROM yorkie LIKE 'sum_%'"`
Expected: 5 `sum_*` tables. (Local stack must run StarRocks ≥3.1 for
`date_trunc` expression partitioning — bump the pinned image if it is 2.5.x.)

- [ ] **Step 3: Commit**

```bash
git add build/docker/analytics/init-create-summary.sql build/docker/analytics/docker-compose.yml build/charts/yorkie-analytics/templates/starrocks/configmap.yaml
git commit -m "Add decoupled daily HLL summary tables for project stats"
```

---

### Task 2: Window-split helper (pure, TDD)

**Files:**
- Create: `server/backend/warehouse/window.go`
- Test: `server/backend/warehouse/window_test.go`

**Interfaces:**
- Produces:
  ```go
  // dayRange is a half-open [start, end) UTC day range; Empty when start >= end.
  type dayRange struct { Start, End time.Time; Empty bool }
  // splitWindow splits [from, to) at the UTC day `today` into a historical
  // range served by the summary and a fresh range served by the base rollup.
  func splitWindow(from, to, today time.Time) (hist, fresh dayRange)
  ```
  `hist = [from, min(to, today))`, `fresh = [max(from, today), to)`; each Empty
  when its start >= end.

- [ ] **Step 1: Write failing tests**

```go
func TestSplitWindow(t *testing.T) {
    day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
    today := day("2026-08-31")
    cases := []struct{ name, from, to string; histEmpty, freshEmpty bool; hEnd, fStart string }{
        {"entirely past", "2026-08-01", "2026-08-31", false, true, "2026-08-31", ""},
        {"entirely today", "2026-08-31", "2026-09-01", true, false, "", "2026-08-31"},
        {"straddling", "2026-08-01", "2026-09-01", false, false, "2026-08-31", "2026-08-31"},
        {"empty input", "2026-08-31", "2026-08-31", true, true, "", ""},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            hist, fresh := splitWindow(day(c.from), day(c.to), today)
            assert.Equal(t, c.histEmpty, hist.Empty)
            assert.Equal(t, c.freshEmpty, fresh.Empty)
            if !hist.Empty { assert.Equal(t, day(c.hEnd), hist.End) }
            if !fresh.Empty { assert.Equal(t, day(c.fStart), fresh.Start) }
        })
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./server/backend/warehouse/ -run TestSplitWindow -v`
Expected: FAIL (`splitWindow` undefined).

- [ ] **Step 3: Implement `splitWindow`** in `window.go` (license header + package comment).

```go
func splitWindow(from, to, today time.Time) (hist, fresh dayRange) {
    hEnd := to
    if today.Before(hEnd) { hEnd = today }
    hist = dayRange{Start: from, End: hEnd, Empty: !from.Before(hEnd)}

    fStart := from
    if today.After(fStart) { fStart = today }
    fresh = dayRange{Start: fStart, End: to, Empty: !fStart.Before(to)}
    return hist, fresh
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./server/backend/warehouse/ -run TestSplitWindow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/backend/warehouse/window.go server/backend/warehouse/window_test.go
git commit -m "Add UTC day window-split helper for dual-read project stats"
```

---

### Task 3: Per-metric descriptor + SummaryEnabled config

**Files:**
- Create: `server/backend/warehouse/metrics.go`
- Modify: `server/backend/warehouse/warehouse.go` (add `SummaryEnabled bool` to `Config`)

**Interfaces:**
- Produces:
  ```go
  // metricDesc describes one warehouse metric's base and summary shapes.
  type metricDesc struct {
      baseTable   string // e.g. "user_events"
      idColumn    string // e.g. "user_id"
      summaryTable string // e.g. "sum_user_hll_daily"
      hllColumn   string // e.g. "user_hll"
      byChannel   bool   // session: group/read per channel_key
      eventType   string // non-empty only for clients ("client-activated")
  }
  var (
      descUser, descDocument, descClient, descChannel, descSession metricDesc
  )
  ```
  `StarRocks.summaryEnabled` read from `Config.SummaryEnabled`.

- [ ] **Step 1: Write the descriptors and wire the flag.** Add `SummaryEnabled`
  to `Config`, set `r.conf` already carries it; expose `func (r *StarRocks)
  summaryEnabled() bool { return r.conf.SummaryEnabled }`.

- [ ] **Step 2: Build** to confirm it compiles.

Run: `go build ./server/backend/warehouse/`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add server/backend/warehouse/metrics.go server/backend/warehouse/warehouse.go
git commit -m "Add per-metric descriptors and SummaryEnabled warehouse flag"
```

---

### Task 4: Dual-read query builders (golden-string TDD)

**Files:**
- Modify: `server/backend/warehouse/starrocks.go`
- Test: `server/backend/warehouse/starrocks_query_test.go`

**Interfaces:**
- Consumes: `metricDesc`, `splitWindow`, `dayRange`.
- Produces (unexported, pure string builders — the DB call stays in the public methods):
  ```go
  // seriesQuery builds the per-day series query for [from,to) at today.
  func seriesQuery(d metricDesc, id types.ID, from, to, today time.Time, summary bool) string
  // totalQuery builds the whole-window distinct total, unioning summary + fresh.
  func totalQuery(d metricDesc, id types.ID, from, to, today time.Time, summary bool) string
  // peakSeriesQuery / peakTotalQuery for GetPeakSessionsPerChannel[Count].
  ```
  When `summary` is false, the builders return exactly today's base-only SQL
  (byte-identical to the current queries) so the flag-off path is unchanged.

- [ ] **Step 1: Write failing golden tests** asserting the built SQL for each
  branch. Example for the total, straddling window, summary on:

```go
func TestTotalQuery_Straddling_Summary(t *testing.T) {
    day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
    got := totalQuery(descUser, types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31"), true)
    assert.Contains(t, got, "HLL_UNION_AGG(sketch)")
    assert.Contains(t, got, "SELECT user_hll AS sketch FROM sum_user_hll_daily")
    assert.Contains(t, got, "dt >= '2026-08-01' AND dt < '2026-08-31'")
    assert.Contains(t, got, "UNION ALL")
    assert.Contains(t, got, "HLL_HASH(user_id) AS sketch FROM user_events")
    assert.Contains(t, got, "timestamp >= '2026-08-31' AND timestamp < '2026-09-01'")   // partition pruning
    assert.Contains(t, got, "DATE(timestamp) >= '2026-08-31' AND DATE(timestamp) < '2026-09-01'") // MV-exact fresh
}

func TestTotalQuery_SummaryOff_MatchesBase(t *testing.T) {
    day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
    got := totalQuery(descUser, types.ID("p1"), day("2026-08-01"), day("2026-09-01"), day("2026-08-31"), false)
    assert.Contains(t, got, "APPROX_COUNT_DISTINCT(user_id)")
    assert.NotContains(t, got, "sum_user_hll_daily")
}
```

Add analogous tests for `seriesQuery` (hist rows from summary + today from base,
concatenated), the client `event_type` predicate, and `peakTotalQuery` (per
`(dt, channel)` cardinality then `MAX`, no cross-boundary union).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./server/backend/warehouse/ -run Query -v`
Expected: FAIL (builders undefined).

- [ ] **Step 3: Implement the builders** in `starrocks.go`, using `splitWindow`.
  Totals union `dayRange` halves with `HLL_UNION_AGG` per the spec's read-path
  SQL; the fresh half carries both raw-`timestamp` and `DATE(timestamp)` bounds.
  Series concatenates summary rows and today's base row. Peak reads per
  `(dt, channel)` and takes `MAX`. Summary-off returns the current SQL verbatim.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./server/backend/warehouse/ -run Query -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/backend/warehouse/starrocks.go server/backend/warehouse/starrocks_query_test.go
git commit -m "Build dual-read project-stats queries splitting at today"
```

---

### Task 5: Wire the 12 methods through the builders + fallback

**Files:**
- Modify: `server/backend/warehouse/starrocks.go`

**Interfaces:**
- Consumes: the builders from Task 4.
- Produces: the 12 `Warehouse` methods unchanged in signature; each now calls
  its builder with `today = time.Now().UTC().Truncate(24h)` and
  `summary = r.summaryEnabled()`. Behavior with the flag off is identical to
  today.

- [ ] **Step 1: Replace each method body** to delegate to the builder + existing
  `queryMetrics`/`queryCount`. Keep the `//nolint:gosec` and NOTE comment.

- [ ] **Step 2: Run the warehouse unit tests + lint**

Run: `go test ./server/backend/warehouse/... && make lint`
Expected: PASS, lint clean.

- [ ] **Step 3: Run the full unit suite** (no DB) to confirm nothing else broke.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/backend/warehouse/starrocks.go
git commit -m "Route project-stats reads through the dual-read builders"
```

---

### Task 6: Ingest CronJob + backfill (internal devops repo)

**Files (internal `media-tool-dev/devops` repo):**
- Create: `k8s/apps/yorkie-analytics/starrocks/summary/configmap.yaml` — the daily idempotent INSERT SQL (7-day lookback) per metric.
- Create: `k8s/apps/yorkie-analytics/starrocks/summary/cronjob.yaml` — `starrocks/fe-ubuntu` mysql client running the SQL; `concurrencyPolicy: Forbid`, history limits, `ttlSecondsAfterFinished`.
- Create: `k8s/apps/yorkie-analytics/starrocks/summary/backfill-job.yaml` — one-time full-history backfill, staged per table for prod (session last, low-ingest window), mirroring `starrocks/mv/prod/`.
- Create: `k8s/apps/yorkie-analytics/starrocks/summary/README.md` — apply/verify/rollback, mirroring `starrocks/mv/README.md`.

**Interfaces:**
- Consumes: the summary tables from Task 1 (must exist first).
- Produces: a running daily job filling `sum_*` tables. Backfill SQL example:
  ```sql
  INSERT INTO sum_user_hll_daily
  SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(user_id))
  FROM user_events
  GROUP BY project_id, DATE(timestamp);
  ```
  The `GROUP BY` requires `HLL_UNION(HLL_HASH(...))`; a bare `HLL_HASH` is
  rejected as "must be an aggregate expression". The daily SQL is the same with
  a trailing 7-day UTC window — use `DATE(UTC_TIMESTAMP())` (not `CURDATE()`,
  which StarRocks evaluates in the session `time_zone`, default `Asia/Shanghai`):
  `WHERE DATE(timestamp) >= DATE_SUB(DATE(UTC_TIMESTAMP()), INTERVAL 7 DAY) AND DATE(timestamp) < DATE(UTC_TIMESTAMP())`.

- [ ] **Step 1: Write the configmap SQL** for all 5 metrics (daily + backfill variants).
- [ ] **Step 2: Write the CronJob and backfill Job** modeled on `starrocks/mv/job.yaml` and `tools/housekeeping-trend/cronjob.yaml`.
- [ ] **Step 3: Write the README** (apply order: tables → backfill → validate → enable CronJob).
- [ ] **Step 4: Commit in the devops repo** (English, per that repo's rules).

Note: no CI here; correctness is verified by the rehearsal in Task 8.

---

### Task 7: Base partitioning + 90-day TTL (runbook — separate migration)

**Not part of the code PR.** A StarRocks migration executed after Task 8 validation,
following the `session_events` redistribution playbook.

- [ ] Create partitioned twins: `<t>_p` with `PARTITION BY date_trunc('day', timestamp)`, `partition_live_number = 90`.
- [ ] Per table, low-ingest window: `PAUSE ROUTINE LOAD` → `INSERT INTO <t>_p SELECT *` → `ALTER TABLE ... RENAME` swap → recreate routine load on the new table → `RESUME`. `session_events` last, watching `ADMIN SHOW REPLICA STATUS` (replication_num=1).
- [ ] `EXPLAIN` a fresh-day total: confirm it prunes to one partition with the raw-`timestamp` bound present.
- [ ] Confirm old partitions actually drop (3.3.x `partition_live_number` — verify against StarRocks #39341) before relying on TTL.
- [ ] Only after summary validation is green: enable the 90-day TTL.

---

### Task 8: Cluster rehearsal + validation

**Not TDD-able** (StarRocks not in CI). Rehearse against the local stack, then dev, then prod.

- [ ] Backfill locally; run the 12 dashboard queries with `SummaryEnabled=true` and again with the flag off; assert **identical results** for windows fully inside current retention (this proves the union math).
- [ ] `EXPLAIN` + `fe.audit.log` `ScanRows` small for the six metrics on the summary path.
- [ ] Flip `SummaryEnabled` on only after backfill + the equality check pass.
- [ ] After Task 7 TTL: confirm a 12-month window still returns a full series matching the summary, not collapsing to the 90-day base.
- [ ] Capture findings in `docs/tasks/active/20260831-project-stats-long-retention-lessons.md`.

---

## Self-Review Notes

- Spec coverage: summary tables (T1), ingest+lookback (T6), dual-read split/union/fallback (T2–T5), base partition+TTL (T7), sequencing/validation (T8). All spec sections mapped.
- `SummaryEnabled` off = byte-identical current SQL, so the code PR is safe to merge before any cluster work — matching the spec's "ships dark, flip after validation".
- Peak sessions deliberately needs no cross-boundary union (MAX of independent per-day-per-channel cardinalities) — encoded in T4.
