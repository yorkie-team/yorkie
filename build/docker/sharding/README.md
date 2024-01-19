# Docker Compose File for Sharded Cluster

These files deploy and set up a MongoDB sharded cluster using `docker compose`.

The cluster consists of the following components and offers the minimum 
configuration required for testing.
- Config Server (Primary Only)
- 2 Shards (Each Primary Only)
- 1 Mongos

```bash
# Run the deploy.sh script to deploy and set up a sharded cluster.
./scripts/deploy.sh

# Shut down the apps
docker compose -f docker-compose.yml down
```

The files we use are as follows:
- `docker-compose.yml`: This file is used to run Yorkie's integration tests with a
 MongoDB sharded cluster. It runs a MongoDB sharded cluster.
- `scripts/init-config.yml`: This file is used to set up a replica set of the config
 server.
- `scripts/init-shard1.yml`: This file is used to set up a replica set of the shard 1.
- `scripts/init-shard2.yml`: This file is used to set up a replica set of the shard 2.
- `scripts/init-mongos.yml`: This file is used to shard the `yorkie-meta` database and
 the collections of it.
- `scripts/deploy.sh`: This script runs a MongoDB sharded cluster and sets up the cluster
 step by step.
