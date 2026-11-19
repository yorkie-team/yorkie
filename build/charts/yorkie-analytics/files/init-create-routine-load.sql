CREATE ROUTINE LOAD yorkie.user_events ON user_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(   
    "kafka_broker_list" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-broker-list" }}",
    "kafka_topic" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-user-events" "topic" }}",
    "property.group.id" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-user-events" "property-group-id" }}"
);

CREATE ROUTINE LOAD yorkie.document_events ON document_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(   
    "kafka_broker_list" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-broker-list" }}",
    "kafka_topic" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-document-events" "topic" }}",
    "property.group.id" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-document-events" "property-group-id" }}"
);

CREATE ROUTINE LOAD yorkie.channel_events ON channel_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-broker-list" }}",
    "kafka_topic" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-channel-events" "topic" }}",
    "property.group.id" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-channel-events" "property-group-id" }}"
);

CREATE ROUTINE LOAD yorkie.session_events ON session_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-broker-list" }}",
    "kafka_topic" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-session-events" "topic" }}",
    "property.group.id" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-session-events" "property-group-id" }}"
);

CREATE ROUTINE LOAD yorkie.client_events ON client_events
PROPERTIES
(
    "format" = "JSON",
    "desired_concurrent_number"="1"
)
FROM KAFKA
(
    "kafka_broker_list" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-broker-list" }}",
    "kafka_topic" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-client-events" "topic" }}",
    "property.group.id" = "{{ index .Values "yorkie-analytics" "starrocks" "routine-load" "kafka-client-events" "property-group-id" }}"
);
