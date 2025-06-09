---
title: fine-grained-document-locking
target-version: 0.6.15
---

<!-- Make sure to append document link in design README.md after creating the document. -->

# Fine-Grained Document Locking

## Summary

This proposal introduces a fine-grained document locking mechanism to improve concurrency on a per-document basis. By splitting the traditional single document lock into multiple specialized locks (e.g., Attachment, Push, Pull, Compaction), we reduce unnecessary contention between operations and enhance overall system performance.

### Goals

- Improve concurrency for operations on a single document
- Reduce contention between unrelated operations such as Attachment, Push, Pull, and Compaction
- Allow compaction/purge operations to coexist with lightweight requests

### Non-Goals

- Does not include distributed or cluster-wide locking mechanisms
- Does not address client-side locking

## Proposal Details

### Lock Structure

| Lock Name        | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `doc`            | Coordinates consistency between PushPull-series requests(RLock) and compaction      |
| `doc.attachment` | Ensures consistency during Attach/Detach operations                                 |
| `doc.push`       | Ensures consistency while pushing changes to server (lock-free implementation)      |
| `doc.pull`       | Ensures consistency when clients have already pushed changes or pull unseen changes |

### Lock Acquisition by API

**AttachDocument(by SDK)**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient` / `findOrCreateDoc`
4. 🔒 Acquire `doc.attachment` (Optional, if the project has attachment limit)
5. Count attachments (if applicable)
6. PushPull
7. 🔓 Release `doc.attachment`, `doc.pull`, `doc`

**DetachDocument(by SDK)**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient`
4. 🔒 Acquire `doc.attachment` (Optional, if the project has attachment limit)
5. PushPull
6. 🔓 Release `doc.attachment`, `doc.pull`, `doc`

**PushPullChanges/RemoveDocument(by SDK)**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient`
4. PushPull
5. 🔓 Release `doc.pull`, `doc`

**CreateDocument/UpdateDocument(by Admin)**

1. 🔒 Acquire `doc` RLock
2. PushPull
3. 🔓 Release `doc`

**PurgeDocument/CompactDocument(by System)**

1. 🔒 Acquire `doc` exclusive Lock
2. `findDoc`
3. Purge or compact document
4. 🔓 Release `doc`

**PushPull(Internal Function)**

1. 🔒 Acquire `doc.push` (conceptually lock, but uses lock-free implementation for high concurrency)
2. Push changes and assign `ServerSeq`
3. 🔓 Release `doc.push`

```ts
// 01. Atomically Reserve ServerSeq
db.documents.findOneAndUpdate(
  { _id: "doc123" },
  { $inc: { server_seq: pushedChanges.length } },
  { returnDocument: "before" }
);

// 02. Push incoming changes using the reserved ServerSeq range
db.changes.insertMany(
  pushedChanges.map((change, index) => ({
    ...change,
    server_seq: initialServerSeq + index + 1,
  }))
);
```

> NOTE(hackerwins): If InsertMany operation fails, there will be a mismatch between the already incremented server_seq and the actual stored changes. This can lead to gaps in the sequence numbers, which can cause data consistency issues.
> We need to handle this case by either rolling back the increment or ensuring that the changes are always pushed successfully.

### Before vs. After Summary

| Legacy Structure                          | Fine-Grained Lock Structure       |
| ----------------------------------------- | --------------------------------- |
| Single `Document` lock for all operations | Separate locks per logical task   |
| Low concurrency, high contention          | Higher parallelism and throughput |
| Compaction blocks all operations          | Minimal interference via RWLock   |

### Future Considerations

- Use tracing/pprof to observe lock contention and further optimize if needed

## Risks and Mitigation

| Risk                              | Mitigation Strategy                                                                |
| --------------------------------- | ---------------------------------------------------------------------------------- |
| Increased code complexity         | Use standardized lock helpers and runtime assertions                               |
| Potential for deadlocks           | Enforce consistent lock acquisition order (`doc` → `pull` → `attachment` → `push`) |
| Difficulty implementing lock-free | Consider using rollback mechanisms or retry logic for failed operations            |
