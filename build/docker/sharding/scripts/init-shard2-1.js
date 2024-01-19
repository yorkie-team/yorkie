rs.initiate(
    {
        _id: "shard-rs-2",
        members: [
            { _id: 0, host: "shard2-1:27017" },
        ]
    }
)
