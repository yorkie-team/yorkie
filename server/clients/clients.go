/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package clients provides the client related business logic.
package clients

import (
	"context"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrInvalidClientKey is returned when the given Key is not valid ClientKey.
	ErrInvalidClientKey = errors.InvalidArgument("invalid client key").WithCode("ErrInvalidClientKey")

	// ErrInvalidClientID is returned when the given Key is not valid ClientID.
	ErrInvalidClientID = errors.InvalidArgument("invalid client id").WithCode("ErrInvalidClientID")
)

// Activate activates the given client.
func Activate(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientKey string,
	metadata map[string]string,
) (*database.ClientInfo, error) {
	return be.DB.ActivateClient(ctx, project.ID, clientKey, metadata)
}

// Deactivate deactivates the given client.
func Deactivate(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	// NOTE(hackerwins): In cluster using ConsistentHashing, document-specific
	// requests are assigned to particular servers. ClientInfo is cached per
	// server, which can lead to a situation where server executing deactivation
	// may not have the complete attachment information in its cache.
	// To ensure all attached documents are properly detached during client
	// deactivation, we skip the cache and fetch ClientInfo directly from DB.
	info, err := FindActiveClientInfo(ctx, be, refKey, true)
	if err != nil {
		return nil, err
	}

	docIDs := make([]types.ID, 0)
	for docID, clientDocInfo := range info.Documents {
		if clientDocInfo.Status != database.DocumentAttached &&
			clientDocInfo.Status != database.DocumentAttaching {
			continue
		}
		docIDs = append(docIDs, docID)
	}

	docInfos, err := be.DB.FindDocInfosByIDs(ctx, project.ID, docIDs)
	if err != nil {
		return nil, err
	}

	if len(docInfos) != len(docIDs) {
		return nil, fmt.Errorf("find documents for detachment, expected: %d, actual: %d",
			len(docIDs), len(docInfos))
	}

	actorID, err := info.ID.ToActorID()
	if err != nil {
		return nil, err
	}

	for _, info := range docInfos {
		if err := be.ClusterClient.DetachDocument(
			ctx,
			project,
			actorID,
			info.ID,
			info.Key,
		); err != nil {
			return nil, err
		}
	}

	return be.DB.DeactivateClient(ctx, refKey)
}

// DeactivateAsync deactivates the given client asynchronously.
// This function is designed to handle browser window close scenarios
// where the original context might be cancelled. It creates a new
// background context to ensure the deactivation process completes
// even if the original request context is cancelled.
func DeactivateAsync(
	be *backend.Backend,
	project *types.Project,
	refKey types.ClientRefKey,
) error {
	be.Background.AttachGoroutine(func(ctx context.Context) {
		if _, err := Deactivate(ctx, be, project, refKey); err != nil {
			logging.LogError(ctx, fmt.Errorf("deactivate client asynchronously: %w", err))
		}
	}, "deactivation")

	return nil
}

// AttachDocument attaches the given document to the client.
func AttachDocument(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	isAttached bool,
) (*database.ClientInfo, error) {
	// NOTE(kokodak): Reattaching a document that has been detached is not allowed.
	// This check is necessary because TryAttaching does not validate this case.
	// If reattaching is allowed in the future, this IsAlreadyDetached check should be removed.
	// For more details on reattachment context, see:
	// https://github.com/yorkie-team/yorkie-js-sdk/pull/996#issuecomment-3023329082
	if clientInfo.IsAlreadyDetached(docInfo.ID, isAttached) {
		return nil, fmt.Errorf("client(%s) attaches %s: %w",
			clientInfo.ID, docInfo.ID, database.ErrDocumentAlreadyDetached)
	}

	if !clientInfo.IsAttaching(docInfo.ID) {
		var err error
		clientInfo, err = be.DB.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		if err != nil {
			return nil, err
		}
	}

	if err := clientInfo.AttachDocument(docInfo.ID, isAttached); err != nil {
		return nil, err
	}

	return clientInfo, nil
}

// FindActiveClientInfo find the active client info by the given ref key.
func FindActiveClientInfo(
	ctx context.Context,
	be *backend.Backend,
	refKey types.ClientRefKey,
	skipCache ...bool,
) (*database.ClientInfo, error) {
	info, err := be.DB.FindClientInfoByRefKey(ctx, refKey, skipCache...)
	if err != nil {
		return nil, err
	}

	if err := info.EnsureActivated(); err != nil {
		return nil, err
	}

	return info, nil
}
