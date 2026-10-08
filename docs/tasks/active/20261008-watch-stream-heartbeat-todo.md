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
2. **Server config** — `Backend.WatchHeartbeatInterval` (default `0s`, which
   disables the heartbeat; `20s` is the suggested value once the clients of a
   server understand heartbeats), with a `--backend-watch-heartbeat-interval`
   flag, validation and a `Parse…` accessor, following
   `ChannelSessionCleanupInterval`. The integration lane turns it on at `2s`
   through `test/helper`.
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
5. **Go client** — reconnect after a timed-out stream through the reader's
   existing path, retrying the handshake with a backoff: the handshake right
   after a lost stream is the one most likely to fail, and a single attempt
   would retire the pipeline over a momentary outage. The channel watch runs
   the same watchdog, since a half-open channel stream silently stops every
   broadcast and session count.

The JS SDK halves of the issue (`packages/sdk/src/client/watch.ts`,
`client.ts`) live in `yorkie-team/yorkie-js-sdk` and are out of this repo.

## Compatibility

The issue's design has the server heartbeat every stream unconditionally. Old
JS SDKs ignore the unknown oneof case, but this repo's Go client treats it as
`ErrUnsupportedWatchResponseType`, which is terminal. A server that
heartbeated by default would therefore retire the watch pipeline of every Go
client that predates the heartbeat, seconds after an upgrade. The heartbeat
ships off (`0s`) for that reason: operators turn it on once their clients
understand it.

No initialization-response timeout ships either. Everything the server does
before its first response — `FindActiveClientInfo`, the auth webhook with its
project-configurable retries and wait intervals, `subscribeResources` — is a
handshake the client has no basis to put a fixed deadline on, and a caller
that wants one has `ctx`.

## Checklist

- [x] proto: `WatchHeartbeat` case + `heartbeat_interval_ms`
- [x] `make proto`
- [x] server config option, flag, validation, accessor, sample config
- [x] server sends heartbeats and advertises the interval
- [x] Go client ignores heartbeats
- [x] Go client idle timeout, gated on a non-zero advertised interval
- [x] Go client reconnects a timed-out stream, retrying with a backoff
- [x] channel watch runs the same idle watchdog
- [x] unit tests for the config and the client's idle-timeout arithmetic
- [x] client tests for the watchdog end to end: a silent stream times out as
      `ErrWatchStreamIdle` and reconnects, a rejected reconnect is retried,
      and a stream with no advertised heartbeat is left alone
