rs.initiate(
    {
        _id: "shard-rs-1",
        members: [
            { _id: 0, host: "shard1-1:27017" },
        ]
    }
)
