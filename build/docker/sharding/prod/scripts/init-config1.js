rs.initiate(
    {
      _id: "config-rs",
      configsvr: true,
      members: [
        { _id : 0, host : "config1:27017" },
        { _id : 1, host : "config2:27017" },
        { _id : 2, host : "config3:27017" },
      ]
    }
  )
  