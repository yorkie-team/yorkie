---
title: watch-heartbeat
target-version: 0.7.25
---

# Watch Heartbeat

## Problem

A Watch stream can go half-open: the TCP connection is established and the
server still holds a subscription for it, but nothing either side sends ever
arrives. Neither end learns this from the socket. The server added a periodic
`WatchHeartbeat` on idle streams so a client can tell a live stream from a
half-open one, and the client arms an idle watchdog at a multiple of the
interval the server advertises.

That heartbeat was a server-wide switch, `--backend-watch-heartbeat-interval`,
defaulting to `0s` (off). It could not be turned on:

- A heartbeat is a new case on the `WatchResponse` body oneof. A client older
  than it has no case for it; this repo's own Go client answers an
  unrecognized body with `ErrUnsupportedWatchResponseType` and retires its
  watch pipeline without reconnecting. A server that sent heartbeats by
  default would cut such clients off seconds after an upgrade.
- A hosted service cannot control which SDK versions connect to it, so it can
  never establish that every client understands the heartbeat. The feature
  stays off, and the client-side watchdog stays disarmed.
- One interval has to fit every client. A client behind a proxy with a short
  idle timeout needs a shorter interval than a mobile client saving battery.

### Goals

- Let a server run with heartbeats on without any risk to old clients.
- Let each client choose the interval that suits its network.
- Keep a hosted server in control of the load heartbeats add.

### Non-Goals

- Making the heartbeat a per-project setting. It is a transport concern of one
  stream, not a property of the data the stream carries.
- Changing the client-side idle watchdog, which already keys off the
  advertised interval and so needs no change.

## Design

The opt-in moves onto the request, so the decision is made per stream by the
only party that knows whether it can handle a heartbeat.

```protobuf
message WatchRequest {
  string client_id = 1;
  repeated ResourceDescriptor resources = 2;
  string actor_id = 3;
  int64 heartbeat_interval_ms = 4;
}
```

`0`, which is what an SDK older than the field leaves on the wire, asks for no
heartbeats. This is the same pattern `actor_id = 3` was added with. A single
interval field is both the opt-in and the tuning knob, so no separate `bool`
is needed.

The server clamps the request into its own bounds and uses the result for both
the heartbeat timer and the `heartbeat_interval_ms` it advertises in
`WatchInitialization`:

```go
func (s *yorkieServer) resolveHeartbeatInterval(requestedMs int64) time.Duration {
	if s.heartbeat.Max <= 0 {
		return 0
	}
	if s.heartbeat.Forced > 0 {
		return s.heartbeat.Forced
	}
	if requestedMs <= 0 {
		return 0
	}
	requested := time.Duration(requestedMs) * time.Millisecond
	return min(max(requested, s.heartbeat.Min), s.heartbeat.Max)
}
```

Because the advertised value is the effective one, the client sizes its
watchdog against what the server will actually do, not what it asked for.

Configuration is two bounds, `--backend-watch-heartbeat-interval-min` (`5s`)
and `--backend-watch-heartbeat-interval-max` (`60s`). The minimum is the one a
hosted service needs: it stops a client asking for a 1s heartbeat and
multiplying the server's load. The maximum doubles as the server-wide kill
switch, since "the longest interval this server will serve is zero" already
reads as "no heartbeats".

`--backend-watch-heartbeat-interval` keeps its original name and its original
power, and is unset by default: an operator who passes it decides for every
stream, whatever the clients ask for. It is what a server needs when it must
detect a half-open stream itself rather than leave that to the peer —
otherwise the peer alone decides whether the server ever writes to the
stream, and a client that opts out leaves a half-open one undetectable from
the server's side. Because a forced heartbeat reaches clients too old to
understand one, it is safe only once every client of that server does, which
is why it is a deliberate setting rather than a default.

### Compatibility

| Client | Server | Result |
|---|---|---|
| Old (no field) | New | Reads as `0`: no heartbeats, advertised `0`. Unaffected |
| New, opted out | New | Same as above |
| New | Old | The old server ignores the unknown field and advertises its own setting |
| New | New | Heartbeats at the requested interval clamped to the server's bounds |

No combination breaks, which is what lets the server ship with heartbeats on.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A client asks for a very short interval and multiplies server load | The minimum bound clamps it up; the operator sets the floor |
| An operator who deliberately disabled heartbeats has them re-enabled by an upgrade | `--backend-watch-heartbeat-interval`, when explicitly set to `0s`, still disables them |
| A client opts out and the server cannot tell its stream has gone half-open | `--backend-watch-heartbeat-interval` set above `0s` forces one on every stream, overriding the request |
| A misconfigured `min > max` silently picks one | Rejected by config validation at startup |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Interval on the request, not a `bool` plus a server interval | One field is both the opt-in and the tuning knob, and an absent field already means "off" |
| Advertise the effective interval, not the requested one | The client's watchdog must track what the server will really send |
| Kill switch on the maximum bound | Avoids a third option whose meaning overlaps the other two |
| The single-interval flag kept as an operator override rather than retired | Liveness detection is the server's concern too, so the last word has to stay with the operator; the flag already meant "this interval, on every stream" |
| The override left unset by default | Its flag default is `0s`, the kill switch, so only a flag the operator actually passed reaches the config |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Keep the server-wide interval and wait for every SDK to catch up | A hosted service cannot establish that, so the feature would never turn on |
| A per-project setting in the dashboard | The heartbeat is a property of one stream's network path, which a project setting cannot see |
| Version negotiation on the client's user agent | Fragile, and says nothing about what the network between them needs |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
