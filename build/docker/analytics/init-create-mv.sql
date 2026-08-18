USE yorkie;

-- Synchronous materialized views holding one HLL sketch per (project, day).
-- StarRocks maintains them at ingest time and rewrites the dashboard's
-- APPROX_COUNT_DISTINCT queries onto them, so GetProjectStats reads a few
-- summary rows instead of full-scanning the event tables. The rewrite only
-- happens when the query filters on DATE(timestamp), which is why the warehouse
-- queries in server/backend/warehouse/starrocks.go use that expression.
-- See docs/design/project-stats-warehouse-mv.md.
--
-- Only the client view carries event_type, because its query filters on it. The
-- others omit it so that GROUP BY DATE(timestamp) maps to a clean key prefix.

CREATE MATERIALIZED VIEW mv_user_hll_daily AS
    SELECT project_id, DATE(timestamp) AS dt,
           HLL_UNION(HLL_HASH(user_id)) AS user_hll
    FROM user_events
    GROUP BY project_id, DATE(timestamp);

CREATE MATERIALIZED VIEW mv_document_hll_daily AS
    SELECT project_id, DATE(timestamp) AS dt,
           HLL_UNION(HLL_HASH(document_key)) AS document_hll
    FROM document_events
    GROUP BY project_id, DATE(timestamp);

CREATE MATERIALIZED VIEW mv_channel_hll_daily AS
    SELECT project_id, DATE(timestamp) AS dt,
           HLL_UNION(HLL_HASH(channel_key)) AS channel_hll
    FROM channel_events
    GROUP BY project_id, DATE(timestamp);

CREATE MATERIALIZED VIEW mv_session_hll_daily_ch AS
    SELECT project_id, DATE(timestamp) AS dt, channel_key,
           HLL_UNION(HLL_HASH(session_id)) AS session_hll
    FROM session_events
    GROUP BY project_id, DATE(timestamp), channel_key;

CREATE MATERIALIZED VIEW mv_client_hll_daily AS
    SELECT project_id, event_type, DATE(timestamp) AS dt,
           HLL_UNION(HLL_HASH(client_id)) AS client_hll
    FROM client_events
    GROUP BY project_id, event_type, DATE(timestamp);
