CREATE ROUTINE LOAD yorkie.events ON user_events
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
