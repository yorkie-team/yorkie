USE yorkie;

-- One-time backfill of the decoupled daily HLL summaries from the full base
-- history. Idempotent: AGGREGATE KEY + HLL_UNION merges re-inserted days, so
-- this can be re-run safely. The scheduled daily job repeats the same SELECT
-- for a trailing lookback window (see the devops repo's summary CronJob).
--
-- On large clusters run these per table in a low-ingest window (session last)
-- rather than all at once; each is a single base full scan. See
-- docs/design/project-stats-long-retention.md and the MV migration playbook.

-- HLL_HASH is per-row; grouping into the HLL_UNION column requires the
-- HLL_UNION aggregate around it (a bare HLL_HASH under GROUP BY is rejected as
-- "must be an aggregate expression"). This matches the mv_*_hll_daily DDL.

INSERT INTO sum_user_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(user_id))
FROM user_events
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_document_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(document_key))
FROM document_events
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_channel_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(channel_key))
FROM channel_events
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_session_hll_daily_ch
SELECT project_id, DATE(timestamp), channel_key, HLL_UNION(HLL_HASH(session_id))
FROM session_events
GROUP BY project_id, DATE(timestamp), channel_key;

INSERT INTO sum_client_hll_daily
SELECT project_id, event_type, DATE(timestamp), HLL_UNION(HLL_HASH(client_id))
FROM client_events
GROUP BY project_id, event_type, DATE(timestamp);
