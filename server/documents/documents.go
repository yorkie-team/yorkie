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

// Package documents provides the document related business logic.
package documents

import (
	"context"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
)

// SnapshotMaxLen is the maximum length of the document snapshot in the
// document summary.
const SnapshotMaxLen = 50

// pageSizeLimit is the limit of the pagination size of documents.
const pageSizeLimit = 101

var (
	// ErrDocumentAttached is returned when the document is attached when
	// deleting the document.
	ErrDocumentAttached = fmt.Errorf("document is attached")
)

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	paging types.Paging[types.ID],
) ([]*types.DocumentSummary, error) {
	if paging.PageSize > pageSizeLimit {
		paging.PageSize = pageSizeLimit
	}

	docInfo, err := be.DB.FindDocInfosByPaging(ctx, project.ID, paging)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, docInfo := range docInfo {
		doc, err := packs.BuildDocumentForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
		if err != nil {
			return nil, err
		}

		snapshot := doc.Marshal()
		if len(snapshot) > SnapshotMaxLen {
			snapshot = snapshot[:SnapshotMaxLen] + "..."
		}

		summaries = append(summaries, &types.DocumentSummary{
			ID:         docInfo.ID,
			Key:        docInfo.Key,
			CreatedAt:  docInfo.CreatedAt,
			AccessedAt: docInfo.AccessedAt,
			UpdatedAt:  docInfo.UpdatedAt,
			Snapshot:   snapshot,
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
	serverSeq int64,
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

// SearchDocumentSummaries returns document summaries that match the query parameters.
func SearchDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	query string,
	pageSize int,
) (*types.SearchResult[*types.DocumentSummary], error) {
	res, err := be.DB.FindDocInfosByQuery(ctx, project.ID, query, pageSize)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, docInfo := range res.Elements {
		summaries = append(summaries, &types.DocumentSummary{
			ID:         docInfo.ID,
			Key:        docInfo.Key,
			CreatedAt:  docInfo.CreatedAt,
			AccessedAt: docInfo.AccessedAt,
			UpdatedAt:  docInfo.UpdatedAt,
		})
	}

	return &types.SearchResult[*types.DocumentSummary]{
		TotalCount: res.TotalCount,
		Elements:   summaries,
	}, nil
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

// FindDocInfo returns a document for the given document ID.
func FindDocInfo(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docID types.ID,
) (*database.DocInfo, error) {
	return be.DB.FindDocInfoByID(ctx, project.ID, docID)
}

// FindDocInfoByKeyAndOwner returns a document for the given document key. If
// createDocIfNotExist is true, it creates a new document if it does not exist.
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

// RemoveDocument removes the given document. If force is false, it only removes
// the document if it is not attached to any client.
func RemoveDocument(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docID types.ID,
	force bool,
) error {
	if force {
		return be.DB.UpdateDocInfoStatusToRemoved(ctx, project.ID, docID)
	}

	isAttached, err := be.DB.IsDocumentAttached(ctx, project.ID, docID, "")
	if err != nil {
		return err
	}
	if isAttached {
		return ErrDocumentAttached
	}

	return be.DB.UpdateDocInfoStatusToRemoved(ctx, project.ID, docID)
}

// IsDocumentAttached returns true if the given document is attached to any client.
func IsDocumentAttached(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docID types.ID,
	excludeClientID types.ID,
) (bool, error) {
	return be.DB.IsDocumentAttached(ctx, project.ID, docID, excludeClientID)
}
