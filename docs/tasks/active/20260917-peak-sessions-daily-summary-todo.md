**Created**: 2026-09-17

# Precompute the daily peak sessions per channel

Two issues, one PR, and one devops change that has to ship first.

- **yorkie-team/yorkie#1995 — the peak total repeats the peak series' scan.**
  `GetProjectStats` fans out twelve warehouse queries. The peak series and the
  peak total read the same per-(day, channel) buckets and reduce them with the
  same `MAX`, so the total is exactly the `MAX` of the series — one query
  recomputing what its sibling goroutine already has, the two contending on the
  same scan.
- **yorkie-team/yorkie#1996 — peak is the only metric that scales with
  channels.** Its summary, `sum_session_hll_daily_ch`, is keyed by channel, so a
  3-month read costs channels x days while every other metric costs days.

Measured on production StarRocks 3.3.9, 3-month window, a project with ~4,900
channels (~280k summary rows):

| query | time |
|---|---|
| peak sessions series | ~2.24s |
| peak sessions total | ~2.19s (identical value to the series' max) |
| every other metric | < 0.7s |

The dashboard's admin RPC deadline is a fixed 3s, and the 3-month view
intermittently exceeded it.

**Goal:** peak reads cost days, not channels x days, and the window total costs
nothing at all.

**Spec:** `docs/design/project-stats-long-retention.md` (extended, not replaced).

## Approach

1. `#1995`: derive the window peak in `server/projects/projects.go` as the max of
   the series after `g.Wait()`, and delete the total query outright. Exact for
   peak — `MAX` over independent buckets is associative — and exact *only* for
   peak: the other five metrics are distinct counts whose window total is an HLL
   union across days, not a max or a sum of daily values.
2. `#1996`: a new decoupled summary table, `sum_session_peak_daily
   (project_id, dt, peak_sessions BIGINT MAX)`, holding the finished daily peak
   as a plain integer. No sketch is needed because the daily peak is independent
   per day; `BIGINT MAX` plays the idempotence role `HLL_UNION` plays for the
   sketch tables. A 3-month read touches ~91 rows instead of ~280k and stops
   depending on channel cardinality.
3. Both writers derive it from `sum_session_hll_daily_ch`, not from
   `session_events`: it avoids a second full scan of a billion-row base, and the
   stored integer is then exactly the maximum of the per-day HLL estimates the
   read path used to compute itself, so history and fresh days agree by
   construction. It must run **after** the session-summary insert in the same
   script.

## Deployment hazard — read before merging

The coverage probe reads `MAX(dt)` from every summary table in one `UNION ALL`.
The moment a server with `SummaryEnabled` on names `sum_session_peak_daily`
against a cluster that lacks it, the probe fails for **every** metric and the
dashboard goes blank — not just the peak chart. So the order is fixed:

1. devops manifest lands and its init Job runs (table created + backfilled),
2. then the server version rolls out.

## Checklist

### Warehouse SQL (yorkie)

- [x] `build/docker/analytics/init-create-summary.sql`: add
      `sum_session_peak_daily`, with the reason it holds an integer rather than a
      sketch in a comment.
- [x] `build/docker/analytics/init-backfill-summary.sql`: add the peak insert
      **after** the `sum_session_hll_daily_ch` insert, with the ordering
      dependency spelled out at the statement.
- [x] `build/charts/yorkie-analytics/templates/starrocks/configmap.yaml`: mirror
      the `CREATE TABLE` under `init-create-summary.sql`. This ConfigMap carries
      create DDL only — backfill and refresh live in the devops manifest.

### Go read path (yorkie)

- [x] `server/backend/warehouse/metrics.go`: add `peakColumn` and a `descPeak`
      whose `summaryTable` is the new table; add it to `allDescs` so the coverage
      probe tracks the peak summary's own lag. Drop `byChannel` — nothing read
      it, and its rationale ("one table serves both sessions and peak") no longer
      holds.
- [x] `server/backend/warehouse/query.go`: history half of `peakSeriesQuery`
      reads `MAX(peak_sessions) ... GROUP BY dt` from the new table (aggregate,
      not a bare column read, so the result does not depend on the aggregate
      table having merged duplicate keys at read time). Fresh half unchanged.
      Delete `peakTotalQuery`.
- [x] Delete `GetPeakSessionsPerChannelCount` from the `Warehouse` interface,
      `DummyWarehouse`, and `StarRocks`.
- [x] `server/projects/projects.go`: drop the twelfth goroutine; take the max of
      `peakSessionsPerChannel` after `g.Wait()`.
- [x] Unplanned, added in review: the coverage probe reads `MIN(dt)` as well as
      `MAX(dt)`, coverage becomes a day range, and `splitWindow` cuts the window
      into three (base / summary / base). A watermark alone would have served a
      partially backfilled `sum_session_peak_daily` as complete. Steady-state SQL
      must stay byte-identical, pinned by a test.

### Tests (yorkie)

- [x] Golden-string tests for the new peak history half and for the coverage
      probe now naming six tables.
- [x] Drop the peak-total cases from `coverage_test.go` and
      `e2e_rehearsal_test.go`; add a case asserting the returned window peak
      equals the max of the returned series.
- [x] `make lint`, `go test ./...`.
- [x] End-to-end coverage test for `GetProjectStats`: nothing called it, so the
      derived peak total was only tested at the helper. A warehouse double
      serving a series whose maximum is neither its first nor its last point.
- [x] Local StarRocks rehearsal (`allin1-ubuntu:3.3.9`): backfill including the
      peak table, then dual-read vs base-only for all metrics, and
      `sum_session_peak_daily` vs a `MAX` over channels computed from
      `sum_session_hll_daily_ch` for the same days.

### devops (ships first)

- [x] `k8s/cluster/analytics-summary.yaml`: add the table to `create.sql`, the
      derived insert to `backfill.sql` and `refresh.sql` (after the session
      statement in both). Mirrored by hand — the ArgoCD application is still
      pinned at chart 0.6.0.
- [ ] Delete and re-apply `analytics-summary-init` (a Job's pod template is
      immutable), confirm the table is populated, then let the CronJob take over.
- [ ] Only then roll out the server.

### Docs

- [x] `docs/design/project-stats-long-retention.md`: new summary-table
      subsection, peak's ingest statement, the rewritten read-path paragraphs,
      the deployment-order hazard, and rows in Risks / Design Decisions /
      Alternatives Considered.
- [ ] Capture lessons, archive the pair (`scripts/tasks-archive.sh`,
      `scripts/tasks-index.sh`).

## See Also

- `docs/tasks/active/20260831-project-stats-long-retention-todo.md` — the design
  this extends, and the plan that built the other five summary tables
- `docs/design/project-stats-long-retention.md` — the spec
