# Read the sessions summary through rl_session_daily by name

**Created**: 2026-10-24

## Why

`GetProjectStats` on a 3-month window still misses the dashboard's 3 s deadline
for a project with thousands of channels. The sessions total and series are the
cost: their summary branch reads `sum_session_hll_daily_ch` at channel-day grain
(~390k sketches, ~1.2 GB, ~0.85 s warm and ~3.4 s cold on the largest project)
instead of the one-row-per-day `rl_session_daily` rollup #1976 added.

The rollup is picked when the summary branch is planned alone, and on StarRocks
3.3.9 it is picked inside the dual read too. On 3.3.22, the version production
runs, it is dropped whenever the fresh branch of the same `UNION ALL` is
rewritten onto the `mv_session_hll_daily_ch` sync MV. GROUP BY in the branch,
reordering the branches, a CTE, and `enable_sync_materialized_view_rewrite`
do not bring it back. `TestE2EDualReadStaysOnTheRollups` checks exactly this,
but the docker stack it runs on pins 3.3.9, so it passes.

Naming the rollup with the documented sync MV hint, `FROM rl_session_daily
[_SYNC_MV_]`, plans the summary on the rollup and the fresh branch on its MV on
both 3.3.9 and 3.3.22, with the same results.

## Plan

- [x] Bump `build/docker/analytics/docker-compose.yml` StarRocks to 3.3.22 and
      watch `TestE2EDualReadStaysOnTheRollups` fail on the sessions queries
- [x] Unit test: the sessions summary branch reads `rl_session_daily
      [_SYNC_MV_]`; the other metrics keep their summary table
- [x] `metricDesc.summaryRollup`, set for `descSession` only, used by
      `seriesQuery` and `totalQuery`
- [x] Rehearsal green on 3.3.22 (and the hint still valid on 3.3.9)
- [x] Update `docs/design/project-stats-long-retention.md`
- [x] `make verify`, self review
- [x] Make provisioning a gate on the rollup (docker init script and chart):
      add it only when missing, fail on a failed `ALTER`, wait on the hinted
      read and fail if it never resolves; add
      `TestSummaryRollupProvisioningIsAGate`
- [x] Rehearse the init script on 3.3.22: fresh stack, and a summary table
      created without the rollup
- [x] Fall back to the summary table while the hinted read does not resolve:
      the coverage probe asks it once per TTL; only `Unknown table` means no,
      any other error fails the probe; rehearsed on 3.3.22 with the rollup dropped
- [ ] PR; after release, `EXPLAIN` on production reads `rl_session_daily`

## Out of scope

- The fresh window (last ~2 days from the base MV) is the same cost for every
  window length; leave it.
- Peak reads `sum_session_peak_daily`, which has no rollup to lose.
