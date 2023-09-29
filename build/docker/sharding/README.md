## Components
A MongoDB sharded cluster consists of the following components.
1. shard: Each shard contains a subset of the sharded data.
2. mongos: The mongos acts as a query router, providing an interface between client applications and the sharded cluster.
3. config servers: Config servers store metadata and configuration settings for the cluster.

For a production deployment, consider the following to ensure data redundancy and system availability.
* Config Server (3 member replica set): `config1`,`config2`,`config3`
* 3 Shards (each a 3 member replica set):
	* `shard1-1`,`shard1-2`, `shard1-3`
	* `shard2-1`,`shard2-2`, `shard2-3`
	* `shard3-1`,`shard3-2`, `shard3-3`
* 2 Mongos: `mongos1`, `mongos2`

## How to deploy
Go to the `prod` or `dev` directory and execute the following command.
It takes 1~2 minutes to completely deploy a cluster.
```bash
docker-compose up -d
```

## How to examine
You can examine the configuration and status using the following commands.
### Config server
```bash
docker-compose exec config1 mongosh --port 27017
rs.status()
```

### Shards
```bash
docker-compose exec shard1-1 mongosh --port 27017
rs.status()
```

### Mongos
```bash
docker-compose exec mongos1 mongosh --port 27017
sh.status()
```

## How to connect
You can use the sharded cluster by adding `mongo-connection-uri` option in Yorkie.
Note that the Yorkie should be on the same network as the cluster.
For the production,
```
--mongo-connection-uri "mongodb://localhost:27020,localhost:27021"
```
For the development,
```
--mongo-connection-uri "mongodb://localhost:27020"
```


## Details
As mongos determines which shard a document is located and routes queries, it's necessary to configure mongos to identify shards and sharding rules.
Therefore, the following commands should be applied to the primary mongos.
* `sh.addShard()` method adds each shard to the cluster.
* `sh.shardCollections()` method shards each collection with the specified shard key and strategy.

Two strategies are available for sharding collections.
1. Hashed sharding: It uses a hashed index of a single field as the shard key to partition data across your sharded cluster.
2. Range-based sharding: It can use multiple fields as the shard key and divides data into contiguous ranges determined by the shard key values.

```javascript
sh.addShard("shard-rs-1/shard1-1:27017,shard1-2:27017,shard1-3:27017")
sh.addShard("shard-rs-2/shard2-1:27017,shard2-2:27017,shard2-3:27017")
sh.addShard("shard-rs-3/shard3-1:27017,shard3-2:27017,shard3-3:27017")

sh.enableSharding("yorkie-meta")
sh.shardCollection("yorkie-meta.projects", { _id: "hashed" })
sh.shardCollection("yorkie-meta.users", { username: "hashed" })
sh.shardCollection("yorkie-meta.clients", { project_id: "hashed" })
sh.shardCollection("yorkie-meta.documents", { project_id: "hashed" })
sh.shardCollection("yorkie-meta.changes", { doc_id: "hashed", server_seq: 1 })
sh.shardCollection("yorkie-meta.snapshots", { doc_id: "hashed" })
sh.shardCollection("yorkie-meta.syncedseqs", { doc_id: "hashed" })
```

Considering the common query patterns used in Yorkie, it's possible to employ Range-based sharding using the following command.
```javascript
sh.shardCollection("<database>.<collection>", { <shard key field> : 1, ... } )
```

## Considerations


## References
* https://www.mongodb.com/docs/v6.0/tutorial/deploy-shard-cluster/
* https://www.mongodb.com/docs/manual/core/sharded-cluster-components/
