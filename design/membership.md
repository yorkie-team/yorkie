---
title: membership management
target-version: 0.6.33
---

# Membership Management

## Summary

Membership management introduces a leader–member structure in the Yorkie cluster to manage the lifecycle of nodes. A single leader is elected to handle cluster-wide tasks (e.g., housekeeping), while all other nodes act as members and receive broadcasted messages.

### Goals

- Maintain up-to-date liveness information for all nodes and deliver broadcast messages to them.
- Guarantee at most one leader at a time, while tolerating short periods with no leader.
- Support smooth renewal of leadership and succession when the leader fails.

### Non-Goals

- Redesigning the leader election algorithm from scratch.

## Proposal Details

### Overview

The membership management component coordinates the lifecycle of nodes within a Yorkie cluster.

It provides three core responsibilities:

1. Leadership Management: Ensures exactly one leader exists. Handles leader election, lease renewal, and succession in case of leader failure or disconnection.
2. Node Liveness Tracking: Uses periodic heartbeats and a configurable liveness window to determine whether a node is active. This allows the cluster to detect failures and identify live nodes.
3. State Persistence: Persists both leader and member states in the database. This enables observability across the cluster and ensures consistent coordination of leadership and liveness information.

### Workflow

- Leader Election: When a node starts, it attempts to acquire leadership through the database. If successful, it becomes the leader; otherwise, it acts as a member.
- Leader Succession: When the leader fails or disconnects, another node attempts to acquire leadership. If successful, it becomes the new leader; otherwise, it remains a member.
- Heartbeat Renewal: All nodes periodically update their status in the database. The leader additionally renews its leadership lease.
- Liveness Evaluation: As nodes update their state, the cluster evaluates their liveness. If the last update exceeds the liveness window, the node is considered inactive.
- State Persistence: All state changes (e.g., leadership acquisition, lease renewal, node liveness updates) are stored in the database, with TTL cleanup for expired entries.

```
loop every interval:
  if is_leader:
    if TryRenewLeadership(DB_SERVER_TIME() + leaseDuration):
      update expires_at = DB_SERVER_TIME() + leaseDuration
    else:
      becomeFollower()
  else:
    if TryAcquireLeadership(DB_SERVER_TIME() + leaseDuration):
      becomeLeader()
      update expires_at = DB_SERVER_TIME() + leaseDuration
    else:
      update updated_at = DB_SERVER_TIME() // heartbeat update
```

### Configuration and Relationship Guidelines

- Renewal Interval (5s): How often nodes refresh their status. Leaders renew their lease; members attempt acquisition and update their heartbeat.
- Lease Duration (15s): Maximum guaranteed time for leadership.
- Liveness Window (10s): Time window used to determine inactivity if a node misses updates. Prevents false negatives due to transient issues.
- To ensure stability:
  - Renewal Interval × n < Lease Duration (with n ≥ 2).
  - Liveness Window must be larger than Renewal Interval and smaller than Lease Duration.
- Defaults (5s, 15s, 10s) satisfy these constraints.

### Leadership and Atomicity

Leadership is enforced at the database level using a partial unique index on the clusternodes collection to guarantee at most one leader:

```js
db.clusternodes.createIndex(
  { is_leader: 1 },
  { unique: true, partialFilterExpression: { is_leader: true } }
);
```

- Acquisition: Performed via a `findOneAndUpdate`(conditional upsert). Duplicate key errors are caught and retried when conflicts occur.
- Lease Renewal: Performed by extending `expires_at` only for documents matching the node’s `lease_token` and `is_leader: true`.
- Observability: All failures are logged and recorded in metrics to help operators identify leadership issues.

### Handling Leader Absence

Leader absence does not significantly impact the system. Only housekeeping tasks are delayed. User-facing requests remain unaffected. Therefore, short-term leader gaps are acceptable.

### Broadcast Design

- Each node records its rpcAddr in `clusternodes` collection.
- Other nodes query this information to perform direct RPC calls for broadcasting.
- Both leaders and followers may broadcast messages, which serve as advisory signals rather than mandatory delivery channels.
  - Example: A missed project cache invalidation message is tolerated because of TTL-based fallback.
- To avoid duplication, messages include UUIDs or sequence numbers, and receivers apply a dedupe window.

### Monitoring and Observability

Operators should monitor the following metrics:

- Leadership acquisition failure rate and number of conflicts.
- Leadership renewal failure rate.
- Last heartbeat timestamp per node.
- Broadcast success/failure counts and duplicate ratios.

## Risks and Mitigation

- Split-brain scenarios: Mitigated by database-enforced uniqueness for leadership.
- Transient leader absence: Mitigated by tolerating short gaps, as core user operations are unaffected.
- Broadcast reliability: Mitigated with UUID/sequence deduplication and TTL fallbacks.
- Database contention: Reduced by low-frequency renewal intervals and retry mechanisms.
