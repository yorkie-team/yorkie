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
    return db.getSiblingDB("config").chunks.findOne({ min: { key: minKeyOfChunk } }).shard
}

// Shard the database for the mongo client test
const mongoClientDB = "test-yorkie-meta-mongo-client"
sh.enableSharding(mongoClientDB)
sh.shardCollection(mongoClientDB + ".documents", { key: 1 })
sh.shardCollection(mongoClientDB + ".changes", { doc_key: 1 })
sh.shardCollection(mongoClientDB + ".snapshots", { doc_key: 1 })
sh.shardCollection(mongoClientDB + ".syncedseqs", { doc_key: 1 })

// Split the inital range at "duplicateIDTestDocKey5" to allow doc_ids duplicate in different shards.
const docSplitKey = "duplicateIDTestDocKey5"
sh.splitAt(mongoClientDB + ".documents", { key: docSplitKey })
// Move the chunk to another shard.
db.adminCommand({ moveChunk: mongoClientDB + ".documents", find: { key: docSplitKey }, to: findAnotherShard(shardOfChunk(docSplitKey)) })

// Shard the database for the server test
const serverDB = "test-yorkie-meta-server"
sh.enableSharding(serverDB)
sh.shardCollection(serverDB + ".documents", { key: 1 })
sh.shardCollection(serverDB + ".changes", { doc_key: 1 })
sh.shardCollection(serverDB + ".snapshots", { doc_key: 1 })
sh.shardCollection(serverDB + ".syncedseqs", { doc_key: 1 })
