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
which is the build-finished gate, and decides whether to `ALTER` from the
read and `SHOW ALTER TABLE ROLLUP`, not from `desc`: a rerun during `PENDING`
would otherwise issue a second `ALTER` and fail. `SHOW ALTER TABLE ROLLUP ...
WHERE TableName = ...` ignores the filter on 3.3.22, so the script filters
the rows itself.

## A hard dependency moves its gate into provisioning

`GetProjectStats` fans its warehouse reads out through one `errgroup`, so a
cluster missing the rollup loses the entire project stats response. Once the
read names the index, the scripts that create it have to fail when they cannot,
or a cluster is reported as provisioned when it is not.

## Hook order does not reach another release

The chart's init Job is a `post-install,post-upgrade` hook, which looks like it
runs after the server is already serving. It cannot be a `pre-*` hook: it talks
to the StarRocks that the same release deploys. And the server is the
`yorkie-cluster` release, not this one, so no hook order in `yorkie-analytics`
holds the server rollout back. The gate between the two is
`starrocksSummaryEnabled`, which defaults off and is turned on after the Job
succeeds.

## Self review

- Final state: the sessions reads name the rollup; both provisioning scripts
  treat it as a gate, after the routine loads (add when missing and not
  already building, fail on a failed `ALTER`, wait up to 10 minutes on the
  hinted read, fail otherwise); `TestSummaryRollupProvisioningIsAGate` checks
  that shape and was mutation-tested (`|| echo`, a continued `|| echo`,
  `|| true`, `break`, no `set -e` are all caught); the chart Job stays a post
  hook.
- Provisioning outside this repo has to follow the same gate: the devops
  manifest that provisions production still tolerates a failed `ALTER` and
  polls `desc`. Production already has the rollup, so it is a follow-up there.
- Known limitation: `GetProjectStats` still fails whole when one read fails;
  the flag gate keeps the hinted read off clusters without the rollup.
