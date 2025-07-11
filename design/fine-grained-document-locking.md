---
title: fine-grained-document-locking
target-version: 0.6.16
---

<!-- Make sure to append document link in design README.md after creating the document. -->

# Fine-Grained Document Locking

## Summary

This proposal introduces a fine-grained document locking mechanism to improve concurrency on a per-document basis. By splitting the traditional single document lock into multiple specialized locks (e.g., Attachment, Push, Pull, Compaction), we reduce unnecessary contention between operations and enhance overall system performance.

## How to Read This Document

### For Implementers

1. Start with [Key Concepts](#key-concepts) to understand core terminology
2. Review [Lock Structure](#lock-structure) to understand the locking hierarchy
3. Study [Lock Acquisition Patterns](#lock-acquisition-patterns) for implementation details
4. Check [Risks and Mitigation](#risks-and-mitigation) for implementation considerations

### For Reviewers

1. Focus on [Goals](#goals) and [Design Rationale](#design-rationale)
2. Examine [Before vs. After Summary](#before-vs-after-summary) for impact assessment
3. Review [Lock Acquisition Patterns](#lock-acquisition-patterns) for correctness

## Key Concepts

| Term           | Definition                                                                          |
| -------------- | ----------------------------------------------------------------------------------- |
| `server_seq`   | Server-side sequence number tracking the order of changes applied to a document     |
| `client_seq`   | Client-side sequence number tracking the order of changes made by a specific client |
| `checkpoint`   | A pair of (server_seq, client_seq) representing a client's synchronization state    |
| `findClient`   | Maps to `clients.FindActiveClientInfo()` - retrieves active client information      |
| `findDoc`      | Document lookup operation to fetch document metadata and current server_seq         |
| `updateClient` | Maps to `UpdateClientInfoAfterPushPull()` - updates client's checkpoint after sync  |

## Design Rationale

### Current Problem

- Single document lock creates high contention between unrelated operations
- Compaction blocks all document operations, reducing system availability
- Low concurrency limits system scalability

### Solution Benefits

- Specialized locks allow concurrent operations on different aspects of the same document
- Read-write locks enable compaction to coexist with read-only operations
- Reduced lock contention improves overall system throughput

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
| `doc.push`       | Ensures consistency while pushing changes to server                                 |
| `doc.pull`       | Ensures consistency when clients have already pushed changes or pull unseen changes |

### Lock Acquisition Patterns

The following sections detail lock acquisition patterns for different types of operations. All operations follow the consistent lock order: `doc` → `doc.pull` → `doc.attachment` → `doc.push` to prevent deadlocks.

#### SDK Operations

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

#### Administrative Operations

**CreateDocument/UpdateDocument(by Admin)**

1. 🔒 Acquire `doc` RLock
2. PushPull
3. 🔓 Release `doc`

#### System Operations

**PurgeDocument/CompactDocument(by System)**

1. 🔒 Acquire `doc` exclusive Lock
2. `findDoc`
3. Purge or compact document
4. 🔓 Release `doc`

#### Internal Functions

**PushPull(Internal Function)**

_Push Phase (Steps 1-5):_

1. 🔒 Acquire `doc.push`
2. `findDoc` to fetch `server_seq`
3. Push `changes` and assign `server_seq`
4. `updateDoc` with `server_seq`
5. 🔓 Release `doc.push`

_Pull Phase (Steps 6-7):_

6. Pull unseen `changes`
7. `updateClient` with the latest `server_seq`

### Implementation Impact

| Aspect              | Legacy Structure                          | Fine-Grained Lock Structure             |
| ------------------- | ----------------------------------------- | --------------------------------------- |
| Lock granularity    | Single `Document` lock for all operations | Separate locks per logical task         |
| Concurrency level   | Low concurrency, high contention          | Higher parallelism and throughput       |
| Compaction impact   | Compaction blocks all operations          | Minimal interference via RWLock         |
| Operation isolation | All operations serialized                 | Related operations can run concurrently |
| Deadlock risk       | Lower (single lock)                       | Managed via consistent lock ordering    |

### Performance Considerations

- **Concurrent Reads**: Multiple clients can simultaneously read document state during compaction
- **Attachment Operations**: Can run concurrently with push/pull operations on different documents
- **Push/Pull Separation**: Push and pull phases can be optimized independently
- **Lock Contention**: Reduced from document-level to operation-level contention

## Implementation Guidelines

### Lock Ordering Rules

Always acquire locks in this order to prevent deadlocks:

1. `doc` (Read or Write lock)
2. `doc.pull`
3. `doc.attachment`
4. `doc.push`

### Error Handling

- If any lock acquisition fails, release all previously acquired locks
- Use defer/finally patterns to ensure lock cleanup
- Implement timeout mechanisms for lock acquisition

### Monitoring and Debugging

- Add metrics for lock wait times and contention
- Log lock acquisition patterns in debug mode
- Use tracing to identify bottlenecks

## Future Considerations

- Use tracing/pprof to observe lock contention and further optimize if needed
- Consider lock-free algorithms for read-heavy operations
- Evaluate reader-writer lock performance vs. mutex performance under different load patterns

## Risks and Mitigation

| Risk                                         | Impact                    | Mitigation Strategy                                                                |
| -------------------------------------------- | ------------------------- | ---------------------------------------------------------------------------------- |
| Increased code complexity                    | Development overhead      | Use standardized lock helpers and runtime assertions                               |
| Potential for deadlocks                      | System hang               | Enforce consistent lock acquisition order (`doc` → `pull` → `attachment` → `push`) |
| Difficulty implementing lock-free operations | Performance regression    | Consider using rollback mechanisms or retry logic for failed operations            |
| Lock contention on popular documents         | Reduced performance gains | Monitor lock wait times and consider document sharding for hot documents           |
