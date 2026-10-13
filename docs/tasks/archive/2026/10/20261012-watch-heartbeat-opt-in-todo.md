**Created**: 2026-10-12

# Watch heartbeat opt-in via WatchRequest

Issue: #2194

## Problem

The Watch heartbeat added in #2163 is a server-wide switch
(`--backend-watch-heartbeat-interval`), defaulting to `0s`. It cannot be
turned on by a server whose client versions it does not control: a client
older than the heartbeat answers the unrecognized `WatchResponse` body with
`ErrUnsupportedWatchResponseType` and retires its watch pipeline. One
interval also has to fit every client, from a proxy with a short idle
timeout to a mobile client saving battery.

## Plan

1. **Proto** — add `int64 heartbeat_interval_ms = 4` to `WatchRequest`.
   `0`/unset means "no heartbeats", so an old SDK that omits the field reads
   as `0` and keeps today's behavior. Regenerate with `make proto`.
2. **Server** — per stream, clamp the requested interval to the server's
   bounds, heartbeat at the clamped value, and advertise it in
   `WatchInitialization.heartbeat_interval_ms`. A request of `0` gets `0`
   advertised and no `WatchHeartbeat` bodies.
3. **Server config** — replace the single interval with bounds:
   `WatchHeartbeatIntervalMin` (`5s`) and `WatchHeartbeatIntervalMax`
   (`60s`). The minimum stops a client asking for a 1s heartbeat and
   multiplying load; `max = 0s` is the server-wide kill switch. Deprecate
   `--backend-watch-heartbeat-interval` / `WatchHeartbeatInterval`: ignored,
   with a startup warning when set.
4. **Go SDK** — `WithWatchHeartbeatInterval`, defaulting to `20s`,
   `client.NoWatchHeartbeat` to opt out (`0` on the wire); sent on both the
   document and the channel Watch request.

## Acceptance criteria

- [x] `WatchRequest.heartbeat_interval_ms` exists and is honored.
- [x] Requested interval is clamped to `[min, max]`; the effective value is
      advertised in `WatchInitialization`.
- [x] A request of `0` yields advertised `0` and no heartbeats.
- [x] `max = 0s` disables heartbeats regardless of what a client asks.
- [x] `--backend-watch-heartbeat-interval` is deprecated and ignored at every
      value, with a startup warning; `config.sample.yml` documents the bounds.
- [x] Go client sends its configured interval; default `20s`.
- [x] Unit tests cover the clamp, the opt-out and the kill switch.

## Out of scope

JS/iOS/Android SDKs (tracked in the JS mirror, yorkie-js-sdk#1479).

**Archived**: 2026-10-13 — acceptance met by #2209 (closes #2194); boxes ticked at archive. The SDK side is yorkie-js-sdk#1479/#1483.
