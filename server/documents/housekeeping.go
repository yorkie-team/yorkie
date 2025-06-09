/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package documents

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	compactionCandidatesKey = "housekeeping/compactionCandidates"
)

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

	locker := be.Lockers.Locker(compactionCandidatesKey)
	defer locker.Unlock()

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

// CandidatePair represents a pair of Project and Document.
type CandidatePair struct {
	Project  *database.ProjectInfo
	Document *database.DocInfo
}

// FindCompactionCandidates finds candidates to compact from the database.
func FindCompactionCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimitPerProject int,
	projectFetchSize int,
	compactionMinChanges int,
	lastProjectID types.ID,
) (types.ID, []CandidatePair, error) {
	projectInfos, err := be.DB.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []CandidatePair
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
			candidates = append(candidates, CandidatePair{
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
