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
	compactionCandidatesKey = "housekeeping/compactionCandidates"
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

	locker, err := be.Locker.NewLocker(ctx, deactivateCandidatesKey)
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
	for _, pair := range candidates {
		if _, err := Deactivate(ctx, be, pair.Project.ToProject(), pair.Client.RefKey()); err != nil {
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

// CompactDocuments compacts documents by removing old changes and creating
// a new initial change.
func CompactDocuments(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	compactionMinChanges int,
	lastCompactionProjectID types.ID,
) (types.ID, error) {
	start := time.Now()

	locker, err := be.Locker.NewLocker(ctx, compactionCandidatesKey)
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

	lastProjectID, candidates, err := FindCompactionCandidates(
		ctx,
		be,
		candidatesLimitPerProject,
		projectFetchSize,
		compactionMinChanges,
		lastCompactionProjectID,
	)
	if err != nil {
		return database.DefaultProjectID, err
	}

	compactedCount := 0
	for _, pair := range candidates {
		if err := CompactDocument(ctx, be, pair.Project.ToProject(), pair.Document); err != nil {
			continue
		}
		compactedCount++
	}

	if len(candidates) > 0 {
		logging.From(ctx).Infof(
			"HSKP: candidates %d, compacted %d, %s",
			len(candidates),
			compactedCount,
			time.Since(start),
		)
	}

	return lastProjectID, nil
}

// ClientCandidatePair represents a pair of Project and Client.
type ClientCandidatePair struct {
	Project *database.ProjectInfo
	Client  *database.ClientInfo
}

// DocumentCandidatePair represents a pair of Project and Document.
type DocumentCandidatePair struct {
	Project  *database.ProjectInfo
	Document *database.DocInfo
}

// FindDeactivateCandidates finds candidates to deactivate from the database.
func FindDeactivateCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	lastProjectID types.ID,
) (types.ID, []ClientCandidatePair, error) {
	projectInfos, err := be.DB.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []ClientCandidatePair
	for _, projectInfo := range projectInfos {
		infos, err := be.DB.FindDeactivateCandidatesPerProject(ctx, projectInfo, candidatesLimitPerProject)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		for _, info := range infos {
			candidates = append(candidates, ClientCandidatePair{
				Project: projectInfo,
				Client:  info,
			})
		}
	}

	var topProjectID types.ID
	if len(projectInfos) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projectInfos[len(projectInfos)-1].ID
	}

	return topProjectID, candidates, nil
}

// FindCompactionCandidates finds candidates to compact from the database.
func FindCompactionCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	compactionMinChanges int,
	lastProjectID types.ID,
) (types.ID, []DocumentCandidatePair, error) {
	projectInfos, err := be.DB.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []DocumentCandidatePair
	for _, projectInfo := range projectInfos {
		infos, err := be.DB.FindCompactionCandidatesPerProject(
			ctx,
			projectInfo,
			candidatesLimitPerProject,
			compactionMinChanges,
		)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		for _, info := range infos {
			candidates = append(candidates, DocumentCandidatePair{
				Project:  projectInfo,
				Document: info,
			})
		}
	}

	var topProjectID types.ID
	if len(projectInfos) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projectInfos[len(projectInfos)-1].ID
	}

	return topProjectID, candidates, nil
}
