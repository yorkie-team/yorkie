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

package projects_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/projects"
)

const testOwnerID = types.ID("000000000000000000000000")

func newTestBackend(t *testing.T) *backend.Backend {
	db, err := memory.New()
	assert.NoError(t, err)

	return &backend.Backend{
		DB:      db,
		Lockers: sync.New(),
	}
}

func TestRefreshStats(t *testing.T) {
	ctx := context.Background()
	be := newTestBackend(t)

	// Create 2 projects.
	p1, err := be.DB.CreateProjectInfo(ctx, fmt.Sprintf("%s-p1", t.Name()), testOwnerID)
	assert.NoError(t, err)
	p2, err := be.DB.CreateProjectInfo(ctx, fmt.Sprintf("%s-p2", t.Name()), testOwnerID)
	assert.NoError(t, err)

	before := time.Now()
	lastID, processed, err := projects.RefreshStats(ctx, be, 10, database.ZeroID)
	after := time.Now()
	assert.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.NotEqual(t, database.ZeroID, lastID)

	// Verify each project's stats were written.
	for _, info := range []*database.ProjectInfo{p1, p2} {
		counts, err := be.DB.GetProjectStatsCounts(ctx, info.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), counts.ClientsCount)
		assert.Equal(t, int64(0), counts.DocumentsCount)
		assert.False(t, counts.UpdatedAt.IsZero(), "UpdatedAt should be non-zero")
		assert.False(t, counts.UpdatedAt.Before(before.Add(-time.Second)),
			"UpdatedAt %s should not predate refresh start %s", counts.UpdatedAt, before)
		assert.False(t, counts.UpdatedAt.After(after.Add(time.Second)),
			"UpdatedAt %s should not postdate refresh end %s", counts.UpdatedAt, after)
	}
}

func TestRefreshStatsPaginationWrap(t *testing.T) {
	ctx := context.Background()
	be := newTestBackend(t)

	// Create 3 projects.
	for i := 0; i < 3; i++ {
		_, err := be.DB.CreateProjectInfo(ctx, fmt.Sprintf("%s-p%d", t.Name(), i), testOwnerID)
		assert.NoError(t, err)
	}

	// Page 1: limit 2 from ZeroID → 2 processed.
	lastID1, processed1, err := projects.RefreshStats(ctx, be, 2, database.ZeroID)
	assert.NoError(t, err)
	assert.Equal(t, 2, processed1)
	assert.NotEqual(t, database.ZeroID, lastID1)

	// Page 2: limit 2 from lastID1 → 1 processed.
	lastID2, processed2, err := projects.RefreshStats(ctx, be, 2, lastID1)
	assert.NoError(t, err)
	assert.Equal(t, 1, processed2)
	assert.NotEqual(t, database.ZeroID, lastID2)

	// Page 3: limit 2 from lastID2 → 0 processed, wraps to ZeroID.
	lastID3, processed3, err := projects.RefreshStats(ctx, be, 2, lastID2)
	assert.NoError(t, err)
	assert.Equal(t, 0, processed3)
	assert.Equal(t, database.ZeroID, lastID3)
}
