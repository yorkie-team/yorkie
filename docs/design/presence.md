---
title: presence
target-version: 0.6.36
---

# Presence

## Summary

This document describes Yorkie's Presence, a lightweight real-time user tracking mechanism designed for scalable applications. Presence provides two types of functionality:

1. **Document**: Attached to documents for tracking user metadata and states in collaborative editing
2. **Channel**: A pub/sub system for real-time communication, including presence counting and broadcasting

This document focuses on the overall Presence-related architecture and how both types work within the Yorkie ecosystem.

## Background

### Presence in Document

Yorkie originally provided Presence as part of Document attachment, where each client can set and update their presence data (e.g., cursor position, user info) that is synchronized with other clients via CRDT operations. This approach works well for collaboration features but has overhead for simple use cases like counting online users.

### Presence in Channel

For applications requiring real-time user count display (e.g., "1,234 users online") and pub/sub messaging, the Presence in Document approach becomes inefficient:

- Full CRDT synchronization is unnecessary for simple counters
- Each user attachment creates document overhead
- Presence data synchronization impacts performance at scale

The Presence in Channel provides a dedicated, lightweight pipeline for real-time communication including presence counting and broadcasting, designed for scenarios with hundreds of thousands to millions of concurrent users.

## Goals

- Provide unified Attachment API for both Documents and Channels
- Support scalable, approximate counting for high-concurrency scenarios
- Enable real-time pub/sub messaging with broadcast capabilities
- Enable real-time subscription to presence count changes
- Automatically handle stale sessions through TTL and heartbeat mechanisms
- Minimize performance impact on existing Document functionality

## Proposal Details

### Overall Architecture

The Presence system consists of three main layers:

```
┌─────────────────────────────────────────────────────────────┐
│                      Client Layer (SDK)                     │
│  ┌────────────────────────┐  ┌───────────────────────────┐  │
│  │   Document             │  │   Channel                 │  │
│  │   + Presence Data      │  │   (Pub/Sub + Presence)    │  │
│  │   (CRDT-based)         │  │   (Lightweight)           │  │
│  └────────────────────────┘  └───────────────────────────┘  │
│               │                           │                 │
│        Attach/Detach              Attach/Detach             │
│        PushPull                   Subscribe/Broadcast       │
│               │                           │                 │
└───────────────┼───────────────────────────┼─────────────────┘
                ↓                           ↓
┌─────────────────────────────────────────────────────────────┐
│                      Server Layer (Go)                      │
│  ┌─────────────────────┐          ┌───────────────────────┐ │
│  │  Document Manager   │          │  Channel Manager      │ │
│  │  - CRDT Operations  │          │  - In-memory Sessions │ │
│  │  - Presence Sync    │          │  - TTL Management     │ │
│  │                     │          │  - Broadcast Routing  │ │
│  └─────────────────────┘          └───────────────────────┘ │
│               └────────────┬───────────────┘                │
│                            ↓                                │
│                   ┌─────────────────┐                       │
│                   │  PubSub System  │                       │
│                   │  - Doc Events   │                       │
│                   │  - Channel Evt  │                       │
│                   └─────────────────┘                       │
└─────────────────────────────────────────────────────────────┘
```

### Resource Type Abstraction

Both Document and Channel implement the `attachable.Attachable` interface:

```go
type Attachable interface {
    Key() key.Key
    Type() ResourceType
    Status() StatusType
    SetStatus(StatusType)
    IsAttached() bool
    ActorID() time.ActorID
    SetActor(time.ActorID)
}

const (
    TypeDocument ResourceType = "document"
    TypeChannel  ResourceType = "channel"
)
```

This abstraction allows the Client to handle both resource types uniformly through a single Attach/Detach API.

### Client-Side Implementation

The client provides a unified `Attach()` API that works for both Documents and Channels. Each attachment tracks its lifecycle, watch stream, and heartbeat timer for TTL management.

**Key Components:**

- Unified attachment management through `attachable.Attachable` interface
- Automatic heartbeat for session TTL refresh
- Watch stream handling for real-time updates

### Server-Side Implementation

#### Channel Manager

The `channel.Manager` handles in-memory channel tracking with the following structure:

**Data Storage:**

- Main storage: `ChannelRefKey → [SessionID → SessionInfo]`
- Reverse index: `ClientID → [ChannelRefKey → SessionID]`
- Reverse index: `SessionID → ChannelRefKey` (for O(1) detach)

**Key Operations:**

1. **Attach**: Registers a new channel session, generates unique sessionID, publishes event via PubSub
2. **Detach**: Removes a channel session using O(1) lookup, publishes event via PubSub
3. **Refresh**: Updates TTL for active sessions by updating timestamp
4. **Broadcast**: Routes messages to all subscribers in the channel
5. **CleanupExpired**: Background task (runs every 10s) to remove stale sessions

#### PubSub System

The PubSub system handles event distribution for both documents and channels, providing subscribe/unsubscribe/publish operations for channel events and broadcast messages.

### RPC API

The server exposes dedicated RPC methods for channel operations:

- `AttachChannel`: Attach to a channel
- `DetachChannel`: Detach from a channel
- `RefreshChannel`: Refresh TTL of an active session
- `WatchChannel`: Stream real-time count updates and broadcast messages
- `Broadcast`: Send messages to all channel subscribers

### How It Works

#### Channel Flow

1. **Attach**: Client → AttachChannel RPC → Server generates sessionID → PubSub broadcasts count
2. **Subscribe**: Client → WatchChannel RPC → Server creates subscription → Streams count updates and broadcast events
3. **Broadcast**: Client → Broadcast RPC → Server routes message to all subscribers (except sender)
4. **Heartbeat**: Client timer (30s) → RefreshChannel RPC → Server updates timestamp
5. **Cleanup**: Server timer (10s) → Scans expired sessions → Detach and broadcast updates
6. **Detach**: Client → DetachChannel RPC → Server removes session → Broadcasts count update

#### Document Presence Flow (Existing)

1. **Attach**: Client → AttachDocument with presence data → Server stores document
2. **Watch**: Client → WatchDocument → Server streams presence events (watched/unwatched/changed)
3. **Update**: Client → Document.Update(presence) → PushPull RPC → Server broadcasts to watchers

### TTL and Heartbeat Mechanism

To handle abnormal disconnections (crashes, network failures), the Channel uses TTL-based expiration:

**Server-Side:**

- Each `SessionInfo` has an `UpdatedAt` timestamp
- Default TTL: 60 seconds
- Cleanup runs every 10 seconds
- Sessions expire when `now - UpdatedAt > TTL`

**Client-Side:**

- Heartbeat timer fires every 30 seconds (default)
- Calls `RefreshChannel()` to update server timestamp
- Continues until detachment or context cancellation

**Design Choices:**

- **UpdatedAt vs ExpiresAt**: Using `UpdatedAt` allows dynamic TTL adjustment without updating all sessions
- **Client-side heartbeat**: Reduces server complexity and allows clients to control refresh frequency
- **Graceful degradation**: Heartbeat failures don't break the application; sessions expire naturally

### Data Structures

**SessionInfo** (Server): Stores session ID, reference key, actor ID, and last update timestamp

**ChannelRefKey**: Identifies a channel by project ID and user-defined key (e.g., "room-123")

**Presence** (Client): Tracks channel key, attachment status, actor ID, current count, sequence number, and broadcast event handlers

### Configuration

**Server:**

- `session_ttl`: Session expiration time (default: 60s)
- `session_cleanup_interval`: Cleanup task frequency (default: 10s)

**Client:**

- `heartbeatInterval`: Heartbeat timer interval (default: 30s, must be < sessionTTL)

### Performance Considerations

**Scalability:**

- In-memory storage for O(1) access
- Thread-safe concurrent maps for lock-free reads
- Reverse indexes for O(1) detach operations
- Batch cleanup of expired sessions

**Resource Usage:**

- Memory: ~100 bytes per presence session
- Network: Minimal RPC calls (attach, detach, periodic refresh)
- CPU: Low overhead with background cleanup

**Approximate Counting:**

- Count may be temporarily inconsistent during concurrent attach/detach
- Cleanup creates brief lag (up to 10s) before removing expired sessions
- Acceptable for UI display ("~1,234 users online")
- Not suitable for critical business logic requiring exact counts

### Comparison: Document Presence vs Channel

| Feature             | Document Presence                          | Channel                            |
| ------------------- | ------------------------------------------ | ---------------------------------- |
| **Use Case**        | Collaborative editing, cursor tracking     | User count + pub/sub messaging     |
| **Data Type**       | Rich presence data (object)                | Counter + broadcast messages       |
| **Synchronization** | CRDT-based, strongly consistent            | Approximate, eventually consistent |
| **Scalability**     | Good (up to thousands)                     | Excellent (millions)               |
| **Overhead**        | Higher (document storage, CRDT ops)        | Lower (in-memory only)             |
| **Persistence**     | Persisted with document                    | In-memory only                     |
| **API**             | `doc.update((root, p) => p.set(...))`      | `channel.Broadcast()`              |
| **Events**          | `watched`, `unwatched`, `presence-changed` | `presence`, `broadcast`            |

### Risks and Mitigation

#### Risk 1: Memory Growth from Abandoned Sessions

**Risk**: Clients crash without detaching, accumulating stale sessions.

**Mitigation**:

- TTL-based expiration (default 60s)
- Background cleanup every 10s
- Clients send heartbeat every 30s to maintain active sessions

#### Risk 2: Count Inconsistency During High Concurrency

**Risk**: Race conditions during rapid attach/detach may cause brief inconsistency.

**Mitigation**:

- Use concurrent-safe data structures (`cmap.Map`)
- Sequence numbers for event ordering
- Acceptable trade-off for approximate counting use case

#### Risk 3: Network Partitions

**Risk**: Network issues prevent heartbeat delivery, causing premature expiration.

**Mitigation**:

- Graceful degradation: Client re-attaches automatically
- TTL is configurable for different network conditions
- Heartbeat failures don't crash the application

#### Risk 4: PubSub Overload During Mass Detach

**Risk**: Mass disconnect events (e.g., server restart) flood PubSub.

**Mitigation**:

- Cleanup runs in batches
- Event publishing is async
- PubSub uses buffered channels

### Future Enhancements

1. **Channel Analytics**: Historical count data and message analytics
2. **Custom TTL per Channel**: Allow per-channel TTL configuration
3. **Channel Metrics**: Prometheus metrics for monitoring
4. **Message Persistence**: Optional message history for channels
5. **Channel Types**: Explicit channel types (counter, pubsub, hybrid)
