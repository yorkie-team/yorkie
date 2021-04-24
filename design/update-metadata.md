---
title: update-metadata
target-version: 0.2.0
---

# Update Metadata

## Summary
Yorkie manages the client's metadata inside the `Client` instance, and when a document is watched, the metadata is shared with other clients([related document](https://github.com/yorkie-team/yorkie/blob/main/design/peer-awareness.md)). 
This feature updates the metadata information of clients managed by the Client instance and notifies other clients.

### Goals
Update the client's metadata and share it with other clients. It supports synchronizing information such as nicknames of clients watching the document.

### Non-Goals

1. It only changes and shares information in metadata. It does not manage the history of metadata changes.
2. When one client is attached to multiple documents, all attached documents know the same metadata. When a change occurs, it modifies all metadata in the attached document.

## Proposal Details

### How does it work?
```
 +--Client "A"----+                             +--Agent------------------------------+
 | +--Metadata--+ |                             | +--PubSub ------------------------+ |
 | | { num: 3 } | |----- UpdateMetadata ----->  | | {                               | |
 | +------------+ |        { name: 3 }          | |   docA: {                       | |
 +----------------+                             | |     A: { num: 3 }, // <- update | |
                                                | |     B: { num: 2 }               | |
 +--Client "B"----+                             | |   },                            | |
 | +--Metadata--+ |                             | |   ...                           | |
 | | { num: 2 } | <-- ClientChangedEvent -----  | | }                               | |
 | +------------+ |                             | +---------------------------------+ |
 +----------------+                             +-------------------------------------+
```
When the client wants to update the metadata, it calls `UpdateMetadata`. 
At this time, if the client is in the `Active` state and the attached document exists, UpdateMetadata API is called. 
This is because if it is not active or there is no attached document, you only need to modify the memory data without requesting API.


When the UpdateMetadata API is called, the metadata managed by PubSub is updated. And the changed client information is shared through the `CLIENT_CHANGED` event to other clients viewing the same document.

### Risks and Mitigation

If you need to manage metadata change history, implement it yourself. 
As of yet, Yorkie doesn't store metadata anywhere other than memory. This is also the case with the change history.  
The reasons are as follows:

- Metadata's primary "source of truth" location is user's DB, and it is simply passed to Yorkie.
- All other locations of the metadata in Yorkie just refer back to the primary "source of truth" location.
- We can prevent the increase of the management point of the database that will occur while managing the metadata change history.


And if you want to manage the metadata for each document, please give us a comment. 
We are managing metadata in a single client, and metadata is being propagated to attached documents. 
The same is true when updating metadata. If only some of several documents are updated with metadata, the metadata of the Client instance and the metadata known to the document may be different.
