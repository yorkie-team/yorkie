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

## A rollup answers to the sync MV hint, once its build has finished

`[_SYNC_MV_]` is documented for synchronous materialized views, so the obvious
worry is that a plain rollup index is not reachable through it. Checked on a
3.3.22 stack: it is. Both `rl_session_daily` from the inline `ROLLUP (...)`
clause and a scratch index from `ALTER TABLE ... ADD ROLLUP` plan as
`TABLE: <base> / rollup: <index>` under the hint.

The trap is timing. Measured on a 3M-row scratch table: for ~10 s the job is
`PENDING` and `desc <table> all` does not list the index; then `desc` lists it
while the job is `RUNNING`; the hinted read resolves when the job reaches
`FINISHED` (~20 s), not before. So the provisioning waits on the hinted read,
which resolves only once the build has finished, and decides whether to
`ALTER` from the read and `SHOW ALTER TABLE ROLLUP`, not from `desc`: a rerun
during `PENDING` would otherwise issue a second `ALTER` and fail. `SHOW ALTER
TABLE ROLLUP ... WHERE TableName = ...` ignores the filter on 3.3.22, so the
script filters the rows itself.

## Naming an index gives up the optimizer's fallback

Left to the optimizer, a missing rollup costs a slower plan. Named with the
hint, it is `Unknown table`, and `GetProjectStats` fans its reads out through
one `errgroup`, so one failed read is the whole response. The read path has to
take the fallback back itself, and per read: a sessions read answered with
`Unknown table` runs again on the summary table. Only that message means the
rollup does not resolve; StarRocks reports every analysis error as 5502, and a
timeout or a lost BE also arrives as a server error.

Two cached designs were tried first and both failed review. A probe that
failed on an inconclusive answer widened a sessions-only question into every
metric's failure, since the probe is shared. A probe that kept the hint on an
inconclusive answer cached "resolves" for a TTL on a cluster still building
the rollup, and every response in that minute failed. A verdict cached away
from the query it is about goes stale; asking the query itself does not.

## An optimization does not fail the release

Once the server degrades instead of failing, the rollup is an optimization
again, like the views, and the scripts have no reason to fail on it. Earlier
rounds failed the run after a 10-minute wait, then on a failed `ALTER`; either
failed the whole analytics release, and with the Job's `backoffLimit: 10`
reran the whole script. Logging what went wrong is enough, since the server
logs every read that falls back.

## Hook order does not reach another release

The chart's init Job is a `post-install,post-upgrade` hook, which looks like it
runs after the server is already serving. It cannot be a `pre-*` hook: it talks
to the StarRocks that the same release deploys. And the server is the
`yorkie-cluster` release, not this one, so no hook order in `yorkie-analytics`
holds the server rollout back. The order between them is the operator's: turn
on `starrocksSummaryEnabled`, or upgrade the server, once the rollup resolves.

## Self review

- Final state: the sessions reads name the rollup, and a read answered with
  `Unknown table` runs again on the summary table; any other error is returned
  as is. Both provisioning scripts add the rollup after the routine loads
  (when missing and not already building) and wait up to two minutes on the
  hinted read, logging rather than failing on anything in that step;
  `TestSummaryRollupProvisioningNeverFailsTheRun` checks that shape and was
  mutation-tested (an unguarded `ALTER` or `SHOW ALTER` under `set -e`, an
  `exit 1` on either, an `exit 1` on timeout are all caught); the chart Job
  stays a post hook.
- The fallback was checked on 3.3.22 with the rollup dropped: the dual-read
  rehearsal fails with `Unknown table` before it and returns the same numbers
  as with the rollup after.
- Provisioning outside this repo should wait the same way: the devops
  manifest that provisions production still tolerates a failed `ALTER` and
  polls `desc`. Production already has the rollup, so it is a follow-up there.
