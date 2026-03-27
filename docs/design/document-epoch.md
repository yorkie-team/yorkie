---
title: document-epoch
target-version: 0.7.3
---

# Document Epoch

## Summary

When an administrator force-compacts a document through the Dashboard, the
server resets its `serverSeq` to 1 and purges all prior changes and snapshots.
Clients that were connected before the compaction still hold checkpoints
referencing the old `serverSeq` (e.g., 17459). On the next PushPull these
clients receive an `ErrInvalidServerSeq` error and become permanently stuck —
they cannot sync and there is no way to distinguish a compaction-induced reset
from a genuine protocol violation.

This document proposes adding an **epoch** field to `DocInfo` and
`ClientDocInfo`. The epoch is a monotonically increasing integer that
increments on every compaction. By comparing epochs at the start of PushPull,
the server can detect stale clients and return a dedicated error that tells the
client to reattach.

### Goals

- Detect, during PushPull, whether a client's checkpoint belongs to a
  pre-compaction epoch.
- Return a dedicated `ErrEpochMismatch` error so clients can distinguish
  compaction resets from protocol violations.
- Allow clients to self-heal by performing an automatic detach → re-attach
  cycle upon receiving the error.
- Maintain backward compatibility with existing documents that have never been
  compacted.

### Non-Goals

- Fixing CRDT tree integrity issues that may occur during compaction itself
  (e.g., "node not found" errors).
- Resolving revision label collisions caused by `serverSeq` resets.
- Automatically recovering unsent local changes on the client side.

## Proposal Details

### What is an Epoch?

An epoch is a monotonically increasing integer that represents a document's
"generation." Every time a document is compacted, all of its changes and
snapshots are purged, `serverSeq` is reset to 1, and the epoch increments by 1.

Think of it like a book's edition number. Page 153 in the 1st edition
(epoch 0) and page 153 in the 2nd edition (epoch 1) contain entirely different
content. If a reader asks the bookstore for "everything after page 153 of the
1st edition," the bookstore must respond: "a new edition has been published —
please start over from the beginning."

The same applies here. A `serverSeq` is only meaningful within its epoch. Once
the epoch changes, all prior checkpoints are invalidated and clients must
reattach to receive the compacted document.

### Schema Changes

Add an `Epoch` field to both `DocInfo` and `ClientDocInfo`:

```go
type DocInfo struct {
    // ...existing fields...
    Epoch int64 `bson:"epoch"`
}

type ClientDocInfo struct {
    // ...existing fields...
    Epoch int64 `bson:"epoch"`
}
```

The default value of `Epoch` is 0, which means existing documents and clients
require no migration.

### Flow

**Attach:** When a client attaches to a document, the server records the
document's current epoch in the client's `ClientDocInfo.Epoch`.

```
Client → AttachDocument → Server
Server: ClientDocInfo.Epoch = DocInfo.Epoch
```

**PushPull (normal):** The server compares epochs before processing changes.
If they match, the existing logic runs unchanged.

```
Client(epoch=1) → PushPullChanges → Server(doc.epoch=1)
Server: client.Epoch == doc.Epoch → proceed as usual
```

**Compaction:** When the admin compacts a document, the server increments the
document's epoch alongside the `serverSeq` reset.

```
Admin → CompactDocumentByAdmin → Server
Server: doc.Epoch++ (1 → 2), doc.ServerSeq → 1
```

**PushPull (after compaction):** A client from the previous epoch attempts to
sync. The server detects the epoch mismatch and returns a dedicated error.

```
Client(epoch=1) → PushPullChanges → Server(doc.epoch=2)
Server: client.Epoch(1) != doc.Epoch(2) → return ErrEpochMismatch
Client: detach → re-attach (receives new epoch)
```

### Epoch Check Placement

The epoch comparison must happen **before** the existing `serverSeq`
validation in `preparePack`. Once epochs differ, comparing `serverSeq` values
is meaningless — they belong to different generations.

```go
func preparePack(...) {
    // Compare epochs first: if the document has been compacted since the
    // client last synced, serverSeq values from different epochs cannot be
    // compared.
    if clientEpoch != docEpoch {
        return ErrEpochMismatch
    }

    // Existing serverSeq validation.
    if initialServerSeq < reqPack.Checkpoint.ServerSeq {
        return ErrInvalidServerSeq
    }
    // ...
}
```

### Code Change Points

1. **`DocInfo`** (`server/backend/database/doc_info.go`): Add `Epoch` field.
2. **`ClientDocInfo`** (`server/backend/database/client_info.go`): Add `Epoch`
   field.
3. **`CompactChangeInfos`** (`server/backend/database/mongo/client.go`):
   Increment `doc.Epoch` alongside the `serverSeq` reset.
4. **`AttachDocument`**: Set `ClientDocInfo.Epoch = DocInfo.Epoch` on attach.
5. **`preparePack`** (`server/packs/pushpull.go`): Add epoch comparison before
   the `serverSeq` check.
6. **Error definition**: Add `ErrEpochMismatch` with an appropriate gRPC
   status code mapping.
7. **SDKs** (JS, iOS, Android): Handle `ErrEpochMismatch` by performing
   detach → re-attach.

### Backward Compatibility

- `Epoch` defaults to 0 for both `DocInfo` and `ClientDocInfo`.
- Documents that have never been compacted have `DocInfo.Epoch = 0`.
- Existing clients have `ClientDocInfo.Epoch = 0`.
- Since 0 == 0, the epoch check passes and existing behavior is unaffected.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Client has unsent local changes when it receives `ErrEpochMismatch`; reattach discards them | Expose `ErrEpochMismatch` as an SDK event so the application can notify the user before reattaching |
| Older SDK versions do not recognize `ErrEpochMismatch` | They fall through to the existing `ErrInvalidServerSeq` behavior — no worse than today |
| Epoch counter overflows | `int64` supports over 9 × 10¹⁸ compactions — practically unlimited |
