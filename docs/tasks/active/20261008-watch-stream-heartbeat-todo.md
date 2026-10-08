**Created**: 2026-10-08

# Watch stream heartbeat and SDK idle timeout

Issue: yorkie-team/yorkie#2153

## Problem

A Watch stream carries one `initialization` response and then events only. A
quiet document sends nothing, so a half-open connection — a laptop resuming
from sleep, a VPN change, a proxy dropping an idle socket without a reset —
is indistinguishable from a quiet document. The stream stays "connected"
until TCP or a load balancer gives up. Unary RPCs keep working because each
opens a new connection, so a watching-only client sees the document freeze
with no error.

`RefreshChannel` does not cover this: it is a unary call the client sends, so
it proves the request path, not the stream.

## Plan

1. **Proto** — add `WatchHeartbeat` as a third case of the `WatchResponse`
   oneof, and `heartbeat_interval_ms` to `WatchInitialization`. A server that
   does not send heartbeats advertises 0.
2. **Server config** — `Backend.WatchHeartbeatInterval` (default `20s`, `0s`
   disables), with a `--backend-watch-heartbeat-interval` flag, validation
   and a `Parse…` accessor, following `ChannelSessionCleanupInterval`.
3. **Server** — `Watch` advertises the configured interval in milliseconds in
   its initialization response; `streamMergedEvents` runs a ticker that sends
   a heartbeat whenever the interval elapses. Every event resets the timer, so
   a busy stream sends no heartbeats.
4. **Go client** — treat a heartbeat as a no-op rather than
   `ErrUnsupportedWatchResponseType`, and when the advertised interval is
   non-zero, cancel the stream if nothing arrives within
   `watchIdleTimeoutFactor` × the interval. The existing reader loop already
   reconnects on a stream error, so a timeout recovers through that path.
   An advertised 0 keeps today's behavior — a new client never times out
   against a server that does not send heartbeats.
5. **Go client** — bound the wait for the initialization response with the
   same idle timeout so a first attach cannot hang forever on a stream that
   never answers.

The JS SDK halves of the issue (`packages/sdk/src/client/watch.ts`,
`client.ts`) live in `yorkie-team/yorkie-js-sdk` and are out of this repo.

## Known risk

The issue's design has the server heartbeat every stream unconditionally. Old
JS SDKs ignore the unknown oneof case, but this repo's Go client treats it as
`ErrUnsupportedWatchResponseType`, which is terminal. So a new server breaks
a pre-change Go client unless the operator sets the interval to `0s`. Noted
for review; see the PR body.

## Checklist

- [x] proto: `WatchHeartbeat` case + `heartbeat_interval_ms`
- [x] `make proto`
- [x] server config option, flag, validation, accessor, sample config
- [x] server sends heartbeats and advertises the interval
- [x] Go client ignores heartbeats
- [x] Go client idle timeout, gated on a non-zero advertised interval
- [x] Go client initialization-response timeout
- [x] unit tests for the config and the client's idle-timeout arithmetic
