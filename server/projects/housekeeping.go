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
//
// The cursor advances to the tail of the fetched batch even when individual
// projects fail (errors are logged and the project is skipped), so a single
// bad project cannot stall subsequent refreshes.
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
