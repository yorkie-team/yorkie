---
title: per-project-channel-session-ttl
target-version: 0.7.11
---

# Per-Project Channel Session TTL

## Problem

`ChannelSessionTTL` is currently a single global server config
(`server/config.go:68`, default `15s`). The channel manager receives this
value once at construction (`server/backend/backend.go`) and applies it
uniformly to every session across every project.

Project owners want this to be tunable per project. The driving use case
is small-room presence: with a 15s TTL, a user who leaves a room is
removed from the live count almost immediately, so a room with steady
but short visits looks emptier than it feels. Raising the project's TTL
to e.g. 60s keeps each visitor in the count roughly 4× longer (15s →
60s), inflating the live presence count and producing the "still feels
occupied" effect the room owner wants — without changing the actual
session lifecycle.

A global config cannot express this: changing it affects every project
on the server.

### Goals

- Allow each project to set its own channel session TTL via the existing
  admin API (and therefore the dashboard).
- Apply the per-project value transparently inside the channel manager's
  expiration cleanup, so existing client/SDK behavior does not change.
- Keep the global `ChannelSessionTTL` config as the default seed for new
  projects and the fallback when a project value is unset or invalid.
- Bound the value to a safe range so a misconfigured project cannot
  cause unbounded session retention.

### Non-Goals

- Per-project `ChannelSessionCleanupInterval`. The cleanup goroutine is
  a single ticker covering every channel; making the interval
  per-project would require either multiple goroutines or per-channel
  scheduling, neither of which the use case justifies.
- Per-project `ChannelSessionCountCacheTTL`. The count cache TTL is a
  separate concern (how stale the count served to clients can be); it
  is not what shapes the "feels occupied" effect. If a project sets
  session TTL well below the cache TTL the cache stays correct because
  expirations only ever decrease the count.
- Live re-keying of existing sessions when a project changes its TTL.
  The new value takes effect on the next cleanup tick (≤ cleanup
  interval, default 10s). Sessions already in flight keep their
  identity; only the expiration check changes.

## Design

### Project field

Add `ChannelSessionTTL` (Go duration string, e.g. `"15s"`, `"1m"`) to:

- `api/types/project.go` — `Project.ChannelSessionTTL string` plus a
  `ChannelSessionTTLAsTimeDuration() (gotime.Duration, error)` helper,
  mirroring `ClientDeactivateThresholdAsTimeDuration`.
- `api/types/updatable_project_fields.go` — add the field to
  `UpdatableProjectFields` with the same validator family as
  `client_deactivate_threshold` (duration parse + min/max bound check).
- `api/yorkie/v1/resources.proto` —
  - `Project.channel_session_ttl` (next available field number).
  - `UpdatableProjectFields.channel_session_ttl`
    (`google.protobuf.StringValue`).
  - Regenerate via `make proto`.
- `server/backend/database/project_info.go` —
  - `ChannelSessionTTL string \`bson:"channel_session_ttl"\``.
  - Default in `NewProjectInfo()`:
    `DefaultChannelSessionTTL.String()` (15s, same constant as today's
    global default — re-exported from `database` if needed to avoid an
    import cycle with `server/config`).
  - `DeepCopy()` and `UpdateFields()` updated for the new field.
  - MongoDB and memory backends pick up the field automatically through
    `ProjectInfo` marshaling; no separate migration needed.

#### Bounds

The validator enforces:

- minimum **1s** (zero or negative would make every session expire
  immediately on the next cleanup tick, which is almost certainly a
  configuration mistake);
- maximum **5m** (300s). A channel keeps every session struct in memory
  until expiration; a runaway value multiplies per-channel memory by the
  ratio of TTL to actual visit length. 5m comfortably covers the
  "linger" use case while bounding the worst case.

Out-of-range or unparseable values are rejected at the admin API; the
runtime falls back to the global default if it ever sees a bad stored
value (defensive — should not happen given API validation).

### Backfill

Existing project rows have no `channel_session_ttl` field. The runtime
treats an empty value as "use the global default", so no DB migration
is required. New projects get the explicit default written by
`NewProjectInfo`.

### Channel manager: read at cleanup time

The `channel.Manager` already loads project info per channel inside
`collectAndPublishMetrics`
(`server/backend/channel/manager.go:539`):

```go
project, err := m.db.FindProjectInfoByID(ctx, key.ProjectID)
```

`FindProjectInfoByID` is cached by the database layer (it is on the
hot path of every RPC's auth interceptor), so a per-channel lookup
inside the cleanup loop is cheap.

`CleanupExpired` becomes:

1. Collect channel keys (already lock-free via
   `m.channels.ForEach`).
2. Within the loop, before classifying expired sessions, resolve the
   TTL for `key.ProjectID`. Memoize per project for the duration of one
   cleanup pass (a `map[types.ID]gotime.Duration` built lazily inside
   `CleanupExpired`) so a project with many channels only pays for one
   lookup per tick.
3. Call `classifyExpiredSessions(now, projectTTL, ch.Sessions)` with
   the resolved per-project TTL instead of `m.sessionTTL`.

If the project lookup fails (e.g. project deleted concurrently), log
and fall back to `m.sessionTTL` for that channel. This is the same
philosophy `collectAndPublishMetrics` already uses.

`m.sessionTTL` is retained as:

- the seed value at server boot (passed via `NewManager`);
- the fallback when a project has no `ChannelSessionTTL`, parsing
  fails, or the project info cannot be fetched.

`classifyExpiredSessions` already takes `sessionTTL gotime.Duration` as
a parameter, so no signature change is needed there.

### Admin API and dashboard

`UpdateProject` already routes through `UpdatableProjectFields`; the
new field is wired automatically once added to the proto, the
validator, and `ProjectInfo.UpdateFields`. The dashboard side
(separate repo) reuses the same form pattern as
`clientDeactivateThreshold`: a text input validated against the Go
duration regex, mapped one-to-one into the RPC.

### Components

- `api/yorkie/v1/resources.proto` — proto field additions.
- `api/types/project.go` — `ChannelSessionTTL` field + helper.
- `api/types/updatable_project_fields.go` — updatable field + validator.
- `server/backend/database/project_info.go` — DB struct field, default,
  copy/update wiring.
- `server/backend/channel/manager.go` — per-channel TTL resolution in
  `CleanupExpired` with intra-tick memoization and fallback to
  `m.sessionTTL` on lookup/parse failure.
- Tests: `manager_test.go` table-driven cases for (a) per-project TTL
  applied, (b) fallback when project lookup fails, (c) memoization
  hits once per project per tick, (d) bound validation in
  `updatable_project_fields_test.go`.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Runaway memory if a project picks an extreme TTL. | Validator caps at 5m. The cap is documented in the dashboard help text and the proto comment. |
| Project lookup adds load to the cleanup loop. | `FindProjectInfoByID` is already cached. Memoize per project inside one cleanup pass so per-tick cost is `O(distinct_projects_with_channels)`, not `O(channels)`. |
| Stale TTL after a project setting change. | The next cleanup tick (≤ cleanup interval, default 10s) picks up the new value. Sessions are short-lived; this is well within the user-perceptible "soon" window. |
| Project lookup failure during cleanup. | Fall back to `m.sessionTTL` and log at warn — same behavior pattern as `collectAndPublishMetrics`. |
| Existing global config users surprised by behavior change. | Behavior change is zero for projects that do not set the field: the default `15s` matches today's global default, and unset projects fall back to `m.sessionTTL` (also driven by the same global config). |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Look up TTL at cleanup time, not at session/channel creation. | The cleanup goroutine is the only consumer of TTL. Pushing TTL onto every `Channel` or `Session` would duplicate state across thousands of objects and force re-keying on project updates. The cleanup-time lookup runs once per project per tick. |
| Memoize per project, not globally across ticks. | A per-tick map is stack-bounded, drops automatically when the tick ends, and always reflects the latest committed project value. A long-lived cache would need invalidation tied to project updates. |
| Keep global config; treat it as the default seed and the fallback. | Operators still need a knob for self-hosted defaults. Removing it would break existing deploys and force a value into the project schema even when the operator hasn't decided. |
| Cap at 5m. | The use case ("feel like the room is busier") is satisfied well before 5m. Higher values trade UX for memory linearly with no obvious user benefit, so cap rather than warn. |
| Duration string field, not int seconds. | Matches every other duration field on `ProjectInfo` (`ClientDeactivateThreshold`, all webhook intervals). Re-uses the dashboard's existing duration regex and the existing validator family. |
| No separate cleanup interval per project. | Cleanup is a single goroutine sweeping all channels. Per-project intervals would need either many goroutines or per-channel scheduling — neither is justified by the use case. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Store TTL on each `Channel` at creation. | Updating a project's TTL would only affect future channels, not existing ones, until they go empty and are recreated. The cleanup-time lookup gives near-immediate effect with no extra state. |
| Look up project info on every session's expiration check. | Wastes the obvious memoization opportunity: every session in a channel shares the same project. Per-channel lookup is the right granularity. |
| Replace the global config entirely with the project field. | Operators need a default to control across self-hosted clusters, and breaking the existing config would be an unforced migration burden. The two coexist cleanly: global = default seed and fallback. |
| Expose `ChannelSessionCountCacheTTL` at the project level too. | Adds surface area for no current user need. The count cache shapes data freshness, not the presence-count "feel" the user is asking for. Can be added later if a real case appears. |
| Validate via the dashboard only. | The admin API is callable directly (CLI, scripts). Validation must live in `UpdatableProjectFields` so every entry point enforces the bounds. |

## Tasks

Track execution plans in `docs/tasks/active/` as separate task documents.
