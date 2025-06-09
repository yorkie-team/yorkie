---
title: fine-grained-document-lock
target-version: 0.6.15
---

<!-- Make sure to append document link in design README.md after creating the document. -->

# Fine-Grained Document Lock

## Summary

This proposal introduces a fine-grained document locking mechanism to improve concurrency on a per-document basis. By splitting the traditional single document lock into multiple specialized locks (e.g., Attachment, Push, Pull, Compaction), we reduce unnecessary contention between operations and enhance overall system performance.

### Goals

- Improve concurrency for operations on a single document
- Reduce contention between unrelated operations such as Attachment, Push, Pull, and Compaction
- Allow compaction/purge operations to coexist with lightweight requests

### Non-Goals

- Does not include distributed or cluster-wide locking mechanisms
- Does not involve changes to GC policies or global document management
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

**AttachDocument**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient` / `findOrCreateDoc`
4. 🔒 Acquire `doc.attachment`
5. Count attachments
6. PushPull
7. 🔓 Release `doc.attachment`, `doc.pull`, `doc`

**DetachDocument**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient`
4. 🔒 Acquire `doc.attachment`
5. Count attachments
6. PushPull
7. 🔓 Release `doc.attachment`, `doc.pull`, `doc`

**PushPullChanges**

1. 🔒 Acquire `doc` RLock
2. 🔒 Acquire `doc.pull`
3. `findClient`
4. PushPull
5. 🔓 Release `doc.pull`, `doc`

**PurgeDocument / CompactDocument**

1. 🔒 Acquire `doc` exclusive Lock
2. `findDoc`
3. Purge or compact document
4. 🔓 Release `doc`

**PushPull(Internal Function)**

1. 🔒 Acquire `doc.push`
2. Push changes and assign `ServerSeq`
3. 🔓 Release `doc.push`

### Before vs. After Summary

| Legacy Structure                          | Fine-Grained Lock Structure       |
| ----------------------------------------- | --------------------------------- |
| Single `Document` lock for all operations | Separate locks per logical task   |
| Low concurrency, high contention          | Higher parallelism and throughput |
| Compaction blocks all operations          | Minimal interference via RLock    |

### Future Considerations

- Use tracing/pprof to observe lock contention and further optimize if needed

## Risks and Mitigation

| Risk                              | Mitigation Strategy                                                                |
| --------------------------------- | ---------------------------------------------------------------------------------- |
| Increased code complexity         | Use standardized lock helpers and runtime assertions                               |
| Potential for deadlocks           | Enforce consistent lock acquisition order (`doc` → `pull` → `attachment` → `push`) |
| Difficulty implementing lock-free | Retain mutex-based implementation as fallback, phase in lock-free safely           |
