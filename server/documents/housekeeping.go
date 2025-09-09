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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	compactionKey = "housekeeping/compaction"
)

// CandidatePairDoc represents a pair of Project and Document.
type CandidatePairDoc struct {
	Project  *types.Project
	Document *database.DocInfo
}

// CompactDocuments compacts documents by removing old changes and creating
// a new initial change using direct document iteration.
func CompactDocuments(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimit int,
	compactionMinChanges int,
	lastDocID types.ID,
) (types.ID, int, int, error) {
	locker, ok := be.Lockers.LockerWithTryLock(compactionKey)
	if !ok {
		return lastDocID, 0, 0, nil
	}
	defer locker.Unlock()

	lastID, candidates, err := FindCompactionCandidates(
		ctx,
		be,
		candidatesLimit,
		compactionMinChanges,
		lastDocID,
	)
	if err != nil {
		return lastDocID, 0, 0, err
	}

	// If no candidates found, reset to beginning for next cycle.
	if len(candidates) == 0 {
		return database.ZeroID, 0, 0, nil
	}

	compactedCount := 0
	for _, pair := range candidates {
		if err := CompactDocument(ctx, be, pair.Project, pair.Document); err != nil {
			logging.From(ctx).Warnf("failed to compact document %s: %v", pair.Document.ID, err)
			continue
		}
		compactedCount++
	}

	return lastID, len(candidates), compactedCount, nil
}

// FindCompactionCandidates finds candidates to compact by directly querying documents.
func FindCompactionCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimit int,
	compactionMinChanges int,
	lastDocID types.ID,
) (types.ID, []CandidatePairDoc, error) {
	candidates, lastID, err := be.DB.FindCompactionCandidates(ctx, candidatesLimit, compactionMinChanges, lastDocID)
	if err != nil {
		return database.ZeroID, nil, err
	}

	var pairs []CandidatePairDoc
	for _, candidate := range candidates {
		// TODO(hackerwins): Consider caching projects in DB layer.
		project, ok := be.Cache.Project.Get(candidate.ProjectID.String())
		if !ok {
			projectInfo, err := be.DB.FindProjectInfoByID(ctx, candidate.ProjectID)
			if err != nil {
				return database.ZeroID, nil, err
			}

			project = projectInfo.ToProject()
			be.Cache.Project.Add(candidate.ProjectID.String(), project)
		}

		pairs = append(pairs, CandidatePairDoc{
			Project:  project,
			Document: candidate,
		})
	}

	return lastID, pairs, nil
}
