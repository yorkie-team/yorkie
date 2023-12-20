#!/bin/sh
echo "Run the Config server, Shard 1 and Shard 2"
docker compose -f build/docker/sharding/docker-compose.yml up --build -d --wait config1 shard1-1 shard2-1
echo $?
echo "init config"
docker compose -f build/docker/sharding/docker-compose.yml exec config1 mongosh test /scripts/init-config1.js
echo $?
echo "init shard1"
docker compose -f build/docker/sharding/docker-compose.yml exec shard1-1 mongosh test /scripts/init-shard1-1.js
echo $?
echo "init shard2"
docker compose -f build/docker/sharding/docker-compose.yml exec shard2-1 mongosh test /scripts/init-shard2-1.js
echo $?
echo "Run the Mongos"
docker compose -f build/docker/sharding/docker-compose.yml up --build -d --wait mongos1
echo $?
echo "init mongos1"
docker compose -f build/docker/sharding/docker-compose.yml exec mongos1 mongosh test /scripts/init-mongos1.js
echo $?
