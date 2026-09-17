USE yorkie;

-- Decoupled daily HLL summary tables. Unlike the synchronous materialized views
-- in init-create-mv.sql (which are rollup indexes physically bound to the base
-- event tables and share their lifetime), these are independent AGGREGATE KEY
-- tables. They are filled by a scheduled idempotent job and survive base-table
-- partition drops, so long-retention dashboard windows keep working after
-- raw-event TTL is enabled. The dual-read path in
-- server/backend/warehouse/starrocks.go reads them for the days they cover and
-- the base rollups for the rest. See docs/design/project-stats-long-retention.md.
--
-- AGGREGATE KEY + HLL_UNION makes re-inserting a day idempotent (the sketch
-- merges). partition_live_number retains ~15 months (12-month product window +
-- buffer). Only the client table carries event_type and only the session table
-- carries channel_key, mirroring init-create-mv.sql.
--
-- sum_session_peak_daily is the one summary that holds no sketch: peak sessions
-- per channel is a MAX over per-(day, channel) distinct counts, and the daily
-- peak is independent per day, so the read path never unions it across days. A
-- plain BIGINT MAX column is enough, and it plays the same idempotence role
-- HLL_UNION plays for the sketch tables -- re-inserting a day takes the max
-- instead of duplicating the row.
--
-- This file is the source of truth for the summary DDL. The deployed clusters
-- carry the same statements in the analytics chart's init ConfigMap
-- (build/charts/yorkie-analytics/templates/starrocks/configmap.yaml); keep the
-- two in sync when changing a column, key, or partition_live_number.

CREATE TABLE IF NOT EXISTS sum_user_hll_daily (
    project_id VARCHAR(64),
    dt         DATE,
    user_hll   HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_document_hll_daily (
    project_id   VARCHAR(64),
    dt           DATE,
    document_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

CREATE TABLE IF NOT EXISTS sum_channel_hll_daily (
    project_id  VARCHAR(64),
    dt          DATE,
    channel_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");

-- channel_key in the key is what peak sessions per channel needs; the plain
-- sessions total/series do not want it and would union every channel-day sketch
-- of a project to count distinct sessions. The rl_session_daily rollup holds
-- those sketches pre-merged to (project_id, dt), which StarRocks picks for the
-- reads that omit channel_key. See docs/design/project-stats-long-retention.md
-- for the ALTER TABLE that adds it to a summary table created before this.
CREATE TABLE IF NOT EXISTS sum_session_hll_daily_ch (
    project_id  VARCHAR(64),
    dt          DATE,
    channel_key VARCHAR(128),
    session_hll HLL HLL_UNION
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt, channel_key)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
ROLLUP (rl_session_daily (project_id, dt, session_hll))
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

-- Peak sessions per channel keyed by channel is the only metric whose summary
-- read scales with channel cardinality: a 3-month window on a project with
-- ~4,900 channels reads ~280k rows from sum_session_hll_daily_ch, about two
-- thirds of the dashboard's 3s admin-RPC deadline. The daily peak is
-- independent per day -- peak(day) = MAX over that day's channels of its
-- distinct sessions -- so it is stored per (project_id, dt) as a plain integer.
-- The same 3-month window then reads ~91 rows and no longer depends on how many
-- channels the project has. No sketch and no cross-day union are needed, which
-- is why this is an integer table rather than a sixth HLL table.
--
-- It is last on purpose, out of the session grouping it belongs to by subject.
-- The init scripts pipe this file to mysql without --force and only log that
-- the summaries "may already exist" when it exits non-zero, so a statement that
-- fails takes every statement below it with it, quietly. The newest table is
-- the one most likely to hit an engine that will not take it, and last is where
-- that costs nothing but itself. Its backfill statement is last of its file for
-- the same reason, and has to run after the session summary it reads -- see
-- init-backfill-summary.sql.
CREATE TABLE IF NOT EXISTS sum_session_peak_daily (
    project_id    VARCHAR(64),
    dt            DATE,
    peak_sessions BIGINT MAX
) ENGINE = OLAP
AGGREGATE KEY(project_id, dt)
PARTITION BY date_trunc('day', dt)
DISTRIBUTED BY HASH(project_id)
PROPERTIES ("replication_num" = "1", "partition_live_number" = "465");
