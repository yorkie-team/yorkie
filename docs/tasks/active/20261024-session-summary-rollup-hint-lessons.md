# Lessons: read the sessions summary through rl_session_daily by name

## A plan test is only as good as the engine it runs on

`TestE2EDualReadStaysOnTheRollups` asserted the right thing — the sessions
series and total plan onto `rl_session_daily` in the real dual-read shape — and
passed, because the local stack pinned StarRocks 3.3.9. Production runs 3.3.22,
where the same query drops the rollup once the fresh branch rewrites onto its
sync MV. Pin the rehearsal stack to the production engine, and re-check plan
assumptions whenever the chart's `3.3-latest` moves.

## Check a plan in the shape the server sends

The plan check after #1976 used the summary branch alone, which picks the
rollup on every version. Only the full `UNION ALL` with a fresh branch on a sync
MV shows the regression.

## Sync MV hint placement

`[_SYNC_MV_]` goes after the MV or rollup name: `FROM rl_session_daily
[_SYNC_MV_]`. `FROM sum_session_hll_daily_ch [rl_session_daily]` parses and is
silently ignored; `FROM sum_session_hll_daily_ch [_SYNC_MV_ rl_session_daily]`
is a syntax error.

## Self review

- Round 1 (correctness/tests): no blocking findings. Addressed: the rollup
  requirement moved to Upgrade Notes; sessions total pinned in
  `TestSteadyStateSQLIsUnchanged`; sessions cases added to the below-floor and
  empty-coverage tests with a `_SYNC_MV_` check. Known limitation: no
  degenerate-window (from == to) sessions case; the E2E rehearsal is not part of
  `make verify`.
