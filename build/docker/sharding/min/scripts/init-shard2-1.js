rs.initiate(
    {
      _id : "shard-rs-2",
      members: [
        { _id : 0, host : "shard2-1:27017" },
        { _id : 1, host : "shard2-2:27017" },
        { _id : 2, host : "shard2-3:27017" }
      ]
    }
  )