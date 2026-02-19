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
	lastServerSeq int64,
	lastDocID types.ID,
) (int64, types.ID, int, int, error) {
	locker, ok := be.Lockers.LockerWithTryLock(compactionKey)
	if !ok {
		return lastServerSeq, lastDocID, 0, 0, nil
	}
	defer locker.Unlock()

	resultServerSeq, resultDocID, candidates, err := FindCompactionCandidates(
		ctx,
		be,
		candidatesLimit,
		compactionMinChanges,
		lastServerSeq,
		lastDocID,
	)
	if err != nil {
		return lastServerSeq, lastDocID, 0, 0, err
	}

	// If no candidates found, reset to beginning for next cycle.
	if len(candidates) == 0 {
		return 0, database.ZeroID, 0, 0, nil
	}

	compactedCount := 0
	for _, pair := range candidates {
		compacted, err := CompactDocument(ctx, be, pair.Project, pair.Document)
		if err != nil {
			logging.From(ctx).Warnf("failed to compact document %s: %v", pair.Document.ID, err)
			continue
		}

		if compacted {
			compactedCount++
		}
	}

	return resultServerSeq, resultDocID, len(candidates), compactedCount, nil
}

// FindCompactionCandidates finds candidates to compact by directly querying documents.
func FindCompactionCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimit int,
	compactionMinChanges int,
	lastServerSeq int64,
	lastDocID types.ID,
) (int64, types.ID, []CandidatePairDoc, error) {
	candidates, resultServerSeq, resultDocID, err := be.DB.FindCompactionCandidates(
		ctx, candidatesLimit, compactionMinChanges, lastServerSeq, lastDocID)
	if err != nil {
		return 0, database.ZeroID, nil, err
	}

	var pairs []CandidatePairDoc
	for _, candidate := range candidates {
		info, err := be.DB.FindProjectInfoByID(ctx, candidate.ProjectID)
		if err != nil {
			return 0, database.ZeroID, nil, err
		}

		pairs = append(pairs, CandidatePairDoc{
			Project:  info.ToProject(),
			Document: candidate,
		})
	}

	return resultServerSeq, resultDocID, pairs, nil
}
