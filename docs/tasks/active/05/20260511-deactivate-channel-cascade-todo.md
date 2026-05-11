**Created**: 2026-05-11

# Cascade Channel Session Detach on Client Deactivate

## Problem

When a browser refreshes/closes without sending explicit detach RPCs, the
channel session lingers until `ChannelSessionTTL` (default 60s) expires
and the cleanup ticker (10s) sweeps it. Users observe ~60s of stale
membership in channel polling.

`clients.Deactivate` already handles document detach via cluster, but
channel sessions are managed in a separate per-server in-memory map
(`channel.Manager`) and are not cascaded.

## Goal

Move responsibility for channel session cleanup from the SDK to the
server. When `clients.Deactivate` runs (via SDK-triggered RPC or via
housekeeping), all channel sessions held by the actor — **across the
entire cluster** — should be detached in the same call.

Worst-case stale window improves:
- RPC path (SDK reachable): immediate detach
- Housekeeping path (SDK unreachable): ~30s (housekeeping interval)
- Channel TTL remains as last-resort safety net (~60s)

## Why cluster broadcast is needed

Channels are sharded across cluster nodes by `channelKey.FirstPath()`
(Istio consistent hashing). A single actor may hold sessions on
multiple servers. Deactivate is keyed by clientID and routes to one
server, which only knows about its own local channel sessions.

Existing cluster RPCs (`DetachDocument`, `ListChannels`, etc.) all
route by channel/document key, so no actor-keyed cluster RPC exists.
Need to add one and use `prepareClusterClients` (same pattern as
`BroadcastChannelList`) to fan out.

## Plan

### Step 1: Channel Manager — `DetachByActor`
- [ ] **Add `DetachByActor(ctx, actor time.ActorID) (int, error)` to `channel.Manager`**
  - Look up `m.clientToSession[actor]`, snapshot session IDs (avoid
    iterator invalidation during deletion)
  - Call existing `Detach` per session
  - Return number of detached sessions
  - Continue on per-session error; aggregate and log
- [ ] **Unit test** — attach 2 channels for actor A, 1 channel for
  actor B; DetachByActor(A) removes only A's sessions

### Step 2: Cluster RPC — `DetachActorFromChannels`
- [ ] **Add to `api/yorkie/v1/cluster.proto`**
  ```proto
  rpc DetachActorFromChannels (ClusterServiceDetachActorFromChannelsRequest)
    returns (ClusterServiceDetachActorFromChannelsResponse) {}

  message ClusterServiceDetachActorFromChannelsRequest {
    string project_id = 1;
    bytes actor_id = 2;
  }
  message ClusterServiceDetachActorFromChannelsResponse {
    int32 detached_count = 1;
  }
  ```
- [ ] **Run `make proto`**
- [ ] **Implement RPC handler in `server/rpc/cluster_server.go`**
  - Validates project, decodes actor ID, calls local
    `s.backend.Channel.DetachByActor`

### Step 3: Backend broadcast helper
- [ ] **Add `BroadcastDetachActorFromChannels(ctx, projectID, actorID)`
  to `server/backend/backend.go`**
  - Mirror `BroadcastChannelCount` shape
  - For each cluster node, call RPC; aggregate detached count
  - Best-effort: log partial failures, do not fail overall

### Step 4: Wire cascade into `clients.Deactivate`
- [ ] **Call `be.BroadcastDetachActorFromChannels` after cluster
  document detach loop, before `be.DB.DeactivateClient`**
- [ ] Log error but do not fail deactivation (best-effort; channel
  TTL is the final safety net)

### Step 5: ClusterClient pool method
- [ ] **Add `DetachActorFromChannels(ctx, projectID, actorID)` to
  ClusterClient in `server/backend/pool_*.go`** (mirror existing
  `ListChannels` shape)

### Step 6: Integration tests
- [ ] **`test/integration/channel_test.go`**: Activate → AttachChannel
  → DeactivateClient → assert `SessionCount` drops to 0 immediately
  (well before TTL)
- [ ] Cover both `Synchronous=true` and async path
- [ ] Verify housekeeping cascade: force inactive client (skip
  RefreshChannel), confirm cleanup at housekeeping tick (not at TTL)

## Out of Scope

- SDK changes: leave `DetachChannel` RPC API intact for explicit
  unsubscribe scenarios.
- Optimizing housekeeping fan-out (N clients × M nodes RPCs). Same
  amplification exists in `BroadcastChannelList`; revisit only if
  operational data shows it's a problem.

## Verification

- `make lint && make test`
- `make test -tags integration`
- Manual: run server with debug log, refresh test page 10×, confirm
  immediate detach (no `CHAN: ... expires[...]` from TTL cleanup)

## Files

- `api/yorkie/v1/cluster.proto`
- `server/backend/channel/manager.go` — `DetachByActor`
- `server/rpc/cluster_server.go` — RPC handler
- `server/backend/backend.go` — `BroadcastDetachActorFromChannels`
- `server/backend/pool_*.go` — ClusterClient method
- `server/clients/clients.go` — cascade call in `Deactivate`
- `test/integration/channel_test.go` — new test cases
