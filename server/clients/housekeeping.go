/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package clients

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	deactivateCandidatesKey = "housekeeping/deactivateCandidates"
)

// DeactivateInactives deactivates clients that have not been active for a
// long time.
func DeactivateInactives(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	housekeepingLastProjectID types.ID,
) (types.ID, error) {
	start := time.Now()

	locker, err := be.Coordinator.NewLocker(ctx, deactivateCandidatesKey)
	if err != nil {
		return database.DefaultProjectID, err
	}

	if err := locker.Lock(ctx); err != nil {
		return database.DefaultProjectID, err
	}

	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.From(ctx).Error(err)
		}
	}()

	lastProjectID, candidates, err := FindDeactivateCandidates(
		ctx,
		be,
		candidatesLimitPerProject,
		projectFetchSize,
		housekeepingLastProjectID,
	)
	if err != nil {
		return database.DefaultProjectID, err
	}

	deactivatedCount := 0
	for _, clientInfo := range candidates {
		if _, err := Deactivate(ctx, be.DB, clientInfo.RefKey()); err != nil {
			return database.DefaultProjectID, err
		}

		deactivatedCount++
	}

	if len(candidates) > 0 {
		logging.From(ctx).Infof(
			"HSKP: candidates %d, deactivated %d, %s",
			len(candidates),
			deactivatedCount,
			time.Since(start),
		)
	}

	return lastProjectID, nil
}

// FindDeactivateCandidates finds candidates to deactivate from the database.
func FindDeactivateCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	lastProjectID types.ID,
) (types.ID, []*database.ClientInfo, error) {
	projects, err := be.DB.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []*database.ClientInfo
	for _, project := range projects {
		infos, err := be.DB.FindDeactivateCandidatesPerProject(ctx, project, candidatesLimitPerProject)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		candidates = append(candidates, infos...)
	}

	var topProjectID types.ID
	if len(projects) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projects[len(projects)-1].ID
	}

	return topProjectID, candidates, nil
}
