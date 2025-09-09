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
	compactionKey = "housekeeping/compaction"
)

// CandidatePairDoc represents a pair of Project and Document.
type CandidatePairDoc struct {
	Project  *database.ProjectInfo
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
) (types.ID, error) {
	locker, ok := be.Lockers.LockerWithTryLock(compactionKey)
	if !ok {
		return database.ZeroID, nil
	}
	defer locker.Unlock()

	start := time.Now()
	lastID, candidates, err := FindCompactionCandidates(
		ctx,
		be,
		candidatesLimit,
		compactionMinChanges,
		lastDocID,
	)
	if err != nil {
		return database.ZeroID, err
	}

	compactedCount := 0
	for _, pair := range candidates {
		if err := CompactDocument(ctx, be, pair.Project.ToProject(), pair.Document); err != nil {
			logging.From(ctx).Warnf("failed to compact document %s: %v", pair.Document.ID, err)
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

	return lastID, nil
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
		// Get project info for each candidate
		projectInfo, err := be.DB.FindProjectInfoByID(ctx, candidate.ProjectID)
		if err != nil {
			logging.From(ctx).Warnf("failed to find project %s: %v", candidate.ProjectID, err)
			continue
		}

		pairs = append(pairs, CandidatePairDoc{
			Project:  projectInfo,
			Document: candidate,
		})
	}

	return lastID, pairs, nil
}
