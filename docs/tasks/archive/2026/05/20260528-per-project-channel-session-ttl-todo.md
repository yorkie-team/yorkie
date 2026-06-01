**Created**: 2026-05-28

# Per-Project Channel Session TTL

Branch: `feat/per-project-channel-session-ttl`

Design: [docs/design/per-project-channel-session-ttl.md](../../design/per-project-channel-session-ttl.md)

## Goal

Allow each project to set its own `ChannelSessionTTL` via the existing
admin API (and thus the dashboard). The channel manager resolves the
TTL per channel at cleanup time, falling back to the global config when
a project value is missing or invalid. Bound the value to `1s..5m`.

## Scope

This todo covers all backend (yorkie) work. The dashboard form/RPC
mapping is a separate task tracked in the `yorkie-team/dashboard` repo
once this PR lands.

## Files touched

- `api/yorkie/v1/resources.proto` — add `channel_session_ttl` to
  `Project` (field 28) and `UpdatableProjectFields` (field 23).
- `api/yorkie/v1/*.pb.go` — regenerated via `make proto`.
- `api/types/project.go` — add `ChannelSessionTTL` field +
  `ChannelSessionTTLAsTimeDuration()` helper.
- `api/types/updatable_project_fields.go` — add field with new
  `channel_session_ttl` validator (parse + bounds).
- `api/types/updatable_project_fields_test.go` (new or extend) —
  validator unit tests.
- `api/converter/from_pb.go`, `api/converter/to_pb.go` — proto ↔ types
  mapping for the new field.
- `server/backend/database/project_info.go` — DB struct field,
  `DefaultChannelSessionTTL` constant, defaults in `NewProjectInfo`,
  `DeepCopy`, `UpdateFields`, `ToProject`.
- `server/backend/database/project_info_test.go` (new or extend) —
  default + update unit tests.
- `server/backend/channel/manager.go` — per-channel TTL resolution
  inside `CleanupExpired` with per-tick memoization and fallback.
- `server/backend/channel/manager_test.go` — TTL resolution unit
  tests using a stub `database.Database`.

## Plan

### Task 1: Add proto fields

- [ ] Edit `api/yorkie/v1/resources.proto`:
  - In `message Project`, append `string channel_session_ttl = 28;`
    after `auto_revision_enabled = 27;`.
  - In `message UpdatableProjectFields`, append
    `google.protobuf.StringValue channel_session_ttl = 23;` after
    `auto_revision_enabled = 22;`.
- [ ] Regenerate: `make proto`.
- [ ] Confirm no other generated files churn beyond the two messages
  via `git diff --stat api/yorkie/v1/`.
- [ ] Commit:
  ```
  Add channel_session_ttl to Project proto

  Reserves field 28 on Project and field 23 on
  UpdatableProjectFields for the per-project channel session TTL.
  The field is wired into Go types and the channel manager in
  follow-up commits.
  ```

### Task 2: ProjectInfo DB struct + default + update wiring

- [ ] In `server/backend/database/project_info.go`:
  - Add constant near other defaults (around line 48):
    ```go
    DefaultChannelSessionTTL time.Duration = 15 * time.Second
    ```
    (Mirrors `server/config.DefaultChannelSessionTTL` to avoid an
    import cycle. The two must stay in sync; cross-reference in a
    short comment.)
  - Add field on `ProjectInfo` (alongside `ClientDeactivateThreshold`):
    ```go
    // ChannelSessionTTL controls how long a presence-channel
    // session is retained after its last refresh, per project.
    // Falls back to the server-wide ChannelSessionTTL when empty.
    ChannelSessionTTL string `bson:"channel_session_ttl"`
    ```
  - In `NewProjectInfo`, add
    `ChannelSessionTTL: DefaultChannelSessionTTL.String(),` next to
    `ClientDeactivateThreshold`.
  - In `DeepCopy`, copy the new field.
  - In `UpdateFields`, add:
    ```go
    if fields.ChannelSessionTTL != nil {
        i.ChannelSessionTTL = *fields.ChannelSessionTTL
    }
    ```
  - In `ToProject`, set `ChannelSessionTTL: i.ChannelSessionTTL`.

- [ ] Write failing test in `server/backend/database/project_info_test.go`:
  ```go
  func TestNewProjectInfoSetsDefaultChannelSessionTTL(t *testing.T) {
      info := database.NewProjectInfo("p", types.ID("owner"))
      assert.Equal(t, database.DefaultChannelSessionTTL.String(), info.ChannelSessionTTL)
  }

  func TestUpdateFieldsAppliesChannelSessionTTL(t *testing.T) {
      info := database.NewProjectInfo("p", types.ID("owner"))
      v := "1m"
      info.UpdateFields(&types.UpdatableProjectFields{ChannelSessionTTL: &v})
      assert.Equal(t, "1m", info.ChannelSessionTTL)
  }
  ```
- [ ] Run: `go test ./server/backend/database/...`. Expected: FAIL
  (field does not exist on `UpdatableProjectFields` yet — proceeds
  through Task 3).

> **Note:** Task 2 leaves `go build` broken because
> `UpdatableProjectFields.ChannelSessionTTL` does not exist yet.
> Do not commit Task 2 alone — commit at the end of Task 3 so the
> tree stays green.

### Task 3: UpdatableProjectFields field + bounded validator (TDD)

- [ ] Write failing validator tests in
  `api/types/updatable_project_fields_test.go` (create if absent):
  ```go
  func TestChannelSessionTTLValidation(t *testing.T) {
      tests := []struct {
          name    string
          value   string
          wantErr bool
      }{
          {"valid 15s", "15s", false},
          {"valid 1m", "1m", false},
          {"valid 5m max", "5m", false},
          {"too short", "500ms", true},
          {"below 1s", "0s", true},
          {"above 5m", "10m", true},
          {"unparseable", "soon", true},
          {"empty", "", true},
      }
      for _, tc := range tests {
          t.Run(tc.name, func(t *testing.T) {
              v := tc.value
              f := &types.UpdatableProjectFields{ChannelSessionTTL: &v}
              err := f.Validate()
              if tc.wantErr {
                  assert.Error(t, err)
              } else {
                  assert.NoError(t, err)
              }
          })
      }
  }
  ```
- [ ] Run: `go test ./api/types/... -run TestChannelSessionTTL -v`.
  Expected: build error (field missing).

- [ ] In `api/types/updatable_project_fields.go`:
  - Add field on `UpdatableProjectFields` after
    `ClientDeactivateThreshold`:
    ```go
    // ChannelSessionTTL controls per-project presence-channel
    // session retention. Must parse as a Go duration and fall
    // within [1s, 5m].
    ChannelSessionTTL *string `bson:"channel_session_ttl,omitempty" validate:"omitempty,channel_session_ttl"`
    ```
  - Add `i.ChannelSessionTTL == nil` to the all-nil check in
    `Validate()` (the `ErrEmptyProjectFields` guard).
  - In the `init()` block, register a new validator after the
    `duration` registration:
    ```go
    if err := validation.RegisterValidation(
        "channel_session_ttl",
        func(level validation.FieldLevel) bool {
            d, err := time.ParseDuration(level.Field().String())
            if err != nil {
                return false
            }
            return d >= time.Second && d <= 5*time.Minute
        },
    ); err != nil {
        fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
        os.Exit(1)
    }
    if err := validation.RegisterTranslation(
        "channel_session_ttl",
        "given {0} must be a duration between 1s and 5m",
    ); err != nil {
        fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
        os.Exit(1)
    }
    ```
- [ ] Run: `go test ./api/types/... -run TestChannelSessionTTL -v`.
  Expected: PASS.
- [ ] Run: `go test ./server/backend/database/...`. Expected: PASS
  (Task 2 tests now compile).
- [ ] Commit Tasks 2+3 together:
  ```
  Wire ChannelSessionTTL field through project types and DB

  Adds the per-project ChannelSessionTTL field on ProjectInfo,
  UpdatableProjectFields, and api/types.Project with a bounded
  validator (1s..5m). New projects default to 15s, matching the
  current global default.
  ```

### Task 4: api/types.Project field + helper (TDD)

- [ ] Write failing test in `api/types/project_test.go`
  (create or extend):
  ```go
  func TestChannelSessionTTLAsTimeDuration(t *testing.T) {
      p := &types.Project{ChannelSessionTTL: "30s"}
      d, err := p.ChannelSessionTTLAsTimeDuration()
      assert.NoError(t, err)
      assert.Equal(t, 30*time.Second, d)

      p.ChannelSessionTTL = "garbage"
      _, err = p.ChannelSessionTTLAsTimeDuration()
      assert.ErrorIs(t, err, types.ErrInvalidTimeDurationString)
  }
  ```
- [ ] Run: `go test ./api/types/... -run TestChannelSessionTTL`.
  Expected: FAIL (helper missing).

- [ ] In `api/types/project.go`:
  - Add field on `Project`:
    ```go
    // ChannelSessionTTL controls how long a presence-channel
    // session is retained after its last refresh, per project.
    ChannelSessionTTL string `bson:"channel_session_ttl"`
    ```
  - Add helper after `ClientDeactivateThresholdAsTimeDuration`:
    ```go
    // ChannelSessionTTLAsTimeDuration converts ChannelSessionTTL
    // string to time.Duration.
    func (p *Project) ChannelSessionTTLAsTimeDuration() (time.Duration, error) {
        d, err := time.ParseDuration(p.ChannelSessionTTL)
        if err != nil {
            return 0, ErrInvalidTimeDurationString
        }
        return d, nil
    }
    ```
- [ ] Run: `go test ./api/types/... -run TestChannelSessionTTL`.
  Expected: PASS.

- [ ] Update converter:
  - In `api/converter/from_pb.go`, locate the `*api.Project` →
    `types.Project` mapping (function `FromProject` or similar)
    and add `ChannelSessionTTL: pbProject.ChannelSessionTtl`.
  - In `api/converter/to_pb.go`, locate the reverse mapping and add
    `ChannelSessionTtl: project.ChannelSessionTTL`.
  - In any helpers that convert `UpdatableProjectFields` ↔ proto
    `UpdatableProjectFields`, add the new field. Grep:
    `git grep -n "ClientDeactivateThreshold" api/converter/`.
- [ ] Run: `go build ./...`. Expected: success.
- [ ] Run: `go test ./api/... ./server/backend/database/...`.
  Expected: PASS.
- [ ] Commit:
  ```
  Expose ChannelSessionTTL on api/types.Project and converters

  Mirrors the new ProjectInfo field on api/types.Project, with a
  parse helper and proto<->types conversion in both directions.
  ```

### Task 5: Channel manager — per-channel TTL at cleanup time (TDD)

- [ ] Write failing test in
  `server/backend/channel/manager_test.go`. Use the existing test
  scaffolding pattern (if `manager_test.go` already constructs a
  `Manager` with a stub `database.Database`, extend it; otherwise
  create a minimal `stubDB` that returns a `*ProjectInfo` with
  `ChannelSessionTTL` set):
  ```go
  func TestCleanupExpiredUsesPerProjectTTL(t *testing.T) {
      // Two projects, different TTLs:
      // - project A: 1s — its sessions should expire after 1s.
      // - project B: 5s — its sessions should NOT expire after 1s.
      stubDB := newStubDB(map[types.ID]string{
          "projA": "1s",
          "projB": "5s",
      })
      mgr := channel.NewManager(noopPubSub, 15*time.Second,
          time.Second, nil, noopBroker, stubDB)

      ctxA := context.Background()
      keyA := types.ChannelRefKey{ProjectID: "projA", ChannelKey: "room/a"}
      keyB := types.ChannelRefKey{ProjectID: "projB", ChannelKey: "room/b"}
      sA, _, _ := mgr.Attach(ctxA, keyA, actorA)
      sB, _, _ := mgr.Attach(ctxA, keyB, actorB)
      _, _ = sA, sB

      // Advance: 2s elapsed. projA (1s TTL) expired, projB (5s) not.
      time.Sleep(2 * time.Second) // or inject a clock if one exists
      cleaned, err := mgr.CleanupExpired(ctxA)
      require.NoError(t, err)
      assert.Equal(t, 1, cleaned)
      assert.Equal(t, int64(0), mgr.SessionCount(keyA, false))
      assert.Equal(t, int64(1), mgr.SessionCount(keyB, false))
  }

  func TestCleanupExpiredFallsBackOnLookupError(t *testing.T) {
      stubDB := newErroringDB() // returns error from FindProjectInfoByID
      mgr := channel.NewManager(noopPubSub, 1*time.Second,
          time.Second, nil, noopBroker, stubDB)
      key := types.ChannelRefKey{ProjectID: "p", ChannelKey: "room/x"}
      _, _, _ = mgr.Attach(context.Background(), key, actorA)
      time.Sleep(2 * time.Second)
      cleaned, err := mgr.CleanupExpired(context.Background())
      require.NoError(t, err)
      assert.Equal(t, 1, cleaned, "should fall back to manager TTL (1s)")
  }

  func TestCleanupExpiredMemoizesPerProjectInOnePass(t *testing.T) {
      stubDB := &countingDB{ttl: "5s"}
      mgr := channel.NewManager(noopPubSub, 15*time.Second,
          time.Second, nil, noopBroker, stubDB)
      // 3 channels on the same project.
      for i := 0; i < 3; i++ {
          key := types.ChannelRefKey{
              ProjectID:  "p",
              ChannelKey: types.ChannelKey(fmt.Sprintf("room/%d", i)),
          }
          _, _, _ = mgr.Attach(context.Background(), key, types.NewActorID())
      }
      _, _ = mgr.CleanupExpired(context.Background())
      assert.Equal(t, 1, stubDB.lookupCalls,
          "FindProjectInfoByID called once per project per cleanup pass")
  }
  ```
  > If existing tests do not currently include a stub `database.Database`,
  > write a minimal one in the same file. Mirror the surface area the
  > manager actually uses (only `FindProjectInfoByID` matters here).

- [ ] Run: `go test ./server/backend/channel/... -run TestCleanupExpired`.
  Expected: FAIL (current code uses `m.sessionTTL` for every channel).

- [ ] In `server/backend/channel/manager.go`, modify
  `CleanupExpired` (around lines 453-486):
  ```go
  func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
      now := gotime.Now()
      cleanedCount := 0

      channelKeys := make([]types.ChannelRefKey, 0)
      m.channels.ForEach(func(ch *Channel) bool {
          if ch != nil {
              channelKeys = append(channelKeys, ch.Key)
          }
          return true
      })

      // Per-tick TTL memoization: each project's TTL is resolved
      // at most once per cleanup pass. The map is local to this
      // call so a project setting change is picked up on the next
      // tick.
      ttlByProject := make(map[types.ID]gotime.Duration)
      ttlFor := func(projectID types.ID) gotime.Duration {
          if d, ok := ttlByProject[projectID]; ok {
              return d
          }
          info, err := m.db.FindProjectInfoByID(ctx, projectID)
          if err != nil {
              logging.From(ctx).Warnf(
                  "cleanup TTL lookup %s: %v (using server default)",
                  projectID, err,
              )
              ttlByProject[projectID] = m.sessionTTL
              return m.sessionTTL
          }
          d, err := info.ToProject().ChannelSessionTTLAsTimeDuration()
          if err != nil || d <= 0 {
              ttlByProject[projectID] = m.sessionTTL
              return m.sessionTTL
          }
          ttlByProject[projectID] = d
          return d
      }

      for _, key := range channelKeys {
          ch := m.channels.Get(key)
          if !m.isValidChannel(ch) {
              continue
          }
          ttl := ttlFor(key.ProjectID)
          expiredSessionIDs := classifyExpiredSessions(now, ttl, ch.Sessions)
          cleanedCount += cleanUpExpiredSessions(ctx, m, expiredSessionIDs)
      }

      if m.metrics != nil && m.metrics.Metrics != nil {
          m.collectAndPublishMetrics(ctx)
      }

      return cleanedCount, nil
  }
  ```
- [ ] Run: `go test ./server/backend/channel/... -run TestCleanupExpired`.
  Expected: PASS.
- [ ] Run: `go test ./server/backend/channel/...` (entire package).
  Expected: PASS — no regression in existing channel tests.
- [ ] Commit:
  ```
  Resolve channel session TTL per project at cleanup time

  CleanupExpired now memoizes the TTL per ProjectID for the
  duration of one cleanup pass and looks it up via the same
  FindProjectInfoByID path used by collectAndPublishMetrics.
  The Manager's sessionTTL is retained as the default seed and
  the fallback when project lookup or parsing fails.
  ```

### Task 6: Full suite + lint

- [ ] `make lint`. Expected: clean.
- [ ] `go test ./...`. Expected: PASS.
- [ ] If MongoDB is running locally (`docker compose -f
  build/docker/docker-compose.yml up -d`), run `make test` for the
  integration suite. Expected: PASS.
- [ ] `git rebase origin/main`. Resolve any conflicts in
  `resources.proto` field numbers if `main` moved.

### Task 7: Self-review and PR

- [ ] Dispatch `superpowers:requesting-code-review` over the branch
  diff. Apply blocking findings; note non-blocking as comments.
- [ ] `git push -u origin feat/per-project-channel-session-ttl`.
- [ ] Open PR titled `Add per-project channel session TTL`
  (≤70 chars). Body: Summary (2-3 bullets) + Test plan.
- [ ] Link the design doc in the PR description.

### Task 8: Archive

- [ ] Write `20260528-per-project-channel-session-ttl-lessons.md`
  alongside this todo (key lessons, surprises).
- [ ] After merge, run from yorkie repo root:
  ```
  bash scripts/tasks-archive.sh
  bash scripts/tasks-index.sh
  ```

## Notes

- `m.sessionTTL` is intentionally not removed from the `Manager`. It
  is the fallback when (a) a project has no value yet, (b) parsing
  fails, or (c) `FindProjectInfoByID` errors. Removing it would
  force every cleanup tick into a hard failure on transient DB
  errors.
- The new `channel_session_ttl` validator is intentionally separate
  from the existing `duration` validator: `duration` checks parse
  only, while `channel_session_ttl` additionally enforces the
  `[1s, 5m]` bound. Reusing one validator name across both would
  silently widen acceptable input on the other duration fields.
- Cleanup interval (`m.cleanupInterval`) is **not** made per-project
  in this task. See the design doc Non-Goals section.

## Follow-up (separate repo)

After this PR merges, add the form field to the dashboard:

- Branch in `yorkie-team/dashboard`: `feat/channel-session-ttl-field`.
- File: `src/features/projects/Settings.tsx`. Add a text input under
  the resources group mirroring the `clientDeactivateThreshold`
  pattern (regex-validated, placeholder `15s`, help text explains
  the presence-count tradeoff and the 1s..5m bound).
- File: `src/api/index.ts` `updateProject`. Map
  `channelSessionTtl` → proto `channel_session_ttl`.
