**Created**: 2026-05-26

# Project Stats Cache — Lessons

## What worked

- Subagent-driven TDD with a two-stage review (spec compliance, then code
  quality) caught a non-trivial number of polish items: missing interface
  doc contracts, thin tests, dead unused slices, asymmetric error-return
  semantics. Each was caught at the task that introduced it, not at PR
  review.
- The 5-line `housekeepingState` struct shared across deactivate,
  compaction, and stats tasks made Task 10 trivial — roughly 50 lines
  mostly mirrored from the existing housekeeping tasks.
- Bundling the design doc and implementation plan as the first two
  commits on the branch gave every subsequent task an unambiguous SSOT
  to point reviewers at.

## Surprises / non-obvious adaptations

- `server/backend/database/database.go` and `mongo/client.go` alias
  `"time"` as `gotime` to avoid colliding with `pkg/document/time`. The
  plan snippets used `time.Time`; the actual code in those files needs
  `gotime.Time`.
- Mongo driver v2 removed `SetReadPreference` from `CountOptions`. This
  repo uses driver v2 (`go.mongodb.org/mongo-driver/v2`), so read
  preference must be applied at the collection level:
  `c.collection(ColClients, options.Collection().SetReadPreference(readpref.SecondaryPreferred()))`.
  The `c.collection(...)` helper returns a fresh handle per call, so the
  option is scoped to that call only.
- Plan Task 5 had to be folded into Task 4. Removing `GetClientsCount`
  and `GetDocumentsCount` from the interface immediately broke
  `server/projects/projects.go` (the only callers), so the build could
  not stay green between the two tasks. Task 5 Step 2 (the
  `StatsUpdatedAt` field on `types.ProjectStats`) ended up in Task 7's
  commit instead, since it depended on Task 6's proto change and Task 7's
  type addition.
- `make proto` in the local dev environment regenerates three unrelated
  files (`yorkie.connect.go`, `yorkie.openapi.yaml`,
  `cluster.openapi.yaml`) because of a connect-go plugin version
  mismatch. Those churn was reverted before landing Task 6. A separate
  follow-up should pin the plugin versions or run buf with locked
  plugins.

## Coordination notes for downstream

- `timestamppb.New(time.Time{})` does NOT produce the Unix epoch — it
  produces `0001-01-01T00:00:00Z`. Dashboard cold-start detection should
  check `seconds < 0` or call `t.AsTime().IsZero()`, not
  `seconds == 0`.
- `ProjectStatsRefreshInterval` defaults to `5m` and is opt-out only via
  an explicit `"0s"`. An empty string in YAML is filled in by
  `EnsureDefaults`. To run yorkie without project-stats refresh, set
  `ProjectStatsRefreshInterval: "0s"` in config.
- The CLI flag `--housekeeping-project-stats-refresh-interval` is
  referenced in error messages but is NOT actually registered in
  `cmd/yorkie/server.go`. Operators expecting CLI overrides will need a
  follow-up commit to wire it through cobra.
- The refresh job uses `tryLock(statsRefreshKey)`. On a sharded cluster
  where the count of activated clients exceeds what one tick can refresh
  in under `ProjectStatsRefreshInterval`, subsequent ticks no-op rather
  than queue. Operators should monitor `HSKP: project-stats #...` log
  frequency and extend `ProjectStatsRefreshInterval` if it falls behind.
