# Yorkie Analytics

## Kafka and StarRocks Setup

- **kafka, kafka-ui**: Message broker for event streaming.
- **starrocks-fe, starrocks-be**: Data warehouse components.
- **init-kafka-topic**: Creates the `user-events` Kafka topic.
- **init-starrocks-database**: Creates the `yorkie` database, `user_events` table, and sets up the `events` routine load.

## Setup Kafka Cluster Mode

To set up Kafka in cluster mode, refer to the [Bitnami Kafka README.md](https://github.com/bitnami/containers/blob/main/bitnami/kafka/README.md) and [docker-compose-cluster.yml](https://github.com/bitnami/containers/blob/main/bitnami/kafka/docker-compose-cluster.yml) for detailed instructions on setting up Kafka in cluster mode using Docker Compose.

## StarRocks with Kafka Routine Load

To use StarRocks with Kafka routine load, follow the [StarRocks Routine Load Quick Start Guide](https://docs.starrocks.io/docs/quick_start/routine-load/). This guide provides detailed instructions on setting up routine load jobs to ingest data from Kafka into StarRocks. Ensure that your Kafka and StarRocks instances are properly configured and running before starting the integration process.

### How To Fix Pause Routine Load

To check routine load status or fix a paused routine load, follow these steps:

1. Connect to StarRocks Frontend (FE) using the following command:

   ```sh
   docker exec -it starrocks-fe mysql -P 9030 -h starrocks-fe -u root --prompt="StarRocks > "
   ```

2. Check the status of the routine load:

   ```sql
   StarRocks > SHOW ROUTINE LOAD FROM yorkie\G
   ```

   Example output:

   ```
   *************************** 1. row ***************************
                           Id: 17031
                        Name: events
                           ...
                     DbName: yorkie
                 TableName: user_events
                       State: PAUSE
           DataSourceType: KAFKA
                           ...
   DataSourceProperties: {"topic":"user-events","currentKafkaPartitions":"0","brokerList":"kafka:9092"}
        CustomProperties: {"group.id":"user_events_group"}
                           ...
   ```

3. Resume the paused routine load:

   ```sql
   StarRocks > RESUME ROUTINE LOAD FOR events;
   ```

4. Verify that the routine load is running:

   ```sql
   StarRocks > SHOW ROUTINE LOAD FROM yorkie\G
   ```

   Example output:

   ```
   *************************** 1. row ***************************
                           Id: 17031
                        Name: events
                           ...
                     DbName: yorkie
                 TableName: user_events
                       State: RUNNING
           DataSourceType: KAFKA
                           ...
   DataSourceProperties: {"topic":"user-events","currentKafkaPartitions":"0","brokerList":"kafka:9092"}
        CustomProperties: {"group.id":"user_events_group"}
                           ...
   ```
