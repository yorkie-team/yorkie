# Peer Awareness

Target version: Yorkie 0.1.2

## Use case

When multiple users edit a single document, the users want to know who is connected and what is being edited by the peers. For example, in Google Docs, the ID and profile of the user editing together are displayed in the upper-right.

## How to use

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

When a new peer registers or leaves, `peers-changed` event is fired, and the other peer's clientID and metadata can be obtained from the event.

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

## How does it work?

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

When a client attaches documents, a stream is connected between agent and the client through WatchDocuments API. This will update the map of clients that are watching the documents in PubSub. When the stream disconnects or a new connection is made, `DOCUMENTS_UNWATCHED` or `DOCUMENTS_WATCHED` event is delivered to other clients who are watching the document together.

Client Metadata is managed inside the instance of the Client and is not stored persistently in Yorkie. The reasons are as follows:

 - Metadata's primary "source of truth" location is user's DB, and it is simply passed to Yorkie.
 - All other locations of the metadata in Yorkie just refer back to the primary "source of truth" location.
 - We can prevent increasing management points caused by storing metadata in MongoDB.

