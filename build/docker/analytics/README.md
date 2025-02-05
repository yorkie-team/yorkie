# StarRocks Analytics Stack

These files deploy and set up a StarRocks analytics stack using `docker compose` for development and testing purposes.

The stack consists of the following components:

- StarRocks Frontend (FE): Query coordinator and metadata manager
- StarRocks Backend (BE): Data storage and query execution engine
- Kafka: Message broker for event streaming
- Kafka UI: Web interface for Kafka monitoring
- Init Services: Database(`yorkie`), Table(`user_events`) and topic(`user-events`) initialization scripts

## How To Use

```sh
# Start the analytics stack
docker compose -f build/docker/analytics/docker-compose.yml up -d

# Open the Kafka UI
open http://localhost:8989

# Run StarRocks SQL client
docker exec -it starrocks-fe mysql -P 9030 -h starrocks-fe -u root --prompt="StarRocks > "

# Shut down the stack
docker compose -f build/docker/analytics/docker-compose.yml down
```

The files used are as follows:

- docker-compose.yml: Defines the StarRocks and Kafka services
- init-user-events-db.sql: Creates the Yorkie database and tables
- init-routine-load.sql: Sets up Kafka routine load jobs

Key services:

- StarRocks FE (ports: 8030, 9020, 9030)
- StarRocks BE (port: 8040)
- Kafka (port: 9092)
- Kafka UI (port: 8989)

The initialization services will:

- Start the StarRocks FE/BE nodes
- Create the required Kafka topics
- Initialize the StarRocks database and tables
- Configure the routine load from Kafka to StarRocks

## For Setup Kafka Cluster Mode

To set up Kafka in cluster mode, refer to the [Bitnami Kafka README.md](https://github.com/bitnami/containers/blob/main/bitnami/kafka/README.md) and [docker-compose-cluster.yml](https://github.com/bitnami/containers/blob/main/bitnami/kafka/docker-compose-cluster.yml) for detailed instructions on setting up Kafka in cluster mode using Docker Compose.

## About StarRocks with Kafka Routine Load

To use StarRocks with Kafka routine load, follow the [StarRocks Routine Load Quick Start Guide](https://docs.starrocks.io/docs/quick_start/routine-load/). This guide provides detailed instructions on setting up routine load jobs to ingest data from Kafka into StarRocks. Ensure that your Kafka and StarRocks instances are properly configured and running before starting the integration process.

### How To Check Routine Load Status

To check routine load status or fix a paused routine load, follow these steps:

1. Connect to StarRocks Frontend (FE) using the following command:

   ```sh
   docker exec -it starrocks-fe mysql -P 9030 -h starrocks-fe -u root --prompt="StarRocks > "
   ```

2. Check the status of the routine load:

   ```sql
   StarRocks > SHOW ROUTINE LOAD FROM yorkie;
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
   StarRocks > SHOW ROUTINE LOAD FROM yorkie;
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
