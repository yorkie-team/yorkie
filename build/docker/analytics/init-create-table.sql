CREATE DATABASE IF NOT EXISTS yorkie;

USE yorkie;

-- The event tables are partitioned by day so that retention can later be
-- expressed as a partition drop instead of a delete. No TTL is set here, on
-- purpose. A TTL drop is forced and unrecoverable, and the summary tables are
-- what serve windows older than the raw retention -- they are filled by a
-- separate ingest job and read only when the server's SummaryEnabled flag is
-- on. Until both are in place, a raw-event TTL silently truncates long
-- windows. Retention is therefore the last step of the rollout, applied
-- deliberately per cluster with
--   ALTER TABLE <t> SET ("partition_ttl" = "90 DAY");
-- once the summaries have been validated. See the deployment sequencing in
-- docs/design/project-stats-long-retention.md.

CREATE TABLE IF NOT EXISTS user_events (
    project_id VARCHAR(64),
    user_id VARCHAR(64),
    timestamp DATETIME,
    event_type VARCHAR(32),
    user_agent VARCHAR(32)
) ENGINE = OLAP
DUPLICATE KEY(project_id, user_id, timestamp)
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM
PROPERTIES (
    "replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS document_events (
    project_id VARCHAR(64),
    document_key VARCHAR(128),
    actor_id VARCHAR(64),
    timestamp DATETIME,
    event_type VARCHAR(32)
) ENGINE = OLAP  
DUPLICATE KEY(project_id, document_key, actor_id, timestamp)  
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM
PROPERTIES (  
    "replication_num" = "1"
);  

CREATE TABLE IF NOT EXISTS channel_events (
    project_id VARCHAR(64),
    channel_key VARCHAR(128),
    timestamp DATETIME,
    event_type VARCHAR(32)
) ENGINE = OLAP
DUPLICATE KEY(project_id, channel_key, timestamp)
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM
PROPERTIES (
    "replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS session_events (
    project_id VARCHAR(64),
    session_id VARCHAR(64),
    timestamp DATETIME,
    user_id VARCHAR(64),
    channel_key VARCHAR(128),
    event_type VARCHAR(32)
) ENGINE = OLAP
DUPLICATE KEY(project_id, session_id, timestamp)
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM
PROPERTIES (
    "replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS client_events (
    project_id VARCHAR(64),
    client_id VARCHAR(64),
    timestamp DATETIME,
    event_type VARCHAR(32)
) ENGINE = OLAP
DUPLICATE KEY(project_id, client_id, timestamp)
PARTITION BY date_trunc('day', timestamp)
DISTRIBUTED BY RANDOM
PROPERTIES (
    "replication_num" = "1"
);
