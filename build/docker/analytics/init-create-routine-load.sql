CREATE ROUTINE LOAD yorkie.user_events ON user_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(     
    "kafka_broker_list" = "kafka:9092",  
    "kafka_topic" = "user-events",
    "property.group.id" = "user_events_group"
);

CREATE ROUTINE LOAD yorkie.document_events ON document_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "kafka:9092",
    "kafka_topic" = "document-events",
    "property.group.id" = "document_events_group"
);

CREATE ROUTINE LOAD yorkie.channel_events ON channel_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "kafka:9092",
    "kafka_topic" = "channel-events",
    "property.group.id" = "channel_events_group"
);

CREATE ROUTINE LOAD yorkie.session_events ON session_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "kafka:9092",
    "kafka_topic" = "session-events",
    "property.group.id" = "session_events_group"
);

CREATE ROUTINE LOAD yorkie.client_events ON client_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "kafka:9092",
    "kafka_topic" = "client-events",
    "property.group.id" = "client_events_group"
);
