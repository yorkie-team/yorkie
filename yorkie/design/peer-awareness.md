---
title: peer-awareness
target-version: 0.1.2
---

# Peer Awareness

## Summary

We will provide Peer Awareness which is a simple algorithm that manages end-user
status like who is connected and metadata like username or email address and etc.
For example, users can implement a list of people participating in the editing,
such as a box in the top right of Google Docs.

### Goals

Implement Peer Awareness and provide API to users to use the feature. The goal of
the first version is to implement simple functionality and check usability.

### Non-Goals

The first version does not implement complex features such as dynamic metadata
updates.

## Proposal details

### How to use

Users can pass metadata along with client options when creating a client.

```typescript
const client = yorkie.createClient('https://yorkie.dev/api', {
  metadata: {
    username: 'hackerwins'
  }
});
```

Then the users create a document in the usual way and attach it to the client.

```typescript
const doc = yorkie.createDocument('examples', 'codemirror');
await client.attach(doc);
```

When a new peer registers or leaves, `peers-changed` event is fired, and the
other peer's clientID and metadata can be obtained from the event.

```typescript
client.subscribe((event) => {
  if (event.name === 'peers-changed') {
    const peers = event.value[doc.getKey().toIDString()];
    for (const [clientID, metadata] of Object.entries(peers)) {
      console.log(clientID, metadata);
    }
  }
});
```

### How does it work?

```
 +--Client "A"----+                      +--Agent-----------------+
 | +--Metadata--+ |                      | +--PubSub -----------+ |
 | | { num: 1 } | <-- WatchDocuments --> | | {                  | |
 | +------------+ |                      | |   docA: {          | |
 +----------------+                      | |     A: { num: 1 }, | |
                                         | |     B: { num: 2 }  | |
 +--Client "B"----+                      | |   },               | |
 | +--Metadata--+ |                      | |   ...              | |
 | | { num: 2 } | <-- WatchDocuments --> | | }                  | |
 | +------------+ |                      | +--------------------+ |
 +----------------+                      +------------------------+
```

When a client attaches documents, a stream is connected between agent and
the client through WatchDocuments API. This will update the map of clients that
are watching the documents in PubSub. When the stream disconnects or a new connection
is made, `DOCUMENTS_UNWATCHED` or `DOCUMENTS_WATCHED` event is delivered to other clients
who are watching the document together.

### Risks and Mitigation

The first version is missing the ability to dynamically update metadata and
propagate it to other peers. Client Metadata is managed inside the instance of the Client
and is not stored persistently in Yorkie. The reasons are as follows:

 - The goal of the first version is to check the usability of the feature.
 - Metadata's primary "source of truth" location is user's DB, and it is simply passed to Yorkie.
 - All other locations of the metadata in Yorkie just refer back to the primary "source of truth" location.
 - We can prevent increasing management points caused by storing metadata in MongoDB.

In the future, if the users needs arise, we may need to implement the ability to
dynamically update metadata and propagates it to peers. We might consider
treating it as a Yorkie Document that has logical clocks, not a normal map in PubSub.
