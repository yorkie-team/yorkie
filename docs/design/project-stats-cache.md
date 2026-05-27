---
title: project-stats-cache
target-version: 0.7.10
---

# Project Stats Cache

## Problem

`GetProjectStats` (used by the dashboard) calls `CountDocuments` on the
`clients` and `documents` collections per request. For projects with tens of
millions of activated clients, the count query takes minutes — the dashboard
times out at 3s and the API becomes unusable.

A production project recently hit 83.58M activated clients, where
`db.clients.count({project_id, status: "activated"})` measured ~223s. The
compound index `(project_id, status, updated_at)` is present and used; sheer
match cardinality is the cause. MongoDB still has to walk every matching
index entry.

### Goals

- Make `GetProjectStats` return within the dashboard's 3s deadline regardless
  of collection size.
- Keep counts close enough to truth for dashboard display. Minute-scale
  staleness is acceptable.
- Reuse the existing housekeeping infrastructure (leader election,
  distributed locking, scheduler).

### Non-Goals

- Real-time exact counts. The dashboard is the only consumer.
- Caching `ChannelsCount`. It is computed via in-memory RPC fanout across
  cluster nodes, has no DB scan, and reflects currently-connected state.
  Caching it would obscure live behavior without saving meaningful work.
- Caching warehouse-backed stats (`ActiveUsers`, `Sessions`, etc.). The
  warehouse (StarRocks) is already OLAP-optimized.
- Fixing the upstream growth that produced 83M activated clients. Tracked
  separately as housekeeping efficiency work.

## Design

### Data Model

Add three fields to `ProjectInfo` (stored in the `projects` collection):

```go
type ProjectInfo struct {
    // ... existing fields ...
    StatsClientsCount   int64     `bson:"stats_clients_count"`
    StatsDocumentsCount int64     `bson:"stats_documents_count"`
    StatsUpdatedAt      time.Time `bson:"stats_updated_at"`
}
```

The `stats_` prefix marks these as asynchronously maintained. No new
collection, no new index — `_id` lookup is sufficient.

### Read Path

Collapse the two slow reads in `server/projects/projects.go` into one:

```go
// Before
documentsCount, err := be.DB.GetDocumentsCount(ctx, id)
clientsCount, err := be.DB.GetClientsCount(ctx, id)

// After
counts, err := be.DB.GetProjectStatsCounts(ctx, id)
```

The MongoDB implementation does a projection-only `FindOne` on the project
doc, bypassing `ProjectCache`:

```go
func (c *Client) GetProjectStatsCounts(
    ctx context.Context,
    projectID types.ID,
) (*database.ProjectStatsCounts, error) {
    var doc struct {
        StatsClientsCount   int64     `bson:"stats_clients_count"`
        StatsDocumentsCount int64     `bson:"stats_documents_count"`
        StatsUpdatedAt      time.Time `bson:"stats_updated_at"`
    }
    err := c.collection(ColProjects).FindOne(
        ctx,
        bson.M{"_id": projectID},
        options.FindOne().SetProjection(bson.M{
            "stats_clients_count":   1,
            "stats_documents_count": 1,
            "stats_updated_at":      1,
        }),
    ).Decode(&doc)
    if errors.Is(err, mongo.ErrNoDocuments) {
        return &database.ProjectStatsCounts{}, nil
    }
    if err != nil {
        return nil, fmt.Errorf("get project stats counts %s: %w", projectID, err)
    }
    return &database.ProjectStatsCounts{
        ClientsCount:   doc.StatsClientsCount,
        DocumentsCount: doc.StatsDocumentsCount,
        UpdatedAt:      doc.StatsUpdatedAt,
    }, nil
}
```

`ProjectCache` is intentionally bypassed. The cache holds project metadata
with a 10-minute TTL; routing stats through it would compound staleness
(5-minute refresh + 10-minute cache = up to 15 minutes). A projection-only PK
lookup returns hundreds of bytes — cheap enough without caching.

The old `GetClientsCount` / `GetDocumentsCount` interface methods have a
single caller each and can be removed.

### Refresh Path

Register a new housekeeping task in `server/server.go`:

```go
be.Housekeeping.RegisterTask(statsRefreshInterval, func(ctx context.Context) error {
    return projects.RefreshStats(ctx, be, refreshCandidatesLimit, &statsRefreshState)
})
```

In `server/projects/housekeeping.go` (new file):

```go
const statsRefreshKey = "housekeeping/project-stats-refresh"

func RefreshStats(
    ctx context.Context,
    be *backend.Backend,
    limit int,
    state *refreshState,
) error {
    locker, ok := be.Lockers.LockerWithTryLock(statsRefreshKey)
    if !ok {
        return nil
    }
    defer locker.Unlock()

    projects, lastID, err := be.DB.FindProjectInfosForRefresh(ctx, limit, state.LastID())
    if err != nil {
        return err
    }
    for _, p := range projects {
        cc, err := be.DB.CountActivatedClients(ctx, p.ID)
        if err != nil {
            logging.From(ctx).Warnf("count clients %s: %v", p.ID, err)
            continue
        }
        dc, err := be.DB.CountDocuments(ctx, p.ID)
        if err != nil {
            logging.From(ctx).Warnf("count documents %s: %v", p.ID, err)
            continue
        }
        if err := be.DB.UpdateProjectStats(ctx, p.ID, cc, dc, time.Now()); err != nil {
            logging.From(ctx).Warnf("update project stats %s: %v", p.ID, err)
        }
    }
    state.SetLastID(lastID)
    return nil
}
```

The state follows the same cursor-based pattern as `deactivateState` and
`compactionState` in `server.go`: paginate across project IDs, wrap when
exhausted.

`FindProjectInfosForRefresh` is a new database method (existing
`ListProjectInfos` is scoped to a single owner — not suitable for global
iteration). Signature:

```go
FindProjectInfosForRefresh(
    ctx context.Context,
    limit int,
    lastProjectID types.ID,
) ([]*ProjectInfo, types.ID, error)
```

Implementation: `find(filter).sort({_id: 1}).limit(N)`, where `filter` is
`{_id: {$gt: lastID}}` for non-zero cursors and `{}` for the initial cycle
(`lastID == ZeroID`). The initial cycle is inclusive because the auto-created
`default` project has `_id == ZeroID` — a strictly-greater filter would skip
it on every cycle. After processing, the cursor advances to the last returned
project's ID, so the inclusive boundary only widens the very first batch of a
term.

`CountActivatedClients` and `CountAliveDocuments` use secondary read preference
to keep the load off the primary. In mongo-driver v2, `CountOptions` no longer
exposes `SetReadPreference`, so the preference is applied at the collection
level (the `c.collection(...)` helper returns a fresh handle per call, so the
option is scoped to this single call):

```go
c.collection(
    ColClients,
    options.Collection().SetReadPreference(readpref.SecondaryPreferred()),
).CountDocuments(
    ctx,
    bson.M{"project_id": projectID, "status": database.ClientActivated},
)
```

### Configuration

Add to `server/backend/housekeeping/config.go`:

```yaml
Housekeeping:
  # ... existing fields ...
  ProjectStatsRefreshInterval: "5m"  # default
```

If the value is empty, `EnsureDefaults` fills in the 5-minute default. The
opt-out path is the explicit string `"0s"`, which the parser maps to a
`time.Duration` of `0` and the registration site skips. Tests that do not
need the task running set `"0s"` explicitly.

### API Surface

Add `stats_updated_at` to `GetProjectStatsResponse` in
`api/yorkie/v1/admin.proto`. Existing field numbers 1–15 are in use, so the
new field takes number 16:

```proto
message GetProjectStatsResponse {
  int64 documents_count = 1;
  int64 clients_count = 2;
  // ... existing fields 3–15 ...
  google.protobuf.Timestamp stats_updated_at = 16;  // NEW
}
```

The dashboard renders "Updated X minutes ago" next to the cached values.
When `stats_updated_at` is zero (cold start, project not refreshed yet), the
dashboard shows a placeholder.

### Concurrency and Lifecycle

- **Leader-only**: `Housekeeping.RegisterTask` already skips non-leaders.
  Single source of refresh per cluster.
- **TryLock**: if a prior cycle is still running (e.g., a very long count on
  a huge project), the new tick no-ops. No queue buildup.
- **Cycle duration**: counting ~80M rows on a secondary can still take
  minutes. The cursor advances per cycle; one slow project does not block
  another from being refreshed on a subsequent tick.
- **Cold start**: until first refresh writes the fields, reads return zeros
  with `UpdatedAt == time.Time{}`. The dashboard treats zero `UpdatedAt` as
  "not yet computed".

### Memory Backend

`memory.DB` implements the same three methods. Counts run over in-memory
tables — trivial cost. This keeps the integration tests honest about
end-to-end behavior.

### Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Count on 80M+ collection takes longer than refresh interval | TryLock prevents pileup; cursor advances on next free tick. Operators can extend interval. |
| Secondary read returns slightly stale values | Acceptable — counts are already approximate by design. |
| Project doc write contention with normal updates | `UpdateOne($set: {stats_*})` is field-scoped. Project settings updates are infrequent. |
| `ProjectCache` could surface stale `stats_*` elsewhere | Read path bypasses the cache. No other consumer reads `stats_*` via cache. |
| Cold start until first refresh | Zero `UpdatedAt` is honest about freshness; dashboard shows a placeholder. Synchronous fallback would reintroduce the 223s timeout. |

### Design Decisions

| Decision | Reason |
|----------|--------|
| Project doc field, not a new `project_stats` collection | One less collection, no new index, atomic refresh per project. Per-project aggregates naturally live on the project doc. |
| Bypass `ProjectCache` on read | Prevents staleness from compounding (refresh + cache TTL). PK projection lookup is already fast. |
| Single `GetProjectStatsCounts` method | One round-trip, atomic snapshot — both counts share `stats_updated_at`. |
| Cache `ClientsCount` and `DocumentsCount` only | They share the same `CountDocuments` performance problem. `ChannelsCount` is in-memory cluster RPC (already fast, and caching would obscure live state). Warehouse stats are already OLAP-optimized. |
| Reuse existing Housekeeping infrastructure | Leader election, distributed locks, scheduler all exist for the same kind of background work. |
| Secondary read preference for count | Offloads expensive scans from the primary. Eventual consistency is acceptable. |
| 5-minute default refresh interval | Dashboard staleness budget. Configurable per deployment. |
| Hide cold start behind zero values | Synchronous fallback would reintroduce the original timeout. Zero + `UpdatedAt` is honest. |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Separate `project_stats` collection | More moving parts (collection, index, model) for no architectural gain. Per-project aggregates belong with the project. |
| Increment/decrement counter on activate/deactivate | Drift risk under crashes; requires transactions or reconciliation. Initial backfill still needs a full scan. Reserve as a future step if 5m staleness becomes intolerable. |
| `estimatedDocumentCount` | O(1) but counts the entire collection — cannot filter by `project_id` or `status`. |
| Increase API timeout | Doesn't address the 223s scan; just shifts the failure mode. |
| In-memory cache per pod | Multi-pod inconsistency, cold start on every restart, harder to bound staleness. The project doc is a simpler storage. |
| Cache `ChannelsCount` too | It's already fast (in-memory RPC, no DB scan). Caching would replace a live value with a 5-minute-old snapshot, misleading the dashboard. |
| Replace upstream with healthier deactivation | Required regardless (separate work), but does not on its own solve the read latency — projects naturally grow over time even with healthy housekeeping. |

## Tasks

Track execution in `docs/tasks/active/`.
