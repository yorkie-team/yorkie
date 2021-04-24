---
title: pub-sub
target-version: 0.1.0
---

# PubSub

## Summary
Yorkie needs to share events happening in documents with different clients. For this, we implemented this feature using gRPC server-side stream and the PubSub pattern.

### Goals
We should be able to share events with other clients who are subscribing to Documents.

## Proposal Details

### How does it work?
Yorkie implements WatchDocuments API using [gRPC server-side streaming](https://grpc.io/docs/languages/go/basics/#server-side-streaming-rpc) to deliver the events that have occurred to other clients.

```protobuf
// api/yorkie.proto

service Yorkie {
    ...
    rpc WatchDocuments (WatchDocumentsRequest) returns (stream WatchDocumentsResponse) {}
}
```

And to manage the event delivery target, we are using the [PubSub pattern](https://en.wikipedia.org/wiki/Publish%E2%80%93subscribe_pattern). 
You can learn more by looking at the [sync package](https://github.com/yorkie-team/yorkie/tree/main/yorkie/backend/sync) we are implementing.
 
The order of operation is as follows.

1. The WatchDocuments API creates a `Subscription` instance with the client and document keys.
2. Subscription instance internally manages the `DocEvent channel`, and the WatchDocuments API uses a select statement to retrieve events passed to the Subscription instance.
3. The client can deliver the event to the Subscription instances that are subscribed to the same document through the `PubSub.Publish(publisherID *time.ActorID, topic string, event DocEvent)` method.
4. In the select statement mentioned earlier, when an event is confirmed, it is sent to the stream server.

### Risks and Mitigation
Currently, Subscription instances are managed in memory. This can be a problem when building a cluster of servers.  
To solve this problem, we are planning to support cluster-mode using [etcd](https://github.com/etcd-io/etcd).