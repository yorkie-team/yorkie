---
title: channel-readonly-attach
target-version: 0.7.9
---

# Channel Read-Only Attach

## Problem

A channel's `session_count` is the headline metric of `Channel` — applications display it as "N people viewing this room", "1,234 online", etc. Today the only way to obtain that number is to `Attach` to the channel: the count is delivered as a field of `RefreshChannelResponse` after the server has registered the caller in `Channel.Sessions`.

This conflates two distinct intents:

1. **Participating** — the caller wants to be counted (e.g. user opens a write composer; they are "writing").
2. **Read-only viewing** — the caller wants to know the count *before* deciding to participate (e.g. a floating "Write Post (N writing)" button that should display the count without contributing to it).

Because there is no read-only path, intent (2) is structurally impossible: any client that wants to display the count is itself part of it. The displayed number is always off by one (themselves), and worse, every viewer inflates the count seen by everyone else.

A common UX pattern exhibits the problem directly. Consider a forum or chat room where a button shows a live count of who is currently composing a message. The button is visible to everyone on the surrounding page, but clicking it (and only clicking it) makes the user a writer. To display that count today, the surrounding page would have to attach to the writers' channel — at which point every page view counts as a write-in-progress.

### Goals

- Allow a client to `Attach` to a channel without contributing to the reported `session_count`.
- Preserve full real-time count delivery to such clients (they still receive pubsub updates from the same channel).
- Keep the wire-level change minimal — one optional proto field, no new RPCs.
- Stay backwards compatible: existing clients (no flag) behave exactly as today.

### Non-Goals

- **In-place upgrade** read-only → participant on the same `session_id`. Re-attachment via detach + new first-call is sufficient; in typical UX flows this happens naturally via route transition (e.g. surrounding page → composer page).
- **Authorization split** between read-only and participant attach. Both use the existing `AttachChannel` ReadWrite permission. A stricter permission tier (read-only attach requires only ReadOnly) can be added later without breaking clients.
- **Aggregate counts** (read-only + participant combined) exposed through any public API. Server-internal metrics may track it for operational reasons.
- **Heartbeat tuning per mode**. Both modes share the same TTL/heartbeat policy.
- **Broadcast restriction**. Read-only here describes membership accounting, not message-flow direction. Read-only sessions remain permitted to call `Broadcast`; the flag affects `session_count` only. (If a use case ever needs "uncounted *and* send-only-disabled", a separate `silent` flag is a better fit than overloading `read_only`.)

## Design

Built on top of [PR #1800](https://github.com/yorkie-team/yorkie/pull/1800) / [#1802](https://github.com/yorkie-team/yorkie/pull/1802), which collapses the 5-RPC channel lifecycle into a single `RefreshChannel`. The first-call path (`session_id` empty) carries activation + attach; the heartbeat path carries TTL refresh + count. Read-only mode is a flag on the first-call.

### Proto

`api/yorkie/v1/yorkie.proto`:

```proto
message RefreshChannelRequest {
  string channel_key = 1;
  string client_id = 2;
  string session_id = 3;
  string client_key = 4;
  map<string, string> metadata = 5;

  // read_only is meaningful only on the first call (session_id empty).
  // When true, the resulting session is attached to the channel but
  // excluded from session_count reported to all clients.
  // Default: false (participant, current behavior).
  bool read_only = 6;
}
```

`session_count` in `RefreshChannelResponse` already means "participant count" once this field exists; the wire format is unchanged.

### Server data model

`server/backend/channel/manager.go`:

```go
type Session struct {
    ID        types.ID
    Key       types.ChannelRefKey
    Actor     time.ActorID
    ReadOnly  bool          // NEW — set at Attach time, immutable for the session's lifetime
    updatedAt atomic.Int64
}
```

`Manager.Attach` signature gains the flag:

```go
func (m *Manager) Attach(
    ctx context.Context,
    key types.ChannelRefKey,
    clientID time.ActorID,
    readOnly bool,
) (types.ID, int64, error)
```

The session is constructed with `ReadOnly: readOnly`. All existing concurrency invariants (the `activeCount` retry loop, the RLock/WLock interaction with `Detach`) are unchanged — read-only sessions occupy the same `Sessions` map and contribute to `activeCount`, they just don't contribute to the reported count.

### Counted vs uncounted

`SessionCount` filters at read time rather than maintaining a parallel counter:

```go
func (m *Manager) SessionCount(key types.ChannelRefKey, includeSubPath bool) int64 {
    if !pkgchannel.IsValidChannelKeyPath(key.ChannelKey) {
        return 0
    }

    countParticipants := func(ch *Channel) int64 {
        var n int64
        ch.Sessions.ForEach(func(_ types.ID, s *Session) bool {
            if !s.ReadOnly {
                n++
            }
            return true
        })
        return n
    }

    if !includeSubPath {
        ch := m.channels.Get(key)
        if ch == nil {
            return 0
        }
        return countParticipants(ch)
    }

    var total int64
    m.channels.ForEachDescendant(key, func(ch *Channel) bool {
        total += countParticipants(ch)
        return true
    })
    return total
}
```

The previous lock-free `ch.Sessions.Len()` was O(1); the new path is O(N) over the channel's sessions. At expected scale (per-channel hundreds, occasionally low thousands of sessions; count queried at first-call and heartbeat, not per-message), this is acceptable. See *Risks* for the operational ceiling.

`Attach` and `Detach` publish `ChannelEvent.SessionCount` to pubsub on every membership change. Both code paths must call `SessionCount` (the filtered helper) rather than reading `ch.Sessions.Len()` directly. Specifically:

- `Attach` (current line ~319): replace `newSessionCount := int64(channel.Sessions.Len())` with the filtered helper for both the return value and the pubsub event.
- `Detach` (current line ~364): same replacement.

A consequence: when **only read-only sessions** attach or detach, `session_count` does not change. We still publish a `ChannelEvent` for symmetry, but the `SessionCount` field will be unchanged from the previous event. The optimization (skip publish when count unchanged) is deferred; correctness first.

### RPC handler

`server/rpc/yorkie_server.go`, `firstChannelRefresh`:

```go
sessionID, sessionCount, err := s.backend.Channel.Attach(
    ctx, refKey, actorID, req.Msg.ReadOnly,
)
```

`heartbeatChannelRefresh` is untouched — read-only-ness is a property of the existing session and need not be re-stated on heartbeat.

### SDK

Client option:

```ts
// packages/sdk/src/client/client.ts
client.attach(channel, {
  syncMode,
  channelHeartbeatInterval,
  readOnly: true,        // NEW
});
```

The flag is forwarded into the `RefreshChannel` first-call request. After the first response, the client stores `sessionID` and proceeds with heartbeats; read-only-ness is server-side state and is not re-sent.

React surface:

```tsx
<ChannelProvider channelKey="writers" readOnly>
  <PostComposerButton />
</ChannelProvider>
```

`readOnly` defaults to `false`. The flag is read at first-call only; mutating the prop while attached has no effect (consistent with `channelKey` semantics). To toggle, callers remount — in typical UX flows this happens naturally via route transition: the surrounding page unmounts its read-only `ChannelProvider` and the composer page mounts a participant `ChannelProvider` with the same `channelKey`.

### Lifecycle in a route-transition UX

```
route /room/:id                      → <ChannelProvider channelKey="writers" readOnly />
route /room/:id/compose              → <ChannelProvider channelKey="writers" />          ← participant
```

Timeline on a third-party tab attached to `writers`:

1. User A on `/room/abc` — first-call with `read_only=true` → server registers session, `SessionCount` unchanged, `ChannelEvent` carries the unchanged count.
2. User A clicks the composer button, navigates to `/room/abc/compose`. The read-only provider unmounts (SDK does not need to detach eagerly; cleanup via TTL is fine because the session is invisible to the count). The participant provider on the new route mounts → first-call with `read_only=false` → server registers a *new* session, `SessionCount` increments, `ChannelEvent` published.
3. User A navigates back. Participant session detaches on unmount (SDK should detach eagerly here so the count delta is prompt). Read-only provider mounts again on `/room/abc`.

The asymmetry — participant detaches eagerly on unmount, read-only falls off via TTL — is intentional: read-only disappearance does not move the displayed number, so cleanup latency is invisible to users. SDK implementations may choose to detach both eagerly for tidiness; that's a quality-of-implementation choice, not a correctness requirement.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| `SessionCount` becomes O(N) over sessions in a channel | Bounded by per-channel session count, called only at attach/heartbeat/detach (not per broadcast). At 10k sessions/channel × 100 Hz of refreshes the cost is dominated by the existing pubsub fan-out, not the count. Re-evaluate with a parallel `participantCount atomic.Int64` if a single channel exceeds 50k sessions. |
| Read-only abuse — clients spam read-only attachments to bypass per-user limits or pad metrics | Read-only sessions still consume `Channel.Sessions` capacity and `activeCount`. The cleanup ticker reaps idle sessions identically. Existing per-project rate limits apply to `RefreshChannel`. |
| Stale displayed count after participant disappears silently | Unchanged from today — driven by the channel `sessionTTL` (15s in #1802). Read-only viewers see the same staleness as anyone else. |
| Read-only sessions receive pubsub events (broadcasts, presence churn) they "shouldn't" | Decision: deliver them all. Filtering at the channel boundary requires a separate fan-out path; the use cases that motivated this design (count-only display) discard the events at the SDK anyway. |
| `Manager.Attach` signature change cascades through internal callers | Internal-only API. Audit `grep -rn "Channel\.Attach\|Manager.*Attach"` under `server/`; the call sites are `firstChannelRefresh` and tests. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Flag on `Session` rather than separate `ReadOnlySessions` map | Single membership list keeps `activeCount`, TTL cleanup, and pubsub fan-out paths unchanged. Filtering is one predicate. |
| Filter at read time in `SessionCount`, not at write time via parallel counter | Avoids a second source of truth that has to stay consistent under concurrent Attach/Detach. Read cost is acceptable for channel scale. |
| `read_only` is immutable per session (no in-place upgrade RPC) | Typical UX upgrades read-only → participant via route change, which detaches and first-calls fresh. Adding `SetReadOnly` later is non-breaking if upgrade-in-place becomes necessary. |
| Both read-only and participant use `AttachChannel` ReadWrite permission | Per requirement: any client may read-only-attach. A ReadOnly-style permission tier can be added later without breaking read-only clients (they'd just keep working on the existing permission). |
| `read_only` flag only meaningful on first-call | Heartbeats carry no membership state changes; this keeps `heartbeatChannelRefresh` a pure liveness ping. |
| Read-only sessions still receive `ChannelEvent` even when only read-only churn occurs | Cost is the same pubsub broadcast; read-only viewers explicitly want count updates and the unchanged count is a valid event payload. |
| `read_only` constrains count accounting only — `Broadcast` is **not** restricted | Membership accounting and message-flow direction are orthogonal concerns. Coupling them via the name would overload `read_only`. If a future use case needs "uncounted *and* send-disabled", introduce a separate `silent` flag rather than reinterpret this one. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| New `PeekChannel(key) → count` single-shot RPC | No push semantics — read-only viewers must poll. Loses the existing pubsub fan-out optimization. Adds a permission surface. Useful for HTTP/REST callers but inferior for clients already running the connect stream. |
| Two channels: `lobby` (everyone) + `active` (participants only) | Requires every UI displaying the count to attach to the lobby, doubling channel pressure for a single semantic. Lobby count itself is meaningless. SDK has to keep two providers in sync. |
| Use channel `Broadcast` for "writing-start"/"writing-stop" events; clients aggregate locally | New tab joining mid-stream sees no state. Crash recovery requires per-event TTLs the channel doesn't currently model. Reinvents `Sessions` poorly. |
| Stash read-only-ness in `metadata` (e.g. `metadata["mode"]="read_only"`) and filter on that | Stringly-typed. `metadata` is per-client (`ActivateClient`) data persisted to the DB, not per-session; using it for a session-scoped flag couples unrelated concepts. Explicit boolean is clearer. |
| Allow `Watch` stream subscription without prior `Attach` (no `Sessions` entry at all) | Bigger architectural change. `Watch` currently mixes doc and channel subscriptions and requires an active client. Refactoring `Watch` to support unattached viewers is out of scope. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.

Anticipated split:

- Server: proto + `Manager.Attach` signature + `SessionCount` filter + RPC handler wiring + manager tests. Branched off #1802.
- SDK: proto regeneration, `client.attach` option, `<ChannelProvider readOnly>` prop, e2e test. Branched off the SDK companion of #1802.
- Example: a playground page demonstrates a sibling read-only `ChannelProvider` on the surrounding page plus a child route that mounts a participant `ChannelProvider` with the same `channelKey`.
