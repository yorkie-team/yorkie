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
   multiplying load; `max = 0s` is the server-wide kill switch. Keep
   `--backend-watch-heartbeat-interval` as the operator's override: unset by
   default, `0s` off, anything else served on every stream.
4. **Go SDK** — `WithWatchHeartbeatInterval`, defaulting to `20s`, `0` to
   opt out; sent on both the document and the channel Watch request.

## Acceptance criteria

- [ ] `WatchRequest.heartbeat_interval_ms` exists and is honored.
- [ ] Requested interval is clamped to `[min, max]`; the effective value is
      advertised in `WatchInitialization`.
- [ ] A request of `0` yields advertised `0` and no heartbeats.
- [ ] `max = 0s` disables heartbeats regardless of what a client asks.
- [ ] `--backend-watch-heartbeat-interval` is unset by default, disables the
      heartbeat at `0s` and forces it on every stream above `0s`.
- [ ] Go client sends its configured interval; default `20s`.
- [ ] Unit tests cover the clamp, the opt-out and the kill switch.

## Out of scope

JS/iOS/Android SDKs (tracked in the JS mirror, yorkie-js-sdk#1479).
