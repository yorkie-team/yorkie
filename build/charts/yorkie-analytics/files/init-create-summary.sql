USE yorkie;

-- Decoupled daily HLL summary tables that outlive the base event tables, so
-- long-retention dashboard windows survive raw-event TTL. Independent
-- AGGREGATE KEY tables (not base-bound rollups); filled by the summary
-- CronJob and read by the dual-read path in
-- server/backend/warehouse/starrocks.go. AGGREGATE KEY + HLL_UNION makes
-- inserts idempotent; partition_live_number retains ~15 months.
-- See docs/design/project-stats-long-retention.md.
--
-- sum_session_peak_daily holds no sketch: the daily peak sessions per
-- channel is independent per day, so it needs no cross-day union and is
-- stored as a plain integer keyed by (project_id, dt). That keeps the peak
-- read off the ~280k channel-day rows a 3-month window would otherwise scan
-- in sum_session_hll_daily_ch. BIGINT MAX is what makes a re-insert
-- idempotent, the role HLL_UNION plays for the sketch tables. It is created
-- last, away from the session table it belongs with: this file is piped to
-- mysql without --force and a failure below is only logged, so the newest
-- statement goes where it can take nothing else down with it.

CREATE TABLE IF NOT EXISTS sum_user_hll_daily (
    project_id VARCHAR(64), dt DATE, user_hll HLL HLL_UNION
) ENGINE = OLAP AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_document_hll_daily (
    project_id VARCHAR(64), dt DATE, document_hll HLL HLL_UNION
) ENGINE = OLAP AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_channel_hll_daily (
    project_id VARCHAR(64), dt DATE, channel_hll HLL HLL_UNION
) ENGINE = OLAP AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_session_hll_daily_ch (
    project_id VARCHAR(64), dt DATE, channel_key VARCHAR(128), session_hll HLL HLL_UNION
) ENGINE = OLAP AGGREGATE KEY(project_id, dt, channel_key)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
ROLLUP (rl_session_daily (project_id, dt, session_hll))
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_client_hll_daily (
    project_id VARCHAR(64), event_type VARCHAR(32), dt DATE, client_hll HLL HLL_UNION
) ENGINE = OLAP AGGREGATE KEY(project_id, event_type, dt)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_session_peak_daily (
    project_id VARCHAR(64), dt DATE, peak_sessions BIGINT MAX
) ENGINE = OLAP AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt) DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");
