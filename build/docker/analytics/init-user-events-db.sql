CREATE DATABASE IF NOT EXISTS yorkie;

USE yorkie;

CREATE TABLE user_events (
    user_id VARCHAR(64),
    timestamp DATETIME, 
    event_type VARCHAR(32),
    project_id VARCHAR(64),
    user_agent VARCHAR(32),
    metadata JSON
) ENGINE = OLAP  
DUPLICATE KEY(user_id)  
DISTRIBUTED BY HASH(user_id) BUCKETS 10
PROPERTIES (  
    "replication_num" = "1"  
);
