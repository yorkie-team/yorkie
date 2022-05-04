---
title: cluster-mode
target-version: 0.1.6
---

# Cluster Mode

## Summary

In a production environment, it is generally expected more than one agent to
handle client's requests. Even if an agent goes down, the other agents must be
able to handle the request. To support this scenario, agents must be able to
increase or decrease easily in the cluster. We will provide a Cluster Mode in
which multiple agents can run by moving agent's local state to a remote
location.

### Goals

Provide cluster mode when the user sets the etcd configuration. If the user does
not set etcd configuration, agent runs in the standalone mode as before.

### Non-Goals

In the cluster, the load balancer is not provided by Yorkie directly, we only
provide a guide for users to configure themselves for their own environment.

## Proposal details

### How to use

When users set etcd configuration in `config.yml`, internal etcd client is
created to store agent's states at the remote location when the agent starts.

```yaml
RPC:
  Port: 11101,
  CertFile: "",
  KeyFile: ""

  // ... other configurations...
ETCD:
  Endpoints: [
      "localhost:2379"
  ]
```

### How does it work?

An example when a user runs two Agents in cluster mode with etcd and introduces
load balancer is as follows:

![cluster-mode-example](media/cluster-mode-example.png)

- Load Balancer: It Receives requests from clients and forwards it to one of
  Agents.
- Broadcast Channel: It is used to deliver `DocEvent` to other Agents when it
  occurs by a Client connected to the Agent.
- etcd: etcd is responsible for three tasks. it is used to maintain `MemberMaps`
  , a map of agents in the cluster. It is used to create cluster-wide locks and
  to store `SubscriptionMaps`.

The agent's states to be moved to etcd are as follows:

- `LockerMap`: A map of locker used to maintain consistency of metadata of the
  documents such as checkpoints when modifying documents.
- `SubscriptionMap`: A map of subscription used to deliver events to other peers
  when an event such as a document change occurs.

The interfaces of `LockerMap` and `SubscriptionMap` are defined in the
`backend/sync` package and there are memory-based implementations in
`backend/sync/memory` and etcd-based implementations in `backend/sync/etcd`.

#### MemberMap

Agents have replicas of `MemberMap` within the cluster in an optimistic
replication manner. `MemberMap` is used by an Agent to broadcast DocEvents to
other Agents.

![member-map](media/member-map.png)

When an Agent started, background routines used to maintain
`MemberMap` are executed. These routines save the current Agent information by
periodically with the TTL in etcd and update the local MemberMap with any
changes to the `/agents` in etcd.

#### LockerMap based on etcd

The etcd-based implementation of `LockerMap` uses the lock and unlock
of [etcd concurrency API](https://etcd.io/docs/v3.4.0/dev-guide/api_concurrency_reference_v3/)
. The concurrency API provides lease time to avoid the problem that crashed
agent holding a lock forever and never releasing it.

Because many factors(
e.g. [stop-the-world GC pause of a runtime](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html))
can cause false expiration of a granted lease, `serverSeq` is used as a fencing
token.

![fencing tokens][fencing-tokens]

```go
// backend/database/mongo/client.go > StoreChangeInfos
res, err := c.collection(ColDocuments, opts).UpdateOne(ctx, bson.M{
"_id":        encodedDocID,
"server_seq": initialServerSeq,
}, bson.M{
"$set": bson.M{
"server_seq": docInfo.ServerSeq,
"updated_at": now,
},
})
if res.MatchedCount == 0 {
return nil, fmt.Errorf("%s: %w", docInfo.ID, db.ErrConflictOnUpdate)
}
```

[fencing-tokens]: https://martin.kleppmann.com/2016/02/fencing-tokens.png
