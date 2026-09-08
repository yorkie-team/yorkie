USE yorkie;

-- Decoupled daily HLL summary tables. Unlike the synchronous materialized views
-- in init-create-mv.sql (which are rollup indexes physically bound to the base
-- event tables and share their lifetime), these are independent AGGREGATE KEY
-- tables. They are filled by a scheduled idempotent job and survive base-table
-- partition drops, so long-retention dashboard windows keep working after
-- raw-event TTL is enabled. The dual-read path in
-- server/backend/warehouse/starrocks.go reads them for [from, today) and the
-- base rollups for today. See docs/design/project-stats-long-retention.md.
--
-- AGGREGATE KEY + HLL_UNION makes re-inserting a day idempotent (the sketch
-- merges). partition_live_number retains ~15 months (12-month product window +
-- buffer). Only the client table carries event_type and only the session table
-- carries channel_key, mirroring init-create-mv.sql.
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

-- The session summary is keyed by channel_key because peak sessions per channel
-- needs that dimension, but the plain sessions total/series do not: they union
-- every channel-day sketch of a project just to count distinct sessions. On a
-- project with high channel cardinality that is millions of sketches per read.
-- The rl_session_daily rollup holds the same sketches pre-merged to
-- (project_id, dt), so those two metrics scan one row per day while peak keeps
-- using the base index. StarRocks maintains it on insert and picks it
-- automatically, so the refresh job and the read path stay unchanged.
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
