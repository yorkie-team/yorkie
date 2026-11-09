---
title: channel-batch-window
target-version: 0.7.27
---

# Channel Batch Window

## Problem

Every channel event (a broadcast or a session-count change) goes through
a `BatchPublisher` built with a hardcoded 100ms window
(`server/backend/pubsub/channel_subscription.go`). The publisher runs a
fixed ticker per channel and delivers whatever arrived since the last
tick, so each event waits for the next tick: 0–100ms, uniformly.

Measured on a local server with two Node clients (one sending at 15Hz,
one watching), a broadcast takes p50 57ms and p95 100ms one way. The
same run with the window set to 5ms gives p50 8ms and p95 16ms. For a
real-time app with a latency budget around 100ms, such as a game's
entity stream, the transport alone uses the whole budget.

The batching buys nothing on the wire for channels. Unlike the document
publisher, the channel publisher has no `OnEnqueue` to fold events, and
`publish()` sends each event to each subscriber as its own stream
message. The window only adds latency.

Lowering the constant is not enough either. The ticker wakes every
channel every window whether or not anything happened, so a 5ms window
means 200 wake-ups a second for every idle channel on the server.

### Goals

- Deliver an event that arrives on an idle channel without waiting for
  a tick.
- Keep the ceiling the window gives today: at most one delivery round
  per window per channel under a steady stream.
- Make the window configurable: a server-wide default, and a per-project
  override through the admin API and the CLI (the dashboard form is a
  follow-up in yorkie-team/dashboard).
- No wake-ups for a channel with no events.

### Non-Goals

- The document publisher. It folds `DocChanged` events in `OnEnqueue`,
  so its window does real work, and its delivery timing stays as it is.
- Coalescing channel events (for example, dropping superseded
  session-count events). Possible later; not needed for latency.
- Re-timing a channel that is already open the moment a project changes
  its window. See "Applying a changed window".

## Design

### Leading-edge throttle for channels

`BatchPublisher` gains a throttle mode, enabled by the channel
publisher only:

- `Publish` appends the event as today and then signals the publisher's
  goroutine through a one-slot channel (a non-blocking send, so a
  pending signal absorbs the rest of a burst).
- The goroutine waits on that signal. On a signal it delivers at once if
  at least one window has passed since the last delivery; otherwise it
  arms a timer for the rest of the window. Signals that arrive while the
  timer is armed are ignored: the timer delivers everything at once.
- With no events there is no timer and no ticker, so an idle channel
  costs nothing.

An isolated broadcast is delivered immediately, a burst is split into at
most one delivery per window, and a window of `0` delivers every event
as soon as the goroutine picks it up. Delivery still happens on the
publisher's goroutine, so a slow subscriber never blocks the `Broadcast`
RPC that published the event.

The ticker mode stays for the document publisher, unchanged.

Reaping dead subscriptions does not need the ticker. A subscription
only dies inside `publish()` (after consecutive failed sends), and the
same `publish()` call reaps it.

### Server default

A new backend config `ChannelBatchWindow` (Go duration string, flag
`--channel-batch-window`, default `100ms`), validated to `[0s, 1s]` at
startup. It is the window for every project that does not set its own.

### Per-project override

`ChannelBatchWindow` is added to the project, following
[per-project-channel-session-ttl](per-project-channel-session-ttl.md):

- `api/yorkie/v1/resources.proto`: `Project.channel_batch_window` and
  `UpdatableProjectFields.channel_batch_window`
  (`google.protobuf.StringValue`).
- `api/types/project.go`: the field and
  `ChannelBatchWindowAsTimeDuration()`.
- `api/types/updatable_project_fields.go`: the field with a
  `channel_batch_window` validator: empty (back to the server default) or
  `[0s, 1s]`.
- `server/backend/database/project_info.go`: the field, copy, update and
  `ToProject` wiring. New projects store an empty value, which means
  "use the server default", so an operator's flag keeps applying to
  them; no migration is needed for existing rows.
- `cmd/yorkie/project`: `update --channel-batch-window` and the column
  in `list`.

### Resolving the window

`yorkieServer.subscribeChannel` already holds the project. It resolves
the window there (the project's value if set and valid, else the server
default, parsed once into `Backend.ChannelBatchWindow`) and passes it to `PubSub.SubscribeChannel`, which creates the
channel's publisher with it or, for a channel already open, sets it on
the existing publisher.

### Applying a changed window

A channel's publisher lives from its first subscriber to its last. A
project change applies to a channel when the next client subscribes to
it, and to every channel opened afterwards. Every reconnect and every
new watcher subscribes, so a busy channel picks it up quickly; an
unchanged, long-lived set of watchers keeps the old window until one of
them reconnects. A window already running when the change lands ends as it
was set, so the change can lag by at most one old window (≤ 1s).

In a cluster, each node resolves the window for the publishers it owns:
`SubscribeChannel` has a single caller, the Watch path, which always has
the project from the auth context, while cluster broadcasts and the
channel manager only publish to existing subscriptions. `UpdateProject`
invalidates the project cache on every node; if that broadcast fails, a
node keeps the old value until its project cache entry expires
(`ProjectCacheTTL`, 10m by default).

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| A burst now produces one delivery immediately and one at the end of the window, where the ticker produced one. | The ceiling per window is one extra round per burst, and channel events are sent one by one either way: subscribers receive the same number of messages. |
| A project sets `0s` and floods the publisher goroutine. | Each event is still one send per subscriber, the same work as today; only the grouping changes. The goroutine and the subscription's bounded buffer stay as they are. |
| A changed window does not reach a channel whose watchers never change. | Documented above; the next subscriber applies it. |
| A bad value stored for a project. | Validated at the admin API; at runtime an unset or unparsable value falls back to the server default. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Leading-edge throttle, not a timer started by the first event. | A trailing timer makes every event wait the whole window: at the 100ms default the average wait would go from 50ms to 100ms. The throttle makes the default faster for everyone and keeps the same ceiling. |
| Channels only. | The document publisher folds `DocChanged` events, so its window is load control, not just delay. Changing its timing needs its own review. |
| Range `[0s, 1s]`. | `0` is safe because channel batching saves no messages. Above 1s only adds latency with nothing gained. |
| Resolve at subscribe time, not per publish. | Subscribing already has the project in hand; publishing does not, and a lookup per broadcast would sit on the hot path. |
| Empty project value means the server default. | Lets an operator change the default for every project that has not chosen one. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Make only the ticker's period configurable. | A small period wakes every idle channel at that rate; the cost grows with channels, not with traffic. |
| Publish synchronously from the `Broadcast` RPC. | A slow subscriber (up to `publishTimeout` per send) would stall the sender's RPC. |
| Look up the project on every publish. | A per-broadcast lookup on the hot path to pick up a rare setting change. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
