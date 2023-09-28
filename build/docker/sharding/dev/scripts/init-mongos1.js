sh.addShard("shard-rs-1/shard1-1:27017,shard1-2:27017,shard1-3:27017")
sh.addShard("shard-rs-2/shard2-1:27017,shard2-2:27017,shard2-3:27017")

sh.enableSharding("yorkie-meta")
sh.shardCollection("yorkie-meta.projects", { _id: "hashed" })
sh.shardCollection("yorkie-meta.users", { username: "hashed" })
sh.shardCollection("yorkie-meta.clients", { project_id: "hashed" })
sh.shardCollection("yorkie-meta.documents", { project_id: "hashed" })
sh.shardCollection("yorkie-meta.changes", { doc_id: "hashed", server_seq: 1 })
sh.shardCollection("yorkie-meta.snapshots", { doc_id: "hashed" })
sh.shardCollection("yorkie-meta.syncedseqs", { doc_id: "hashed" })

// proxy collections
sh.shardCollection("yorkie-meta.proxy_project_owners_names", { owner: 1, name: 1 }, true)
sh.shardCollection("yorkie-meta.proxy_project_publickeys", { public_key: 1 }, true)
sh.shardCollection("yorkie-meta.proxy_project_secretkeys", { secret_key: 1 }, true)
sh.shardCollection("yorkie-meta.proxy_user_usernames", { username: 1 }, true)