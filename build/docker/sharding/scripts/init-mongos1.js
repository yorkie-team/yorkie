sh.addShard("shard-rs-1/shard1-1:27017");
sh.addShard("shard-rs-2/shard2-1:27017");

function findAnotherShard(shard) {
  if (shard == "shard-rs-1") {
    return "shard-rs-2";
  }

  return "shard-rs-1";
}

function shardOfChunk(minKeyOfChunk) {
  return db
    .getSiblingDB("config")
    .chunks.findOne({ min: { project_id: minKeyOfChunk } }).shard;
}

// Sharded DB for the client test
const clientTestDB = "test-yorkie-meta-mongo-client";
sh.enableSharding(clientTestDB);
sh.shardCollection(clientTestDB + ".clients", { project_id: 1, _id: "hashed" });
sh.shardCollection(clientTestDB + ".documents", { project_id: 1 });
sh.shardCollection(clientTestDB + ".schemas", { project_id: 1 });
sh.shardCollection(clientTestDB + ".changes", { doc_id: "hashed" });
sh.shardCollection(clientTestDB + ".snapshots", { doc_id: "hashed" });
sh.shardCollection(clientTestDB + ".versionvectors", { doc_id: "hashed" });
sh.shardCollection(clientTestDB + ".revisions", { doc_id: "hashed" });

// Split the inital range at `splitPoint` to allow doc_ids duplicate in different shards.
const splitPoint = ObjectId("500000000000000000000000");
sh.splitAt(clientTestDB + ".documents", { project_id: splitPoint });
// Move the chunk to another shard.
db.adminCommand({
  moveChunk: clientTestDB + ".documents",
  find: { project_id: splitPoint },
  to: findAnotherShard(shardOfChunk(splitPoint)),
});

// Sharded DB for the server test
const serverTestDB = "test-yorkie-meta-server";
sh.enableSharding(serverTestDB);
sh.shardCollection(serverTestDB + ".clients", { project_id: 1, _id: "hashed" });
sh.shardCollection(serverTestDB + ".documents", { project_id: 1 });
sh.shardCollection(serverTestDB + ".schemas", { project_id: 1 });
sh.shardCollection(serverTestDB + ".changes", { doc_id: "hashed" });
sh.shardCollection(serverTestDB + ".snapshots", { doc_id: "hashed" });
sh.shardCollection(serverTestDB + ".versionvectors", { doc_id: "hashed" });
sh.shardCollection(serverTestDB + ".revisions", { doc_id: "hashed" });
