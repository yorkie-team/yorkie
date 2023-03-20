---
title: document-deletion
target-version: 0.3.2
---

# Document Deletion

## Summary

Document deletion is crucial feature to save resources. Without deletion, accumulated documents and its relevant data will waste storage resources.

Yorkie implements this feature by dividing document deletion process into three steps:

1. Implement basic document deletion API with simply setting `RemovedAt` date in `docInfo`.
2. Determine whether to include removed documents in `ListDocuments` API used by Dashboard and CLI(Admin).
3. Implement housekeeping background process to physically remove document(`is_removed` = `true`) and its relevant data.
   
Also, there are some considersations to be made:

- User should be able to remove document with API to optimize their system.
- Document deletion should be propogated throughout peers.

This document explains _Step 1: Implementing basic document deletion API with soft deletion_.

### Goals

Explains document deletion mechanism, especially soft deletion.

### Non-Goals

We only discuss soft(logical) deletion in this document. Hard(physical) deletion using housekeeping will not be discussed in this document.

## Proposal Details

### State transition of Document

As we introduced document deletion, new state: `Removed` is added. Below is new state transition diagram of document.

```
 ┌──────────┐ Attach ┌──────────┐ Remove ┌─────────┐
 │ Detached ├───────►│ Attached ├───────►│ Removed │
 └──────────┘        └─┬─┬──────┘        └─────────┘
           ▲           │ │     ▲
           └───────────┘ └─────┘
              Detach     PushPull
```

This state diagram shows how document state transition is made. This also tells what document can perform on certain state. For example, `Detached` document cannot perform `Remove` and change state to `Removed`.

As you can see above, by introducing document deletion, `Attached` document can now be moved to `Removed`. Keep in mind that only `Attached` document can perform `Remove` to change state to `Removed`.

### How it Works?

The overall document deletion process looks like this:

1. Client sends document delete API(`RemoveDocument`) with  `DocIsRemoved = true` in request `ChangePack`
2. In `PushPull` of `RemoveDocument`(or other APIs), check `DocIsRemoved`. And if `DocIsRemoved = true`, simply set `RemovedAt` date in `docInfo` to logically(soft) remove document.
3. Set response `ChangePack`'s `DocIsRemoved` to `true` to inform document deletion.
4. After certain period of time(`DocumentRemoveDuration`), document is physically removed from database.

The very first step of document deletion is to perform soft deletion of the document, which is shown as _Step 1_ ~ _Step_ 3 in above.

**Step 1: Client sends document delete API(`RemoveDocument`) with  `DocIsRemoved = true` in request `ChangePack`**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/client/client.go#L599-L638

The first step is to receive client document delete request and validate document state.

As we have discussed earlier, only `Attached` document can perform `Remove` action and change state to `Removed`. This is implemented in first few lines of codes in above.

**Step 2: In `PushPull` of `RemoveDocument`(or other APIs), check `DocIsRemoved`. And if `DocIsRemoved = true`, simply set `RemovedAt` date in `docInfo` to logically(soft) remove document.**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/packs/packs.go#L48-L100

The second step is to check deletion flag `DocIsRemoved` in `RemoveDocument` API's `PushPull()` method, and if `DocIsRemoved` is true, use `be.DB.CreateChangeInfos()` method to logically(soft) remove document.

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/backend/database/mongo/client.go#L816-L889

In `be.DB.CreateChangeInfos()`, if `isRemoved` flag is `true`,  `RemovedAt` date is updated to `now` in `docInfo`. This is how we perform soft deletion of document: just initialize date in `RemovedAt` of `docInfo`. If `isRemoved` is `false`, `RemovedAt` date will not be set and stay as `not exists`.

By this mechanism, we can filter out soft(logical) removed document by checking `RemovedAt` exists or not. And perform hard(physical) deletion.

**Step 3: Set response `ChangePack`'s `DocIsRemoved` to `true` to inform document deletion.**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/rpc/yorkie_server.go#L457-L538

After the soft(logical) deletion of document is done, we inform client that document is removed by setting `DocIsRemoved` to `true` in response `ChangePack`.

Since we have set `docInfo`'s `RemovedAt` date, all other APIs' response will also include `DocIsRemoved` as `true`. This is because all APIs' use `PushPull()` method as a common method to perform document operation, and it will also set `DocIsRemoved` to `true` if `RemovedAt` date exists in `docInfo`.

This will ensure that if a document is removed, all other peers who currently use it will also know that the document is removed by the next API response's `DocIsRemoved` flag. This is how we ensure document deletion propagation among peers.

### New Key - ID Mapping in Document

As we introduced document deletion, `Key` - `ID` mapping in document has changed.

Before document deletion, `Key` - `ID` mapping has **one to one(1:1)** relation. But after document deletion, `Key` - `ID` mapping now has **one to many(1:N)** relation. This is because since we now reuse `Key` to create new document, and there can be several documents with same `Key`, but different `ID`.

Before document deletion, `Key` - `ID` mapping has a **one to one(1:1)** relationship. But after document deletion, the `Key` - `ID` mapping now has a **one to many(1:N)** relation. This is because we now reuse the `Key` to create new documents, and there can be several documents with the same `Key` but different `ID`.

Due to this, we need to use document `ID` in API requests instead of document `Key` to properly identify documents.
