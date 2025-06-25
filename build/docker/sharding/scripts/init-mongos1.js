sh.addShard("shard-rs-1/shard1-1:27017");
sh.addShard("shard-rs-2/shard2-1:27017");

// Shard the database for the mongo client test
const mongoClientDB = "test-yorkie-meta-mongo-client";
sh.enableSharding(mongoClientDB);
sh.shardCollection(mongoClientDB + ".clients", { key: "hashed" });
sh.shardCollection(mongoClientDB + ".documents", { key: "hashed" });
sh.shardCollection(mongoClientDB + ".changes", { doc_id: "hashed" });
sh.shardCollection(mongoClientDB + ".snapshots", { doc_id: "hashed" });
sh.shardCollection(mongoClientDB + ".versionvectors", { doc_id: "hashed" });

// Shard the database for the server test
const serverDB = "test-yorkie-meta-server";
sh.enableSharding(serverDB);
sh.shardCollection(serverDB + ".clients", { key: "hashed" });
sh.shardCollection(serverDB + ".documents", { key: "hashed" });
sh.shardCollection(serverDB + ".changes", { doc_id: "hashed" });
sh.shardCollection(serverDB + ".snapshots", { doc_id: "hashed" });
sh.shardCollection(serverDB + ".versionvectors", { doc_id: "hashed" });
