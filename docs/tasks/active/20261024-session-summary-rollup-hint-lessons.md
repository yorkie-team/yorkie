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

## A rollup answers to the sync MV hint, once it has finished building

`[_SYNC_MV_]` is documented for synchronous materialized views, so the obvious
worry is that a plain rollup index is not reachable through it. Checked on a
3.3.22 stack: it is. Both `rl_session_daily` from the inline `ROLLUP (...)`
clause and a scratch index from `ALTER TABLE ... ADD ROLLUP` plan as
`TABLE: <base> / rollup: <index>` under the hint.

The one trap is timing. Straight after the `ALTER`, `desc <table> all` already
lists the new index while the same name in a `FROM` still fails with
`Unknown table`; a few seconds later it resolves. So a `desc` poll is the wrong
gate for a read that names the index — `SHOW ALTER TABLE ROLLUP FROM yorkie`
reporting `State: FINISHED` is.

## The hint widens a sessions failure into a whole-response failure

`GetProjectStats` fans its eleven warehouse reads out through one `errgroup`,
which returns on the first error. A cluster missing the rollup therefore loses
the entire project stats response, not just the sessions series and total. That
is what makes the `ALTER` an upgrade prerequisite rather than a tuning step, and
it is why the upgrade note says so rather than describing it as a sessions-only
degradation.

## Self review

- Round 2 (design fit): the hint's resolution against an `ADD ROLLUP` index and
  the build-completion gate verified on a local 3.3.22 stack; the blast radius
  of a missing rollup corrected in the upgrade note, the design doc and the
  `summarySource` comment. Known limitation, outside this change's files: the
  provisioning scripts still treat a missing `rl_session_daily` as non-fatal,
  and the chart adds it from a post-upgrade hook, so an upgrade can serve the
  hinted query before the index exists.
- Round 1 (correctness/tests): no blocking findings. Addressed: the rollup
  requirement moved to Upgrade Notes; sessions total pinned in
  `TestSteadyStateSQLIsUnchanged`; sessions cases added to the below-floor and
  empty-coverage tests with a `_SYNC_MV_` check. Known limitation: no
  degenerate-window (from == to) sessions case; the E2E rehearsal is not part of
  `make verify`.
