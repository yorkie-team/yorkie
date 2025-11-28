CREATE DATABASE IF NOT EXISTS yorkie;

USE yorkie;

CREATE TABLE IF NOT EXISTS user_events (
    user_id VARCHAR(64),
    timestamp DATETIME, 
    event_type VARCHAR(32),
    project_id VARCHAR(64),
    user_agent VARCHAR(32)
) ENGINE = OLAP  
DUPLICATE KEY(user_id)  
DISTRIBUTED BY HASH(user_id) BUCKETS 10
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
DISTRIBUTED BY HASH(project_id) BUCKETS 16
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
DISTRIBUTED BY HASH(project_id) BUCKETS 16
PROPERTIES (
    "replication_num" = "1"
);
