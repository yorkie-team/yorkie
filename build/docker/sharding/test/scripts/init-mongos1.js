sh.addShard("shard-rs-1/shard1-1:27017")
sh.addShard("shard-rs-2/shard2-1:27017")

// The DB 'yorkie-meta-1' is for the mongo client test.
sh.enableSharding("yorkie-meta-1")
sh.shardCollection("yorkie-meta-1.users", { username: 1 }, true)
// sh.shardCollection("yorkie-meta-1.clients", { _id: 1 }, true)
sh.shardCollection("yorkie-meta-1.documents", { key: 1 })
sh.shardCollection("yorkie-meta-1.changes", { doc_key: 1 })
sh.shardCollection("yorkie-meta-1.snapshots", { doc_key: 1 })
sh.shardCollection("yorkie-meta-1.syncedseqs", { doc_key: 1 })
// Split the inital range at "duplicateIDTestDocKey5" to allow doc_ids duplicate in different shards.
sh.splitAt("yorkie-meta-1.documents", { key: "duplicateIDTestDocKey5" })
// Move the chunk to another shard.
const currentShard = db.getSiblingDB("config").chunks.findOne({ min: { key: 'duplicateIDTestDocKey5' } }).shard
var nextShard = ""
if (currentShard == "shard-rs-1") {
    nextShard = "shard-rs-2"
} else {
    nextShard = "shard-rs-1"
}
db.adminCommand({ moveChunk: "yorkie-meta-1.documents", find: { key: "duplicateIDTestDocKey5" }, to: nextShard })


// The DB 'yorkie-meta-2' is for the server test.
sh.enableSharding("yorkie-meta-2")
sh.shardCollection("yorkie-meta-2.users", { username: 1 }, true)
// sh.shardCollection("yorkie-meta-2.clients", { _id: 1 }, true)
sh.shardCollection("yorkie-meta-2.documents", { key: 1 })
sh.shardCollection("yorkie-meta-2.changes", { doc_key: 1 })
sh.shardCollection("yorkie-meta-2.snapshots", { doc_key: 1 })
sh.shardCollection("yorkie-meta-2.syncedseqs", { doc_key: 1 })
