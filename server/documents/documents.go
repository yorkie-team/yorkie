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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
)

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	paging types.Paging,
) ([]*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindDocInfosByPaging(ctx, paging)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, docInfo := range docInfo {
		summaries = append(summaries, &types.DocumentSummary{
			ID:         docInfo.ID,
			Key:        docInfo.Key,
			CreatedAt:  docInfo.CreatedAt,
			AccessedAt: docInfo.AccessedAt,
			UpdatedAt:  docInfo.UpdatedAt,
		})
	}

	return summaries, nil
}

// GetDocumentSummary returns a document summary.
func GetDocumentSummary(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
) (*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindDocInfoByID(ctx, id)
	if err != nil {
		return nil, err
	}

	doc, err := packs.BuildDocumentForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		return nil, err
	}

	return &types.DocumentSummary{
		ID:         docInfo.ID,
		Key:        docInfo.Key,
		CreatedAt:  docInfo.CreatedAt,
		AccessedAt: docInfo.AccessedAt,
		UpdatedAt:  docInfo.UpdatedAt,
		Snapshot:   doc.Marshal(),
	}, nil
}

// FindDocInfo returns a document for the given document key and owner.
func FindDocInfo(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientInfo *database.ClientInfo,
	docKey key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	docInfo, err := be.DB.FindDocInfoByKey(
		ctx,
		project.ID,
		clientInfo.ID,
		docKey,
		createDocIfNotExist,
	)
	if err != nil {
		return nil, err
	}
	return docInfo, nil
}
