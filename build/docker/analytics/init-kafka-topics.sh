#!/bin/bash
set -e

echo -e 'Waiting for Kafka to be ready...'
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --list

echo -e 'Creating kafka topics'
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --create --if-not-exists --topic user-events --replication-factor 1 --partitions 1
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --create --if-not-exists --topic channel-events --replication-factor 1 --partitions 1
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --create --if-not-exists --topic session-events --replication-factor 1 --partitions 1

echo -e 'Successfully created the following topics:'
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --list

