USE yorkie;

-- One-time backfill of the decoupled daily HLL summaries from the full base
-- history. Idempotent: AGGREGATE KEY + HLL_UNION merges re-inserted days, so
-- this can be re-run safely. The scheduled daily job repeats the same SELECT
-- for a trailing lookback window (see the devops repo's summary CronJob).
--
-- Every statement stops before the running UTC day. The read path splits at the
-- summary's MAX(dt) + 1 and so trusts every day at or below MAX(dt) as
-- complete; writing a partial current day would make that day undercount from
-- the next UTC midnight until a later run merges the rest of it in.
--
-- On large clusters run these per statement in a low-ingest window rather than
-- all at once; each base statement is a single full scan, the session one the
-- heaviest. The order of the base statements is free, with one exception: the
-- sum_session_peak_daily statement reads sum_session_hll_daily_ch, so it has
-- nothing to work with until the session statement has landed. It is written
-- last for that reason and for one more, given at the statement itself. See
-- docs/design/project-stats-long-retention.md and the MV migration playbook.

-- HLL_HASH is per-row; grouping into the HLL_UNION column requires the
-- HLL_UNION aggregate around it (a bare HLL_HASH under GROUP BY is rejected as
-- "must be an aggregate expression"). This matches the mv_*_hll_daily DDL.

INSERT INTO sum_user_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(user_id))
FROM user_events
WHERE DATE(timestamp) < DATE(UTC_TIMESTAMP())
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_document_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(document_key))
FROM document_events
WHERE DATE(timestamp) < DATE(UTC_TIMESTAMP())
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_channel_hll_daily
SELECT project_id, DATE(timestamp), HLL_UNION(HLL_HASH(channel_key))
FROM channel_events
WHERE DATE(timestamp) < DATE(UTC_TIMESTAMP())
GROUP BY project_id, DATE(timestamp);

INSERT INTO sum_session_hll_daily_ch
SELECT project_id, DATE(timestamp), channel_key, HLL_UNION(HLL_HASH(session_id))
FROM session_events
WHERE DATE(timestamp) < DATE(UTC_TIMESTAMP())
GROUP BY project_id, DATE(timestamp), channel_key;

INSERT INTO sum_client_hll_daily
SELECT project_id, event_type, DATE(timestamp), HLL_UNION(HLL_HASH(client_id))
FROM client_events
WHERE DATE(timestamp) < DATE(UTC_TIMESTAMP())
GROUP BY project_id, event_type, DATE(timestamp);

-- Peak sessions per channel, as a plain integer per (project_id, dt).
--
-- This statement MUST run after the sum_session_hll_daily_ch INSERT above: it
-- reads the rows that statement wrote, not session_events. It is last rather
-- than directly after it because mysql stops at the first failing statement and
-- the wrapper only logs that it "could not run the summary backfill": a failure
-- here would otherwise skip the client backfill, leaving that summary empty,
-- its coverage at nothing, and its reads on whole-history base scans with
-- nothing to say so.
--
-- Deriving from the summary rather than the base is deliberate. The summary is
-- orders of magnitude smaller -- one row per (project, day, channel) against a
-- billion raw session_events rows -- so this costs a scan of the summary rather
-- than a second full scan of the base. It also makes the stored integer exactly
-- the MAX of the per-day HLL estimates the read path would compute from
-- sum_session_hll_daily_ch itself, so the precomputed history and the freshly
-- computed days agree by construction instead of by two estimators happening to
-- land on the same number.
--
-- BIGINT MAX on the target column is what makes a re-insert idempotent, the
-- same role HLL_UNION plays for the sketch tables: re-running a day takes the
-- max of the old and new values instead of appending a second row.
INSERT INTO sum_session_peak_daily
SELECT project_id, dt, MAX(session_count) FROM (
    SELECT project_id, dt, channel_key, HLL_UNION_AGG(session_hll) AS session_count
    FROM sum_session_hll_daily_ch
    WHERE dt < DATE(UTC_TIMESTAMP())
    GROUP BY project_id, dt, channel_key
) c
GROUP BY project_id, dt;
