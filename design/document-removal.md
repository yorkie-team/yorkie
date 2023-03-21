---
title: document-removal
target-version: 0.3.2
---

# Document Removal

## Summary

Document removal is a crucial feature to save resources. Without document removal, accumulated documents and their relevant data will waste storage resources.

Yorkie implements this feature by dividing the document removal process into three steps:

1. Implement the basic document remove API by simply setting `RemovedAt` date in `docInfo`.
2. Determine whether to include removed documents in `ListDocuments` API used by Dashboard and CLI(Admin).
3. Implement a housekeeping background process to physically remove document(`is_removed` = `true`) and their relevant data.
   
Also, there are some considerations to be made:

- User should be able to remove documents with API to optimize their system.
- Document removal should propagate among peers.

This document explains _Step 1: Implementing the basic document removal API with soft removal_.

### Goals

Explains document removal mechanisms, especially soft removal.

### Non-Goals

We only discuss soft (logical) removal in this document. Hard (physical) removal using housekeeping will not be discussed in this document.

## Proposal Details

### State transition of Document

As we introduced document removal, a new state: `Removed` is added. Below is the new state transition diagram of the document.

```
 ┌──────────┐ Attach ┌──────────┐ Remove ┌─────────┐
 │ Detached ├───────►│ Attached ├───────►│ Removed │
 └──────────┘        └─┬─┬──────┘        └─────────┘
           ▲           │ │     ▲
           └───────────┘ └─────┘
              Detach     PushPull
```

This state diagram shows how document state transitions are made. This also tells what the document can do in a certain state. A `detached` document, for example, cannot be `removed` and its state changed to `removed.`

As you can see above, by introducing document removal, `Attached` document can now be changed to `Removed`. Keep in mind that only `Attached` document can perform `Remove` to change its state to `Removed`.

### How it Works?

The overall document removal process looks like this:

1. The client calls the remove document API(`RemoveDocument`) with `IsRemoved = true` in the request `ChangePack`
2. In `PushPull` of `RemoveDocument`(or other APIs), check `IsRemoved`. And if `IsRemoved = true`, simply set `RemovedAt` date in `docInfo` to logically (softly) remove the document.
3. Set response `ChangePack`'s `IsRemoved` to `true` to inform document removal.
4. After a certain period of time(`DocumentRemoveDuration`), the document is physically removed from the database.

The very first step of document removal is to perform soft removal of the document, which is shown as _Step 1_ ~ _Step_ 3 above.

**Step 1: The client calls the document remove API(`RemoveDocument`) with `IsRemoved = true` in the request `ChangePack`**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/client/client.go#L599-L638

The first step is to receive a client document removal request and validate the document's state.

As we have discussed earlier, only `Attached` document can perform `Remove` action and change the state to `Removed`. This is implemented in the first few lines of code above.

**Step 2: In `PushPull` of `RemoveDocument`(or other APIs), check `IsRemoved`. And if `IsRemoved = true`, simply set `RemovedAt` date in `docInfo` to logically (softly) remove the document.**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/packs/packs.go#L48-L100

The second step is to check the removing flag `IsRemoved` in `RemoveDocument` API's `PushPull()` method, and if `IsRemoved` is true, use `be.DB.CreateChangeInfos()` method to logically (softly) remove the document.

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/backend/database/mongo/client.go#L816-L889

In `be.DB.CreateChangeInfos()`, if `isRemoved` flag is `true`, `RemovedAt` date is updated to `now` in `docInfo`. This is how we perform soft removal of documents: just initialize the date in `RemovedAt` of `docInfo`. If `isRemoved` is `false`, `RemovedAt` date will not be set and stay as `not exists`.

By this mechanism, we can filter out softly (logically) removed documents by checking `RemovedAt` exists or not. And perform hard (physical) removing.

**Step 3: Set the ChangePack response `ChangePack`'s `IsRemoved` to `true` to inform document removal.**

https://github.com/yorkie-team/yorkie/blob/dcf4cfb6ff98173c6d02a0abed89cb847f8aaa1e/server/rpc/yorkie_server.go#L457-L538

After the soft (logical) removal of the document is done, we inform the client that the document is removed by setting `IsRemoved` to `true` in the response `ChangePack`.

Since we have set `docInfo`'s `RemovedAt` date, all other APIs' responses will also include `IsRemoved` as `true`. This is because all APIs use `PushPull()` method as a common method to perform document operations, and it will also set `IsRemoved` to `true` if `RemovedAt` date exists in `docInfo`.

This will ensure that if a document is removed, all other peers who currently use it will also know that the document is removed by the next API response's `IsRemoved` flag. This is how we ensure document removal propagation among peers.

### New Key - ID Mapping in Document

As we introduced document removal, `Key` - `ID` mapping in documents changed.

Before document removal was introduced, `Key` - `ID` mapping had **one to one (1:1)** relationship. But after document removal was introduced, `Key` - `ID` mapping now has **one to many (1:N)** relation. This is because we now reuse `Key` to create new documents, and there can be several documents with the same `Key`, but different `ID`.

Before document removal, `Key` - `ID` mapping has a **one to one (1:1)** relationship. But after document removal, the `Key` - `ID` mapping now has a **one to many (1:N)** relation. This is because we now reuse the `Key` to create new documents, and there can be several documents with the same `Key` but different `ID`.

Due to this, we need to use document `ID` in API requests instead of document `Key` to properly identify documents.
