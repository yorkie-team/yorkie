rs.initiate(
    {
        _id: "config-rs",
        configsvr: true,
        members: [
            { _id: 0, host: "config1:27017" },
        ]
    }
)
