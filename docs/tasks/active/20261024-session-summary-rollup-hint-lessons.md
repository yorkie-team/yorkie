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
`Unknown table`; a few seconds later it resolves. So the provisioning waits on
the read the server sends, `SELECT 1 FROM rl_session_daily [_SYNC_MV_]`, rather
than on `desc`. That also covers an index from the inline `ROLLUP` clause,
which has no alter job for `SHOW ALTER TABLE ROLLUP` to report.

## A hard dependency moves its gate into provisioning

`GetProjectStats` fans its warehouse reads out through one `errgroup`, so a
cluster missing the rollup loses the entire project stats response. Once the
read names the index, the scripts that create it have to fail when they cannot,
or a cluster is reported as provisioned when it is not. Documenting the gap
instead of closing it kept the review open for five rounds.

## Hook order does not reach another release

The chart's init Job is a `post-install,post-upgrade` hook, which looks like it
runs after the server is already serving. It cannot be a `pre-*` hook: it talks
to the StarRocks that the same release deploys. And the server is the
`yorkie-cluster` release, not this one, so no hook order in `yorkie-analytics`
holds the server rollout back. The gate between the two is
`starrocksSummaryEnabled`, which defaults off and is turned on after the Job
succeeds.

## Self review

- Round 6 (by hand): the provisioning scripts and the chart moved into this
  change. Both now add the rollup only when it is missing, fail on a failed
  `ALTER`, and wait on the hinted read, failing when it never resolves.
  `TestSummaryRollupProvisioningIsAGate` runs instead of being skipped. The
  Job stays a post hook, for the reason above. The `auth_webhook_test.go`
  change from round 5 was reverted as outside this change.
- Round 5: the provisioning gap is the whole of the remaining review surface —
  the rollup is created by both scripts but neither treats the `ALTER` as a
  gate, and the chart's Job is a `post-upgrade` hook. Closing it means editing
  `build/docker/analytics/init-starrocks-database.sh` and the chart's
  `configmap.yaml`/`job.yaml`, none of which this change touches, so it is
  carried rather than fixed. What this round adds is a mechanical handle on it:
  `TestSummaryRollupProvisioningIsAGate` spells out the three conditions — a
  non-tolerant `ALTER`, a `SHOW ALTER TABLE ROLLUP` wait, and a Job that is not
  a post-upgrade hook — and is skipped, so whoever moves those files deletes one
  `t.Skip` instead of reconstructing the requirement from the upgrade note.
  Nothing in the read path can soften the dependency on its own: `summarySource`
  builds a SQL string, and SQL has no conditional table resolution, so naming
  the index and tolerating its absence are not both available.
- Round 4: added `TestSummaryRollupsAreProvisioned`, a plain unit test that
  every descriptor naming a `summaryRollup` is created by both provisioning
  paths the repo ships — the inline `ROLLUP (...)` clause for a fresh cluster
  and the `ALTER ... ADD ROLLUP` for a summary table that predates it. CI has
  no StarRocks, so it cannot check that a given cluster ran them; what it does
  check is that the index the server names and the index the scripts create
  cannot drift apart silently. The runbook step 1 and the upgrade note now say
  outright that those scripts are not the deployment gate, since both continue
  past a failed `ALTER` and the chart's init job is a post-upgrade hook.
  Changing the scripts themselves stays outside this change's files.
- Round 3 (blast radius): the deployment runbook in
  `docs/design/project-stats-long-retention.md` still opened with "the base scan
  is always a correct fallback" and gated the rollup build on `desc … all`, both
  untrue for the sessions reads once they name the index; corrected, and the
  rollup stated as a prerequisite of the server rollout rather than only of the
  validation step. Still outside this change's files, so unchanged here: the
  docker init script and the chart configmap swallow a failed `ADD ROLLUP`, poll
  `desc … all` rather than `SHOW ALTER TABLE ROLLUP`, and the chart runs the
  `ALTER` from a `post-upgrade` hook.
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
