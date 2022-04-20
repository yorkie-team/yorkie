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

package documents

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/packs"
)

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	previousID db.ID,
	pageSize int,
) ([]*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindDocInfosByPreviousIDAndPageSize(ctx, previousID, pageSize)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, docInfo := range docInfo {
		doc, err := packs.BuildDocumentForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
		if err != nil {
			return nil, err
		}

		summaries = append(summaries, &types.DocumentSummary{
			ID:       docInfo.ID.String(),
			Key:      doc.Key(),
			Snapshot: doc.Marshal(),
		})
	}

	return summaries, nil
}
