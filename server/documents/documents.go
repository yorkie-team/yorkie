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
	"errors"
	"fmt"
	"strconv"
	"strings"
	stdSync "sync"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/packs"

	yschema "github.com/yorkie-team/yorkie/pkg/schema"
)

// RootMaxLen is the maximum length of the document root in the
// document summary.
const RootMaxLen = 50

// pageSizeLimit is the limit of the pagination size of documents.
const pageSizeLimit = 101

// UpdateMode constants define the modes for updating a document
const (
	// UpdateModeBoth updates both the document root and schema
	UpdateModeBoth = "both"

	// UpdateModeRootOnly updates only the document root
	UpdateModeRootOnly = "root_only"

	// UpdateModeSchemaOnly updates only the document schema
	UpdateModeSchemaOnly = "schema_only"

	// UpdateModeDetachSchema detaches the schema from the document
	UpdateModeDetachSchema = "detach_schema"
)

// DocAttachmentKey generates a key for the document attachment.
func DocAttachmentKey(docKey types.DocRefKey) sync.Key {
	return sync.NewKey(fmt.Sprintf("doc-attachment-%s-%s", docKey.ProjectID, docKey.DocID))
}

// DocWatchStreamKey generates a key for watching document.
func DocWatchStreamKey(clientID time.ActorID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("doc-watchstream-%s-%s", clientID, docKey))
}

var (
	// ErrDocumentAttached is returned when the document is attached when
	// deleting the document.
	ErrDocumentAttached = errors.New("document is attached")

	// ErrDocumentAlreadyExists is returned when the document already exists.
	ErrDocumentAlreadyExists = errors.New("document already exists")
)

// CreateDocument creates a new document with the given key and server sequence.
func CreateDocument(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	userID types.ID,
	docKey key.Key,
	initialRoot yson.Object,
) (*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindOrCreateDocInfo(
		ctx,
		types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  userID,
		},
		docKey,
	)
	if err != nil {
		return nil, err
	}

	if docInfo.Owner != userID || docInfo.ServerSeq != 0 {
		return nil, fmt.Errorf("create document: %w", ErrDocumentAlreadyExists)
	}

	newDoc := document.New(docInfo.Key)
	if err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetYSON(initialRoot)
		return nil
	}); err != nil {
		return nil, err
	}

	if err = be.DB.CompactChangeInfos(
		ctx,
		docInfo,
		docInfo.ServerSeq,
		newDoc.CreateChangePack().Changes,
	); err != nil {
		return nil, err
	}

	return &types.DocumentSummary{
		ID:              docInfo.ID,
		Key:             docInfo.Key,
		AttachedClients: 0,
		CreatedAt:       docInfo.CreatedAt,
		AccessedAt:      docInfo.AccessedAt,
		UpdatedAt:       docInfo.UpdatedAt,
		Root:            newDoc.Marshal(),
		DocSize:         newDoc.DocSize(),
		SchemaKey:       docInfo.Schema,
	}, nil
}

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	paging types.Paging[types.ID],
	includeRoot bool,
) ([]*types.DocumentSummary, error) {
	paging.PageSize = min(paging.PageSize, pageSizeLimit)

	infos, err := be.DB.FindDocInfosByPaging(ctx, project.ID, paging)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, info := range infos {
		// TODO(hackerwins): Resolve the N+1 problem.
		clientInfos, err := be.DB.FindAttachedClientInfosByRefKey(ctx, info.RefKey())
		if err != nil {
			return nil, err
		}

		summary := &types.DocumentSummary{
			ID:              info.ID,
			Key:             info.Key,
			AttachedClients: len(clientInfos),
			CreatedAt:       info.CreatedAt,
			AccessedAt:      info.AccessedAt,
			UpdatedAt:       info.UpdatedAt,
			SchemaKey:       info.Schema,
		}

		if includeRoot {
			doc, err := packs.BuildInternalDocForServerSeq(ctx, be, info, info.ServerSeq)
			if err != nil {
				return nil, err
			}

			root := doc.Marshal()
			if len(root) > RootMaxLen {
				root = root[:RootMaxLen] + "..."
			}

			summary.Root = root
			summary.DocSize = doc.DocSize()
		}

		summaries = append(summaries, summary)
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
	info, err := be.DB.FindDocInfoByKey(ctx, project.ID, k)
	if err != nil {
		return nil, err
	}

	clientInfos, err := be.DB.FindAttachedClientInfosByRefKey(ctx, info.RefKey())
	if err != nil {
		return nil, err
	}

	doc, err := packs.BuildInternalDocForServerSeq(ctx, be, info, info.ServerSeq)
	if err != nil {
		return nil, err
	}

	return &types.DocumentSummary{
		ID:              info.ID,
		Key:             info.Key,
		AttachedClients: len(clientInfos),
		CreatedAt:       info.CreatedAt,
		AccessedAt:      info.AccessedAt,
		UpdatedAt:       info.UpdatedAt,
		Root:            doc.Marshal(),
		DocSize:         doc.DocSize(),
		SchemaKey:       info.Schema,
	}, nil
}

// GetDocumentSummaries returns a list of document summaries.
func GetDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	keys []key.Key,
	includeRoot bool,
	includePresences bool,
) ([]*types.DocumentSummary, error) {
	docInfos, err := be.DB.FindDocInfosByKeys(ctx, project.ID, keys)
	if err != nil {
		return nil, err
	}

	// Initialize all document summaries with basic info first
	summaries := make([]*types.DocumentSummary, len(docInfos))
	for i, docInfo := range docInfos {
		summaries[i] = &types.DocumentSummary{
			ID:         docInfo.ID,
			Key:        docInfo.Key,
			CreatedAt:  docInfo.CreatedAt,
			AccessedAt: docInfo.AccessedAt,
			UpdatedAt:  docInfo.UpdatedAt,
			SchemaKey:  docInfo.Schema,
			Root:       "",
			Presences:  nil,
		}
	}

	// If root or presences are needed, use cluster API to fill additional fields
	if includeRoot || includePresences {
		var wg stdSync.WaitGroup
		errChan := make(chan error, len(docInfos))

		// Launch goroutines for parallel cluster API calls
		for i, docInfo := range docInfos {
			wg.Add(1)
			go func(idx int, info *database.DocInfo) {
				defer wg.Done()

				// Call cluster API to get only snapshot/presence data
				summary, err := be.ClusterClient.GetDocument(
					ctx,
					project,
					info.Key.String(),
					includeRoot,
					includePresences,
				)

				if err != nil {
					errChan <- err
					return
				}

				if includeRoot {
					summaries[idx].Root = summary.Root
					summaries[idx].DocSize = summary.DocSize
				}
				if includePresences {
					summaries[idx].Presences = summary.Presences
				}
			}(i, docInfo)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(errChan)

		// Check for any errors
		for err := range errChan {
			if err != nil {
				return nil, err
			}
		}
	}

	return summaries, nil
}

// GetDocumentByServerSeq returns a document for the given server sequence.
func GetDocumentByServerSeq(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	k key.Key,
	serverSeq int64,
) (*document.InternalDocument, error) {
	docInfo, err := be.DB.FindOrCreateDocInfo(
		ctx,
		types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(time.InitialActorID),
		},
		k,
	)
	if err != nil {
		return nil, err
	}

	doc, err := packs.BuildInternalDocForServerSeq(ctx, be, docInfo, serverSeq)
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
			SchemaKey:  docInfo.Schema,
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

// FindDocInfoByRefKey returns a document for the given document refKey.
func FindDocInfoByRefKey(
	ctx context.Context,
	be *backend.Backend,
	refkey types.DocRefKey,
) (*database.DocInfo, error) {
	return be.DB.FindDocInfoByRefKey(ctx, refkey)
}

// FindOrCreateDocInfo returns a document for the given document key. If
// createDocIfNotExist is true, it creates a new document if it does not exist.
func FindOrCreateDocInfo(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docKey key.Key,
) (*database.DocInfo, error) {
	return be.DB.FindOrCreateDocInfo(
		ctx,
		clientInfo.RefKey(),
		docKey,
	)
}

// UpdateDocInfoSchema updates the schema key stored in DocInfo.
func UpdateDocInfoSchema(
	ctx context.Context,
	be *backend.Backend,
	refKey types.DocRefKey,
	schemaKey string,
) error {
	return be.DB.UpdateDocInfoSchema(ctx, refKey, schemaKey)
}

// UpdateDocument updates the given document with the given root.
// change pack.
func UpdateDocument(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docInfo *database.DocInfo,
	root yson.Object,
	schema *types.Schema,
	updateMode string,
) (*types.DocumentSummary, error) {
	clientInfo := &database.ClientInfo{
		ID:        types.IDFromActorID(time.InitialActorID),
		ProjectID: project.ID,
		Documents: map[types.ID]*database.ClientDocInfo{
			docInfo.ID: {
				Status:    database.DocumentAttached,
				ServerSeq: docInfo.ServerSeq,
				ClientSeq: 0,
			},
		},
	}

	doc, err := packs.BuildDocForCheckpoint(ctx, be, docInfo, change.Checkpoint{
		ServerSeq: docInfo.ServerSeq,
		ClientSeq: 0,
	}, time.InitialActorID)
	if err != nil {
		return nil, err
	}

	// 1. Update document root
	if updateMode == UpdateModeRootOnly || updateMode == UpdateModeBoth {
		if err = doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(root)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	// 2. Validate document root against schema rules
	if updateMode != UpdateModeDetachSchema && schema != nil {
		result := yschema.ValidateYorkieRuleset(doc.RootObject(), schema.Rules)
		if !result.Valid {
			var errorMessages []string
			for _, err := range result.Errors {
				errorMessages = append(errorMessages, err.Message)
			}
			return nil, fmt.Errorf("%w: %s", document.ErrSchemaValidationFailed, strings.Join(errorMessages, ", "))
		}
	}

	// 3. Push changes to the server
	if updateMode == UpdateModeRootOnly || updateMode == UpdateModeBoth {
		if _, err = packs.PushPull(
			ctx,
			be,
			project,
			clientInfo,
			docInfo.RefKey(),
			doc.CreateChangePack(),
			packs.PushPullOptions{
				Mode:   types.SyncModePushOnly,
				Status: document.StatusAttached,
			}); err != nil {
			return nil, err
		}
	}

	// 4. Update document schema
	if updateMode != UpdateModeRootOnly {
		docInfo.Schema = ""
		if updateMode != UpdateModeDetachSchema {
			docInfo.Schema = schema.Name + "@" + strconv.Itoa(schema.Version)
		}
		if err := UpdateDocInfoSchema(ctx, be, docInfo.RefKey(), docInfo.Schema); err != nil {
			return nil, err
		}
	}

	return &types.DocumentSummary{
		ID:              docInfo.ID,
		Key:             docInfo.Key,
		AttachedClients: 0,
		CreatedAt:       docInfo.CreatedAt,
		AccessedAt:      docInfo.AccessedAt,
		UpdatedAt:       docInfo.UpdatedAt,
		Root:            doc.Marshal(),
		DocSize:         doc.DocSize(),
		SchemaKey:       docInfo.Schema,
	}, nil
}

// RemoveDocument removes the given document. If force is false, it only removes
// the document if it is not attached to any client.
func RemoveDocument(
	ctx context.Context,
	be *backend.Backend,
	refKey types.DocRefKey,
	force bool,
) error {
	if force {
		return be.DB.UpdateDocInfoStatusToRemoved(ctx, refKey)
	}

	isAttached, err := be.DB.IsDocumentAttached(ctx, refKey, "")
	if err != nil {
		return err
	}
	if isAttached {
		return ErrDocumentAttached
	}

	return be.DB.UpdateDocInfoStatusToRemoved(ctx, refKey)
}

// IsDocumentAttached returns true if the given document is attached to any client.
func IsDocumentAttached(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	return be.DB.IsDocumentAttached(ctx, docRefKey, excludeClientID)
}

// FindAttachedClientCount returns the number of attached clients for the given document.
func FindAttachedClientCount(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
) (int, error) {
	infos, err := be.DB.FindAttachedClientInfosByRefKey(ctx, docRefKey)
	if err != nil {
		return 0, err
	}

	return len(infos), nil
}

// CompactDocument compacts the given document.
func CompactDocument(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	document *database.DocInfo,
) error {
	return be.ClusterClient.CompactDocument(ctx, project, document)
}

// PurgeDocument purges the given document.
func PurgeDocument(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	document *database.DocInfo,
) error {
	return be.ClusterClient.PurgeDocument(ctx, project, document)
}
