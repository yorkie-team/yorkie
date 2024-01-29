sh.addShard("shard-rs-1/shard1-1:27017")
sh.addShard("shard-rs-2/shard2-1:27017")

function findAnotherShard(shard) {
    if (shard == "shard-rs-1") {
        return "shard-rs-2"
    } else {
        return "shard-rs-1"
    }
}

function shardOfChunk(minKeyOfChunk) {
    return db.getSiblingDB("config").chunks.findOne({ min: { project_id: minKeyOfChunk } }).shard
}

// Shard the database for the mongo client test
const mongoClientDB = "test-yorkie-meta-mongo-client"
sh.enableSharding(mongoClientDB)
sh.shardCollection(mongoClientDB + ".clients", { project_id: 1 })
sh.shardCollection(mongoClientDB + ".documents", { project_id: 1 })
sh.shardCollection(mongoClientDB + ".changes", { doc_id: 1 })
sh.shardCollection(mongoClientDB + ".snapshots", { doc_id: 1 })
sh.shardCollection(mongoClientDB + ".syncedseqs", { doc_id: 1 })

// Split the inital range at `splitPoint` to allow doc_ids duplicate in different shards.
const splitPoint = ObjectId("500000000000000000000000")
sh.splitAt(mongoClientDB + ".documents", { project_id: splitPoint })
// Move the chunk to another shard.
db.adminCommand({ moveChunk: mongoClientDB + ".documents", find: { project_id: splitPoint }, to: findAnotherShard(shardOfChunk(splitPoint)) })

// Shard the database for the server test
const serverDB = "test-yorkie-meta-server"
sh.enableSharding(serverDB)
sh.shardCollection(serverDB + ".clients", { project_id: 1 })
sh.shardCollection(serverDB + ".documents", { project_id: 1 })
sh.shardCollection(serverDB + ".changes", { doc_id: 1 })
sh.shardCollection(serverDB + ".snapshots", { doc_id: 1 })
sh.shardCollection(serverDB + ".syncedseqs", { doc_id: 1 })

