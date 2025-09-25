---
title: cluster-node-management
target-version: 0.6.32
---

<!-- Make sure to append document link in design README.md after creating the document. -->

# Cluster Node Management

## Summary

Cluster Node Management introduces a leader–member structure to manage the lifecycle of nodes in a Yorkie cluster.  
This mechanism improves efficiency by eliminating redundant housekeeping tasks and reducing database load.  
It also provides a reliable foundation for tracking node liveness and coordinating leadership within the cluster.


### Goals

- Ensure that at most one leader exists at any given time
- Support periodic renewal of leadership and seamless succession when a leader fails
- Maintain up-to-date liveness information for all nodes using heartbeats and the liveness window
- Persist leader and member states in the database for observability and consistency across the cluster

### Non-Goals

- A complete redesign of the leadership election algorithm

## Proposal Details

### Overview

Cluster Node Management is the component responsible for coordinating the lifecycle of nodes in a Yorkie cluster.  
It provides three core responsibilities:

1. **Leadership Management**  
   Ensures that exactly one leader exists at a time. Handles leader election, renewal of the lease, and succession when a leader fails or disconnects.

2. **Node Liveness Tracking**  
   Uses periodic heartbeats and a configurable liveness window to determine whether nodes are active. This allows the cluster to detect failures and maintain awareness of which nodes are alive.

3. **State Persistence**  
   Persists the state of both leaders and members in the database. This enables observability across the cluster and ensures that leadership and liveness information are consistently available for coordination.

Through these responsibilities, the system reduces redundant housekeeping tasks, lowers database load, and creates a foundation for reliable cluster coordination.

### System Components

#### Leader Node
- Periodically renews leadership before `leaseDuration` expires.
- On successful renewal, updates `expires_at`.
- On failure, demotes itself to follower.

#### Member Node
- Attempts to acquire leadership if no active leader exists.
- On failure, updates `updated_at` to signal liveness.
- Always persists state through upserts into the database.

#### Database
- Stores node states, including leadership and liveness information.
- Applies TTL-based cleanup to automatically remove expired node documents.

### Operational Flow
```
loop every heartbeatInterval:
  now := DB_SERVER_TIME()

  if leader:
    if TryRenewLeadership(now + leaseDuration):
      update expires_at = now + leaseDuration
    else:
      becomeFollower()
  else:
    if TryAcquireLeadership(now + leaseDuration):
      becomeLeader()
      update expires_at = now + leaseDuration
    else:
      update updated_at = now

```

### Configuration

```yaml
# Interval at which each node sends a heartbeat (updates its `updated_at`) to indicate liveness.
heartbeatInterval: 5s  

# Maximum allowed gap since the last heartbeat.
# A node is considered active if NOW - updated_at <= livenessWindow.
livenessWindow: 10s  

# Time period for which leadership is valid.
# A leader must renew its lease before this duration expires.
leaseDuration: 15s  

# Database-level TTL for automatic removal of expired node documents (e.g., MongoDB TTL index).
# This ensures cleanup of stale leaders/members that are no longer alive.
dbTTL: 10s+  
```

### Responsibilities and Limitations

**Responsibilities**

* Guarantee at most one active leader within the cluster
* Maintain liveness information for all nodes
* Persist leader and member states for observability
* Provide predictable rules for leader renewal and succession

**Limitations**

* Leaderless windows may occur during renewal failures or until TTL cleanup
* Depends on database mechanisms (e.g., MongoDB TTL) for expired record removal
