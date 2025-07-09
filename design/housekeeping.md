---
title: housekeeping
target-version: 0.6.18
---

# Housekeeping

## Summary

Housekeeping is a background service that periodically cleans up resources and data that are no longer needed in Yorkie. It plays a crucial role in maintaining the performance and efficiency of the CRDT-based collaborative system by managing memory usage and optimizing data storage.

### Goals

- Automatically deactivate inactive clients to optimize garbage collection efficiency
- Provide document compaction to reduce storage overhead by removing old changes
- Ensure system performance and resource utilization remain optimal over time

### Non-Goals

- Direct control over programming language-level garbage collection
- Real-time cleanup operations that could impact user experience
- Manual intervention for routine maintenance tasks

## Proposal Details

### Overview

The Housekeeping service runs as a scheduled background process using `gocron` scheduler. It performs two main types of operations:

1. **Client Deactivation**: Deactivates inactive clients to optimize GC
2. **Document Compaction**: Consolidates document changes to reduce storage overhead

### Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Scheduler     │    │   Housekeeping  │    │   Background    │
│   (gocron)      │───▶│   Tasks         │───▶│   Workers       │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                                │
                                ▼
                       ┌─────────────────┐
                       │  Distributed    │
                       │  Locking        │
                       └─────────────────┘
```

### Configuration

```yaml
Housekeeping:
  # Interval between housekeeping runs (default: 30s)
  Interval: "30s"

  # Maximum number of candidates to be returned per project (default: 500)
  CandidatesLimitPerProject: 500

  # Maximum number of projects to be returned in a single run (default: 100)
  ProjectFetchSize: 100

  # Minimum number of changes required to compact a document (default: 1000)
  CompactionMinChanges: 1000
```

### Client Deactivation for Efficient Garbage Collection

#### How It Works

Garbage collection in Yorkie is optimized through a two-phase approach:

1. **Client Deactivation Phase**: Identifies and deactivates clients that have been inactive for more than 24 hours
2. **Garbage Collection Phase**: Removes tombstone nodes that are no longer referenced by any active clients

This approach is essential because:

- Inactive clients prevent garbage collection of tombstone nodes
- Deactivating unused clients allows for more aggressive cleanup
- The `minVersionVector` can be updated more frequently, enabling better garbage collection

#### Client Deactivation Process

```go
// Pseudocode for client deactivation
for each project {
    candidates = FindInactiveClients(project, threshold: "24h")
    for each candidate {
        DeactivateClient(candidate)
    }
}
```

#### Why Client Deactivation is Necessary for GC

In CRDT systems, garbage collection relies on `minVersionVector` to determine which tombstone nodes can be safely removed. Inactive clients with outdated version vectors prevent the `minVersionVector` from advancing, blocking garbage collection of potentially large amounts of data.

### Document Compaction

#### Overview

Document compaction reduces storage overhead by:

1. Removing old change history
2. Creating a new initial change that represents the current document state
3. Maintaining document integrity while reducing metadata size

#### Compaction Process

```go
// Pseudocode for document compaction
for each project {
    candidates = FindCompactionCandidates(project, minChanges: 1000)
    for each document {
        if !IsDocumentAttached(document) {
            CompactDocument(document)
        }
    }
}
```

#### Compaction Criteria

- Document must have at least `CompactionMinChanges` changes (default: 1000)
- Document must not be currently attached to any client
- Document content must remain identical after compaction

### Performance Characteristics

#### Scheduling and Concurrency

- **Interval**: Runs every 30 seconds by default
- **Concurrency Control**: Uses distributed locking to prevent multiple instances from running simultaneously
- **Project Cycling**: Processes projects in a round-robin fashion to distribute load
- **Batching**: Limits candidates per project to prevent overwhelming the system

#### Monitoring and Logging

The service provides detailed logging for monitoring:

```
HSKP: candidates 150, deactivated 45, 2.3s
HSKP: candidates 89, compacted 12, 1.8s
```

### Cluster Mode Considerations

In clustered deployments:

- Only the master server executes housekeeping tasks (leader election)
- Prevents duplicate work across cluster nodes
- Future versions may distribute tasks by project for better scalability

### Related Components

#### Garbage Collection Algorithm

For detailed information about the garbage collection algorithm, refer to:

- [`garbage-collection.md`](garbage-collection.md): General garbage collection principles
- [`gc-for-text-type.md`](gc-for-text-type.md): Text-specific garbage collection

#### Integration Points

- **PushPull Operations**: Garbage collection is triggered during document synchronization
- **Version Vectors**: `minVersionVector` calculation depends on active client status
- **Snapshot Creation**: Compaction may trigger snapshot regeneration

### Configuration Examples

#### Development Environment

```yaml
Housekeeping:
  Interval: "10s"
  CandidatesLimitPerProject: 10
  ProjectFetchSize: 5
  CompactionMinChanges: 100
```

#### Production Environment

```yaml
Housekeeping:
  Interval: "1m"
  CandidatesLimitPerProject: 1000
  ProjectFetchSize: 200
  CompactionMinChanges: 5000
```

### Future Enhancements

- **Project-based Task Distribution**: Distribute housekeeping tasks across cluster nodes by project
- **Adaptive Thresholds**: Automatically adjust compaction thresholds based on usage patterns
- **Metrics Integration**: Enhanced monitoring through Prometheus metrics
