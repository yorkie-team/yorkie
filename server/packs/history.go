/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package packs

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// FindChanges fetches changes of the given document.
func FindChanges(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	from int64,
	to int64,
) ([]*change.Change, error) {
	docRefKey := docInfo.RefKey()
	if be.Config.SnapshotWithPurgingChanges {
		minSyncedSeqInfo, err := be.DB.FindMinSyncedSeqInfo(ctx, docRefKey)
		if err != nil {
			return nil, err
		}

		snapshotInfo, err := be.DB.FindClosestSnapshotInfo(
			ctx,
			docRefKey,
			minSyncedSeqInfo.ServerSeq+be.Config.SnapshotInterval,
			false,
		)
		if err != nil {
			return nil, err
		}

		if snapshotInfo != nil && from < snapshotInfo.ServerSeq+1 {
			from = snapshotInfo.ServerSeq + 1
		}
	}

	changes, err := be.DB.FindChangesBetweenServerSeqs(
		ctx,
		docRefKey,
		from,
		to,
	)
	return changes, err
}
