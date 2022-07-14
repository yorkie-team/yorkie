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
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
)

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	paging types.Paging[types.ID],
) ([]*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindDocInfosByPaging(ctx, project.ID, paging)
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
	project *types.Project,
	k key.Key,
) (*types.DocumentSummary, error) {
	// TODO(hackerwins): Split FindDocInfoByKeyAndOwner into upsert and find.
	docInfo, err := be.DB.FindDocInfoByKeyAndOwner(
		ctx,
		project.ID,
		types.IDFromActorID(time.InitialActorID),
		k,
		false,
	)
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

// GetDocumentByServerSeq returns a document for the given server sequence.
func GetDocumentByServerSeq(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	k key.Key,
	serverSeq uint64,
) (*document.InternalDocument, error) {
	docInfo, err := be.DB.FindDocInfoByKeyAndOwner(
		ctx,
		project.ID,
		types.IDFromActorID(time.InitialActorID),
		k,
		false,
	)
	if err != nil {
		return nil, err
	}

	doc, err := packs.BuildDocumentForServerSeq(ctx, be, docInfo, serverSeq)
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// FindDocInfoByKey returns a document for the given document key.
func FindDocInfoByKey(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docKey key.Key,
) (*database.DocInfo, error) {
	return be.DB.FindDocInfoByKey(
		ctx,
		project.ID,
		docKey,
	)
}

// FindDocInfoByKeyAndOwner returns a document for the given document key and owner.
func FindDocInfoByKeyAndOwner(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientInfo *database.ClientInfo,
	docKey key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	return be.DB.FindDocInfoByKeyAndOwner(
		ctx,
		project.ID,
		clientInfo.ID,
		docKey,
		createDocIfNotExist,
	)
}
