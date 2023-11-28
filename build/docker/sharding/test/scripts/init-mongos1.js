sh.addShard("shard-rs-1/shard1-1:27017")
sh.addShard("shard-rs-2/shard2-1:27017")

// The DB 'yorkie-meta-1' is for the mongo client test.
sh.enableSharding("yorkie-meta-1")
sh.shardCollection("yorkie-meta-1.users", { username: 1 }, true)
sh.shardCollection("yorkie-meta-1.clients", { key: 1 })
sh.shardCollection("yorkie-meta-1.documents", { key: 1 })
sh.shardCollection("yorkie-meta-1.changes", { doc_key: 1 })
sh.shardCollection("yorkie-meta-1.snapshots", { doc_key: 1 })
sh.shardCollection("yorkie-meta-1.syncedseqs", { doc_key: 1 })

const docSplitKey = "duplicateIDTestDocKey5"
const clientSplitKey = "duplicateIDTestClientKey5"

// Split the inital range at "duplicateIDTestDocKey5" to allow doc_ids duplicate in different shards.
sh.splitAt("yorkie-meta-1.documents", { key: docSplitKey })
// Move the chunk to another shard.
const currentDocShard = db.getSiblingDB("config").chunks.findOne({ min: { key: docSplitKey } }).shard
var nextDocShard = ""
if (currentDocShard == "shard-rs-1") {
    nextDocShard = "shard-rs-2"
} else {
    nextDocShard = "shard-rs-1"
}
db.adminCommand({ moveChunk: "yorkie-meta-1.documents", find: { key: docSplitKey }, to: nextDocShard })

// Split the inital range at "duplicateIDTestClientKey5" to allow client_ids duplicate in different shards.
sh.splitAt("yorkie-meta-1.clients", { key: clientSplitKey })
// Move the chunk to another shard.
const currentClientShard = db.getSiblingDB("config").chunks.findOne({ min: { key: clientSplitKey } }).shard
var nextClientShard = ""
if (currentClientShard == "shard-rs-1") {
    nextClientShard = "shard-rs-2"
} else {
    nextClientShard = "shard-rs-1"
}
db.adminCommand({ moveChunk: "yorkie-meta-1.clients", find: { key: clientSplitKey }, to: nextClientShard })


// The DB 'yorkie-meta-2' is for the server test.
sh.enableSharding("yorkie-meta-2")
sh.shardCollection("yorkie-meta-2.users", { username: 1 }, true)
sh.shardCollection("yorkie-meta-2.clients", { key: 1 })
sh.shardCollection("yorkie-meta-2.documents", { key: 1 })
sh.shardCollection("yorkie-meta-2.changes", { doc_key: 1 })
sh.shardCollection("yorkie-meta-2.snapshots", { doc_key: 1 })
sh.shardCollection("yorkie-meta-2.syncedseqs", { doc_key: 1 })
