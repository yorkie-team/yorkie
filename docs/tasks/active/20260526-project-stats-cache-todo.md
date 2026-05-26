**Created**: 2026-05-26

# Project Stats Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache `clients_count` and `documents_count` on the project document, refreshed asynchronously by a new housekeeping task, so `GetProjectStats` returns within the dashboard's 3s deadline regardless of collection size.

**Architecture:** Add `stats_clients_count`, `stats_documents_count`, `stats_updated_at` fields on `ProjectInfo`. A leader-only housekeeping task paginates over projects every 5 minutes, runs `CountDocuments` against MongoDB secondary, and updates each project's stats fields. `GetProjectStats` reads cached values via a projection-only `FindOne` that bypasses `ProjectCache`.

**Tech Stack:** Go, MongoDB driver, Protocol Buffers, gocron (existing scheduler)

**Spec:** `docs/design/project-stats-cache.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `server/backend/database/project_info.go` | Add `StatsClientsCount`, `StatsDocumentsCount`, `StatsUpdatedAt` fields; update `DeepCopy`; add `ProjectStatsCounts` value type |
| Modify | `server/backend/database/database.go` | Add interface methods; remove `GetClientsCount`/`GetDocumentsCount` |
| Modify | `server/backend/database/mongo/client.go` | Implement new methods; remove old |
| Modify | `server/backend/database/memory/database.go` | Implement new methods; remove old |
| Modify | `server/backend/database/memory/database_test.go` | Update tests covering removed methods |
| Modify | `server/projects/projects.go` | Switch `GetProjectStats` to use cached counts |
| Create | `server/projects/housekeeping.go` | `RefreshStats` task entry point |
| Modify | `server/backend/housekeeping/config.go` | Add `ProjectStatsRefreshInterval` field + parser + validation |
| Modify | `server/config.go` | Add `DefaultProjectStatsRefreshInterval` and propagation |
| Modify | `server/server.go` | Register the new housekeeping task with its own state |
| Modify | `api/yorkie/v1/admin.proto` | Add `stats_updated_at = 16` to `GetProjectStatsResponse` |
| Modify | `api/types/project_stats.go` | Add `StatsUpdatedAt` to `ProjectStats` struct |
| Modify | `server/rpc/admin_server.go` | Wire `StatsUpdatedAt` from response into proto |
| Create | `server/projects/housekeeping_test.go` | Unit tests for refresh flow against memory DB |
| Create | `test/integration/project_stats_test.go` | Integration test against MongoDB |

---

### Task 1: Add `ProjectInfo` stats fields and `ProjectStatsCounts` type

**Files:**
- Modify: `server/backend/database/project_info.go`

- [ ] **Step 1: Add fields to `ProjectInfo`**

Insert after `UpdatedAt` field (around line 142):

```go
// StatsClientsCount is the cached count of activated clients in the project.
// Refreshed periodically by the project-stats housekeeping task.
StatsClientsCount int64 `bson:"stats_clients_count"`

// StatsDocumentsCount is the cached count of non-removed documents in the project.
// Refreshed periodically by the project-stats housekeeping task.
StatsDocumentsCount int64 `bson:"stats_documents_count"`

// StatsUpdatedAt is the time when the stats fields above were last refreshed.
// Zero value indicates the refresh has not run yet for this project.
StatsUpdatedAt time.Time `bson:"stats_updated_at"`
```

- [ ] **Step 2: Update `DeepCopy` to copy new fields**

Add inside the `return &ProjectInfo{...}` block:

```go
StatsClientsCount:           i.StatsClientsCount,
StatsDocumentsCount:         i.StatsDocumentsCount,
StatsUpdatedAt:              i.StatsUpdatedAt,
```

- [ ] **Step 3: Add `ProjectStatsCounts` value type at end of file**

```go
// ProjectStatsCounts holds the cached count values for GetProjectStats.
type ProjectStatsCounts struct {
	ClientsCount   int64
	DocumentsCount int64
	UpdatedAt      time.Time
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add server/backend/database/project_info.go
git commit -m "Add stats fields to ProjectInfo for cached project counts"
```

---

### Task 2: Update database interface

**Files:**
- Modify: `server/backend/database/database.go`

- [ ] **Step 1: Remove old method signatures**

Delete the `GetDocumentsCount` and `GetClientsCount` lines (around 282–286):

```go
// GetDocumentsCount returns the number of documents in the given project.
GetDocumentsCount(ctx context.Context, projectID types.ID) (int64, error)

// GetClientsCount returns the number of active clients in the given project.
GetClientsCount(ctx context.Context, projectID types.ID) (int64, error)
```

- [ ] **Step 2: Add new method signatures in the same location**

```go
// GetProjectStatsCounts returns the cached project counts (clients, documents)
// stored on the project document. Bypasses ProjectCache.
GetProjectStatsCounts(ctx context.Context, projectID types.ID) (*ProjectStatsCounts, error)

// UpdateProjectStats writes the cached stats fields on the project document.
UpdateProjectStats(
	ctx context.Context,
	projectID types.ID,
	clientsCount int64,
	documentsCount int64,
	updatedAt time.Time,
) error

// CountActivatedClients counts clients with status = activated for the given
// project. Slow on large collections; used by the project-stats refresh task only.
CountActivatedClients(ctx context.Context, projectID types.ID) (int64, error)

// CountAliveDocuments counts non-removed documents for the given project.
// Slow on large collections; used by the project-stats refresh task only.
CountAliveDocuments(ctx context.Context, projectID types.ID) (int64, error)

// FindProjectInfosForRefresh returns up to `limit` project infos with `_id > lastID`,
// ordered by `_id` ascending. Used by housekeeping tasks that need to iterate
// across all projects.
FindProjectInfosForRefresh(
	ctx context.Context,
	limit int,
	lastID types.ID,
) ([]*ProjectInfo, types.ID, error)
```

- [ ] **Step 3: Verify build fails (interface unimplemented)**

Run: `go build ./...`
Expected: build errors in `mongo/client.go` and `memory/database.go` ("does not implement Database").

- [ ] **Step 4: Commit (intentional broken build for next task to fix)**

Hold off committing until Tasks 3 and 4 land. Continue.

---

### Task 3: Implement methods in memory backend

**Files:**
- Modify: `server/backend/database/memory/database.go`

- [ ] **Step 1: Write failing tests in `server/backend/database/memory/database_test.go`**

Add to the test file:

```go
func TestProjectStatsCounts(t *testing.T) {
	t.Run("returns zeros when project has no stats fields", func(t *testing.T) {
		db, _ := memory.New()
		info, err := db.CreateProjectInfo(ctx, "p1", testOwner, "24h")
		assert.NoError(t, err)

		counts, err := db.GetProjectStatsCounts(ctx, info.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), counts.ClientsCount)
		assert.Equal(t, int64(0), counts.DocumentsCount)
		assert.True(t, counts.UpdatedAt.IsZero())
	})

	t.Run("returns updated values after UpdateProjectStats", func(t *testing.T) {
		db, _ := memory.New()
		info, err := db.CreateProjectInfo(ctx, "p2", testOwner, "24h")
		assert.NoError(t, err)

		now := time.Now()
		assert.NoError(t, db.UpdateProjectStats(ctx, info.ID, 100, 50, now))

		counts, err := db.GetProjectStatsCounts(ctx, info.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(100), counts.ClientsCount)
		assert.Equal(t, int64(50), counts.DocumentsCount)
		assert.WithinDuration(t, now, counts.UpdatedAt, time.Millisecond)
	})
}

func TestFindProjectInfosForRefresh(t *testing.T) {
	db, _ := memory.New()
	var ids []types.ID
	for i := 0; i < 5; i++ {
		info, err := db.CreateProjectInfo(ctx, fmt.Sprintf("p%d", i), testOwner, "24h")
		assert.NoError(t, err)
		ids = append(ids, info.ID)
	}

	page1, lastID, err := db.FindProjectInfosForRefresh(ctx, 3, database.ZeroID)
	assert.NoError(t, err)
	assert.Len(t, page1, 3)

	page2, _, err := db.FindProjectInfosForRefresh(ctx, 3, lastID)
	assert.NoError(t, err)
	assert.Len(t, page2, 2)
}
```

Adjust `testOwner` and helper imports if existing patterns differ; copy from existing tests in the same file.

- [ ] **Step 2: Run tests to confirm failure**

Run: `go test ./server/backend/database/memory/... -run 'TestProjectStatsCounts|TestFindProjectInfosForRefresh' -v`
Expected: compile errors (methods undefined).

- [ ] **Step 3: Remove old `GetClientsCount` and `GetDocumentsCount` (lines 1642–1687)**

Delete both methods entirely.

- [ ] **Step 4: Implement new methods**

Append:

```go
// GetProjectStatsCounts returns the cached project counts stored on the project info.
func (d *DB) GetProjectStatsCounts(
	_ context.Context,
	projectID types.ID,
) (*database.ProjectStatsCounts, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", projectID.String())
	if err != nil {
		return nil, fmt.Errorf("find project %s: %w", projectID, err)
	}
	if raw == nil {
		return &database.ProjectStatsCounts{}, nil
	}
	info := raw.(*database.ProjectInfo)
	return &database.ProjectStatsCounts{
		ClientsCount:   info.StatsClientsCount,
		DocumentsCount: info.StatsDocumentsCount,
		UpdatedAt:      info.StatsUpdatedAt,
	}, nil
}

// UpdateProjectStats writes the cached stats fields on the project document.
func (d *DB) UpdateProjectStats(
	_ context.Context,
	projectID types.ID,
	clientsCount int64,
	documentsCount int64,
	updatedAt time.Time,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", projectID.String())
	if err != nil {
		return fmt.Errorf("find project %s: %w", projectID, err)
	}
	if raw == nil {
		return database.ErrProjectNotFound
	}
	info := raw.(*database.ProjectInfo).DeepCopy()
	info.StatsClientsCount = clientsCount
	info.StatsDocumentsCount = documentsCount
	info.StatsUpdatedAt = updatedAt

	if err := txn.Insert(tblProjects, info); err != nil {
		return fmt.Errorf("update project stats %s: %w", projectID, err)
	}
	txn.Commit()
	return nil
}

// CountActivatedClients counts clients with status = activated for the given project.
func (d *DB) CountActivatedClients(
	_ context.Context,
	projectID types.ID,
) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("count clients of %s: %w", projectID, err)
	}

	var count int64
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		if raw.(*database.ClientInfo).Status != database.ClientActivated {
			continue
		}
		count++
	}
	return count, nil
}

// CountAliveDocuments counts non-removed documents for the given project.
func (d *DB) CountAliveDocuments(
	_ context.Context,
	projectID types.ID,
) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblDocuments, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("count documents of %s: %w", projectID, err)
	}

	var count int64
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		if !raw.(*database.DocInfo).RemovedAt.IsZero() {
			continue
		}
		count++
	}
	return count, nil
}

// FindProjectInfosForRefresh returns up to `limit` project infos with `_id > lastID`,
// ordered by ID ascending.
func (d *DB) FindProjectInfosForRefresh(
	_ context.Context,
	limit int,
	lastID types.ID,
) ([]*database.ProjectInfo, types.ID, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(tblProjects, "id", lastID.String())
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("list projects for refresh: %w", err)
	}

	var infos []*database.ProjectInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ProjectInfo)
		if info.ID == lastID {
			continue
		}
		infos = append(infos, info.DeepCopy())
		if len(infos) >= limit {
			break
		}
	}

	tail := database.ZeroID
	if len(infos) > 0 {
		tail = infos[len(infos)-1].ID
	}
	return infos, tail, nil
}
```

- [ ] **Step 5: Update existing tests that referenced the removed methods**

Find references with: `grep -rn 'GetClientsCount\|GetDocumentsCount' server/backend/database/memory/`. Replace with `CountActivatedClients` / `CountAliveDocuments` where the test was exercising the count semantic, or delete obsolete tests that only covered the old API.

- [ ] **Step 6: Run tests**

Run: `go test ./server/backend/database/memory/... -v`
Expected: all pass, including new tests.

- [ ] **Step 7: Commit**

```bash
git add server/backend/database/database.go server/backend/database/memory/
git commit -m "Add project stats methods to database interface and memory backend"
```

---

### Task 4: Implement methods in MongoDB backend

**Files:**
- Modify: `server/backend/database/mongo/client.go`

- [ ] **Step 1: Remove old `GetClientsCount` and `GetDocumentsCount` (lines 1658–1687)**

Delete both methods entirely.

- [ ] **Step 2: Add new method implementations**

Append:

```go
// GetProjectStatsCounts returns the cached project counts via a projection-only
// FindOne. ProjectCache is intentionally bypassed to avoid compounding staleness.
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

// UpdateProjectStats writes the cached stats fields on the project document.
func (c *Client) UpdateProjectStats(
	ctx context.Context,
	projectID types.ID,
	clientsCount int64,
	documentsCount int64,
	updatedAt time.Time,
) error {
	_, err := c.collection(ColProjects).UpdateOne(
		ctx,
		bson.M{"_id": projectID},
		bson.M{"$set": bson.M{
			"stats_clients_count":   clientsCount,
			"stats_documents_count": documentsCount,
			"stats_updated_at":      updatedAt,
		}},
	)
	if err != nil {
		return fmt.Errorf("update project stats %s: %w", projectID, err)
	}
	return nil
}

// CountActivatedClients counts clients with status = activated for the given project.
// Uses secondary read preference to keep load off the primary.
func (c *Client) CountActivatedClients(
	ctx context.Context,
	projectID types.ID,
) (int64, error) {
	count, err := c.collection(ColClients).CountDocuments(
		ctx,
		bson.M{
			"project_id": projectID,
			StatusKey:    database.ClientActivated,
		},
		options.Count().SetReadPreference(readpref.SecondaryPreferred()),
	)
	if err != nil {
		return 0, fmt.Errorf("count activated clients of %s: %w", projectID, err)
	}
	return count, nil
}

// CountAliveDocuments counts non-removed documents for the given project.
// Uses secondary read preference to keep load off the primary.
func (c *Client) CountAliveDocuments(
	ctx context.Context,
	projectID types.ID,
) (int64, error) {
	count, err := c.collection(ColDocuments).CountDocuments(
		ctx,
		bson.M{
			"project_id": projectID,
			"removed_at": bson.M{"$exists": false},
		},
		options.Count().SetReadPreference(readpref.SecondaryPreferred()),
	)
	if err != nil {
		return 0, fmt.Errorf("count alive documents of %s: %w", projectID, err)
	}
	return count, nil
}

// FindProjectInfosForRefresh returns up to `limit` project infos with `_id > lastID`,
// ordered by `_id` ascending.
func (c *Client) FindProjectInfosForRefresh(
	ctx context.Context,
	limit int,
	lastID types.ID,
) ([]*database.ProjectInfo, types.ID, error) {
	cursor, err := c.collection(ColProjects).Find(
		ctx,
		bson.M{"_id": bson.M{"$gt": lastID}},
		options.Find().SetSort(bson.M{"_id": 1}).SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("find projects for refresh: %w", err)
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, database.ZeroID, fmt.Errorf("decode projects for refresh: %w", err)
	}

	tail := database.ZeroID
	if len(infos) > 0 {
		tail = infos[len(infos)-1].ID
	}
	return infos, tail, nil
}
```

- [ ] **Step 3: Add missing imports**

Ensure `client.go` imports the following (some may already be present):

```go
"errors"
"go.mongodb.org/mongo-driver/v2/mongo"
"go.mongodb.org/mongo-driver/v2/mongo/options"
"go.mongodb.org/mongo-driver/v2/mongo/readpref"
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 5: Run mongo integration smoke test**

Run: `go test -tags integration ./server/backend/database/mongo/... -v -run 'TestProject' -count=1`
Expected: existing project tests still pass.

- [ ] **Step 6: Commit**

```bash
git add server/backend/database/mongo/client.go
git commit -m "Implement project stats methods in MongoDB backend"
```

---

### Task 5: Switch `GetProjectStats` to read cached counts

**Files:**
- Modify: `server/projects/projects.go`

- [ ] **Step 1: Replace the slow read sites**

Around lines 199–207, replace:

```go
documentsCount, err := be.DB.GetDocumentsCount(ctx, id)
if err != nil {
	return nil, err
}

clientsCount, err := be.DB.GetClientsCount(ctx, id)
if err != nil {
	return nil, err
}
```

With:

```go
counts, err := be.DB.GetProjectStatsCounts(ctx, id)
if err != nil {
	return nil, err
}
```

- [ ] **Step 2: Update the return value at the end of `GetProjectStats`**

Replace `DocumentsCount: documentsCount,` and `ClientsCount: clientsCount,` with:

```go
DocumentsCount:   counts.DocumentsCount,
ClientsCount:     counts.ClientsCount,
StatsUpdatedAt:   counts.UpdatedAt,
```

(The new `StatsUpdatedAt` field is added in Task 7.)

- [ ] **Step 3: Verify build fails (StatsUpdatedAt undefined)**

Run: `go build ./...`
Expected: error about `StatsUpdatedAt` not defined on `types.ProjectStats`. This is intentional and resolved in Task 7.

- [ ] **Step 4: Do not commit yet**

Hold this change; finalize after Tasks 6–7 are landed.

---

### Task 6: Add `stats_updated_at` to admin proto and regenerate

**Files:**
- Modify: `api/yorkie/v1/admin.proto`
- Regenerated: `api/yorkie/v1/admin.pb.go`, `api/yorkie/v1/admin.connect.go`

- [ ] **Step 1: Edit the proto**

In `GetProjectStatsResponse`, append a new field after `active_clients = 15`:

```proto
google.protobuf.Timestamp stats_updated_at = 16;
```

If `google/protobuf/timestamp.proto` is not already imported at the top, add:

```proto
import "google/protobuf/timestamp.proto";
```

- [ ] **Step 2: Regenerate**

Run: `make proto`
Expected: `api/yorkie/v1/admin.pb.go` updated with `StatsUpdatedAt *timestamppb.Timestamp`.

- [ ] **Step 3: Commit**

```bash
git add api/yorkie/v1/admin.proto api/yorkie/v1/admin.pb.go api/yorkie/v1/admin.connect.go
git commit -m "Add stats_updated_at to GetProjectStatsResponse"
```

---

### Task 7: Add `StatsUpdatedAt` to `types.ProjectStats` and admin server wiring

**Files:**
- Modify: `api/types/project_stats.go`
- Modify: `server/rpc/admin_server.go`

- [ ] **Step 1: Add field to `ProjectStats`**

Append to the `ProjectStats` struct in `api/types/project_stats.go`:

```go
// StatsUpdatedAt is the time when the cached counts (DocumentsCount, ClientsCount)
// were last refreshed by the project-stats housekeeping task. Zero indicates
// the refresh has not run yet.
StatsUpdatedAt time.Time `json:"stats_updated_at"`
```

- [ ] **Step 2: Wire the field in `admin_server.go::GetProjectStats`**

Locate the response construction (around line 270). Add `StatsUpdatedAt: timestamppb.New(stats.StatsUpdatedAt)` to the proto response, where the other fields are mapped.

If the existing handler uses a converter, find the converter and add the mapping there instead. Look for: `grep -n 'ClientsCount\|DocumentsCount' server/rpc/admin_server.go api/converter/`.

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: clean build. Task 5 changes now also compile.

- [ ] **Step 4: Commit**

```bash
git add api/types/project_stats.go server/projects/projects.go server/rpc/admin_server.go
git commit -m "Surface stats_updated_at through GetProjectStats response"
```

---

### Task 8: Add housekeeping config for refresh interval

**Files:**
- Modify: `server/backend/housekeeping/config.go`
- Modify: `server/config.go`

- [ ] **Step 1: Add field to `housekeeping.Config`**

In `server/backend/housekeeping/config.go`, add next to existing `Interval`:

```go
// ProjectStatsRefreshInterval is the interval between project-stats refresh cycles.
// If empty or "0s", the project-stats refresh task is not registered.
ProjectStatsRefreshInterval string `yaml:"ProjectStatsRefreshInterval"`
```

- [ ] **Step 2: Add parser method**

In the same file, mirroring the existing `ParseInterval`:

```go
// ParseProjectStatsRefreshInterval parses the configured interval. Returns 0 if empty.
func (c *Config) ParseProjectStatsRefreshInterval() (time.Duration, error) {
	if c.ProjectStatsRefreshInterval == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.ProjectStatsRefreshInterval)
	if err != nil {
		return 0, fmt.Errorf(
			`invalid argument %s for "--housekeeping-project-stats-refresh-interval" flag: %w`,
			c.ProjectStatsRefreshInterval, err,
		)
	}
	return d, nil
}
```

- [ ] **Step 3: Validate in `Validate`**

If the existing `Validate` method has logic for `Interval`, mirror it for the new field (only validate if non-empty).

- [ ] **Step 4: Add default in `server/config.go`**

Find `DefaultHousekeepingInterval = 30 * time.Second` (line 45) and add adjacent:

```go
DefaultProjectStatsRefreshInterval = 5 * time.Minute
```

Propagate the default in `EnsureDefaults` (around line 231) and `NewConfig` (around line 390). Mirror the existing `Interval` pattern.

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add server/backend/housekeeping/config.go server/config.go
git commit -m "Add ProjectStatsRefreshInterval housekeeping config"
```

---

### Task 9: Implement `RefreshStats` task

**Files:**
- Create: `server/projects/housekeeping.go`

- [ ] **Step 1: Write the new file**

```go
/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package projects provides project-related operations.
package projects

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	statsRefreshKey = "housekeeping/project-stats-refresh"
)

// RefreshStats walks projects in cursor order, counts activated clients and
// non-removed documents per project against the MongoDB secondary, and writes
// the values back as cached stats on the project document.
//
// Returns the new cursor position and the number of projects processed.
func RefreshStats(
	ctx context.Context,
	be *backend.Backend,
	limit int,
	lastProjectID types.ID,
) (types.ID, int, error) {
	locker, ok := be.Lockers.LockerWithTryLock(statsRefreshKey)
	if !ok {
		return lastProjectID, 0, nil
	}
	defer locker.Unlock()

	projects, tailID, err := be.DB.FindProjectInfosForRefresh(ctx, limit, lastProjectID)
	if err != nil {
		return lastProjectID, 0, err
	}

	if len(projects) == 0 {
		return database.ZeroID, 0, nil
	}

	processed := 0
	for _, p := range projects {
		cc, err := be.DB.CountActivatedClients(ctx, p.ID)
		if err != nil {
			logging.From(ctx).Warnf("count activated clients %s: %v", p.ID, err)
			continue
		}
		dc, err := be.DB.CountAliveDocuments(ctx, p.ID)
		if err != nil {
			logging.From(ctx).Warnf("count alive documents %s: %v", p.ID, err)
			continue
		}
		if err := be.DB.UpdateProjectStats(ctx, p.ID, cc, dc, time.Now()); err != nil {
			logging.From(ctx).Warnf("update project stats %s: %v", p.ID, err)
			continue
		}
		processed++
	}

	return tailID, processed, nil
}
```

- [ ] **Step 2: Write unit test in `server/projects/housekeeping_test.go`**

```go
/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * ... (full Apache header)
 */

package projects_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRefreshStats(t *testing.T) {
	ctx := context.Background()
	be := helper.NewBackendWithMemoryDB(t)  // use existing helper; substitute matching pattern

	// Seed two projects.
	info1, err := be.DB.CreateProjectInfo(ctx, "p1", helper.TestOwner, "24h")
	assert.NoError(t, err)
	info2, err := be.DB.CreateProjectInfo(ctx, "p2", helper.TestOwner, "24h")
	assert.NoError(t, err)

	// Run refresh.
	lastID, processed, err := projects.RefreshStats(ctx, be, 10, database.ZeroID)
	assert.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.NotEqual(t, database.ZeroID, lastID)

	// Verify stats fields populated (counts will be 0 since no clients/docs).
	counts1, err := be.DB.GetProjectStatsCounts(ctx, info1.ID)
	assert.NoError(t, err)
	assert.False(t, counts1.UpdatedAt.IsZero())
	assert.WithinDuration(t, time.Now(), counts1.UpdatedAt, 5*time.Second)

	counts2, err := be.DB.GetProjectStatsCounts(ctx, info2.ID)
	assert.NoError(t, err)
	assert.False(t, counts2.UpdatedAt.IsZero())
}

func TestRefreshStatsPaginationWrap(t *testing.T) {
	ctx := context.Background()
	be := helper.NewBackendWithMemoryDB(t)

	for i := 0; i < 3; i++ {
		_, err := be.DB.CreateProjectInfo(ctx, fmt.Sprintf("p%d", i), helper.TestOwner, "24h")
		assert.NoError(t, err)
	}

	// First call returns 2, second returns 1, third wraps to ZeroID with 0 processed.
	lastID, processed, err := projects.RefreshStats(ctx, be, 2, database.ZeroID)
	assert.NoError(t, err)
	assert.Equal(t, 2, processed)

	lastID, processed, err = projects.RefreshStats(ctx, be, 2, lastID)
	assert.NoError(t, err)
	assert.Equal(t, 1, processed)

	_, processed, err = projects.RefreshStats(ctx, be, 2, lastID)
	assert.NoError(t, err)
	assert.Equal(t, 0, processed)
}
```

If `helper.NewBackendWithMemoryDB` does not exist, inspect existing tests in `server/projects/` for the pattern they use to bootstrap a backend with in-memory DB and follow it.

- [ ] **Step 3: Run tests**

Run: `go test ./server/projects/... -v -run 'TestRefreshStats'`
Expected: pass.

- [ ] **Step 4: Commit**

```bash
git add server/projects/housekeeping.go server/projects/housekeeping_test.go
git commit -m "Add RefreshStats housekeeping task"
```

---

### Task 10: Register `RefreshStats` in `server.go`

**Files:**
- Modify: `server/server.go`

- [ ] **Step 1: Add a refresh-state field**

Inside `RegisterHousekeepingTasks` (around line 211), alongside `deactivateState` and `compactionState`, add:

```go
statsState := &housekeepingState{lastID: database.ZeroID}
```

- [ ] **Step 2: Register the new task at end of `RegisterHousekeepingTasks`, before the final `return nil`**

```go
statsInterval, err := be.Housekeeping.Config.ParseProjectStatsRefreshInterval()
if err != nil {
	return err
}
if statsInterval > 0 {
	if err := be.Housekeeping.RegisterTask(statsInterval, func(ctx context.Context) error {
		statsState.Lock()
		currentLastID := statsState.lastID
		statsState.Unlock()

		isNewTerm := currentLastID == database.ZeroID
		start := time.Now()

		lastID, processed, err := projects.RefreshStats(
			ctx,
			be,
			be.Housekeeping.Config.CandidatesLimit,
			currentLastID,
		)
		if err != nil {
			return err
		}

		statsState.Lock()
		if isNewTerm {
			statsState.term++
		}
		statsState.lastID = lastID
		statsState.totalProcessed += processed
		if processed > 0 {
			logging.From(ctx).Infof(
				"HSKP: project-stats #%d %s refreshed %d/%d %s",
				statsState.term,
				currentLastID,
				processed,
				statsState.totalProcessed,
				time.Since(start),
			)
		}
		statsState.Unlock()

		return nil
	}); err != nil {
		return err
	}
}
```

Add `"github.com/yorkie-team/yorkie/server/projects"` import if not already present.

- [ ] **Step 3: Verify build and lint**

Run: `make lint && make test`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add server/server.go
git commit -m "Register project-stats refresh as a housekeeping task"
```

---

### Task 11: Integration test against MongoDB

**Files:**
- Create: `test/integration/project_stats_test.go`

- [ ] **Step 1: Write the integration test**

Pattern after an existing test in `test/integration/` that exercises housekeeping (look at `housekeeping_test.go` or similar).

```go
//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 * ... (full Apache header)
 */

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestProjectStatsRefresh(t *testing.T) {
	ctx := context.Background()

	be := helper.NewBackend(t)  // existing helper that returns a real-MongoDB backend
	defer be.Close()

	// Create project + a couple of activated clients + a non-removed document.
	info, err := be.DB.CreateProjectInfo(ctx, helper.TestProjectName(t), helper.TestOwner, "24h")
	assert.NoError(t, err)

	for i := 0; i < 3; i++ {
		_, err := be.DB.ActivateClient(ctx, info.ID, helper.TestClientKey(t, i), nil)
		assert.NoError(t, err)
	}
	_, err = helper.CreateTestDocument(ctx, be, info.ID, helper.TestDocKey(t))
	assert.NoError(t, err)

	// Before refresh: cached counts are zero.
	pre, err := be.DB.GetProjectStatsCounts(ctx, info.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), pre.ClientsCount)
	assert.Equal(t, int64(0), pre.DocumentsCount)
	assert.True(t, pre.UpdatedAt.IsZero())

	// Run refresh.
	_, processed, err := projects.RefreshStats(ctx, be, 10, database.ZeroID)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, processed, 1)

	// After refresh: cached counts populated.
	post, err := be.DB.GetProjectStatsCounts(ctx, info.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), post.ClientsCount)
	assert.Equal(t, int64(1), post.DocumentsCount)
	assert.WithinDuration(t, time.Now(), post.UpdatedAt, 30*time.Second)
}
```

Helper calls (`helper.NewBackend`, `helper.TestProjectName`, `helper.CreateTestDocument`, etc.) should be matched against actual existing helpers — adjust naming to what `test/helper/` exposes.

- [ ] **Step 2: Run integration tests**

Run: `make test -tags integration` (requires local MongoDB)
Expected: new test passes.

- [ ] **Step 3: Commit**

```bash
git add test/integration/project_stats_test.go
git commit -m "Add integration test for project-stats refresh"
```

---

### Task 12: Final lint + full test sweep

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: clean.

- [ ] **Step 2: Run unit tests**

Run: `make test`
Expected: clean.

- [ ] **Step 3: Run integration tests**

Run: `make test -tags integration`
Expected: clean.

- [ ] **Step 4: Manual smoke**

Start a local server with the new config:

```yaml
Housekeeping:
  Interval: "30s"
  CandidatesLimit: 500
  ProjectStatsRefreshInterval: "30s"  # tight for smoke
```

Activate a couple of clients, create a doc, wait one refresh cycle, and call `GetProjectStats` — confirm counts and `StatsUpdatedAt` are populated.

- [ ] **Step 5: Move task to lessons (after PR merge)**

Once the PR is merged:

```bash
./scripts/tasks-archive.sh
```

This moves the todo+lessons pair to `docs/tasks/archive/2026/05/`. Author the lessons file beforehand if there's anything worth capturing (slow-count behavior on secondary, refresh skew under leader transitions, etc.).
