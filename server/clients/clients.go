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
	"errors"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	// ErrInvalidClientKey is returned when the given Key is not valid ClientKey.
	ErrInvalidClientKey = errors.New("invalid client key")

	// ErrInvalidClientID is returned when the given Key is not valid ClientID.
	ErrInvalidClientID = errors.New("invalid client id")
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
	info, err := FindActiveClientInfo(ctx, be, refKey)
	if err != nil {
		return nil, err
	}

	for docID, clientDocInfo := range info.Documents {
		if clientDocInfo.Status != database.DocumentAttached {
			continue
		}

		// TODO(hackerwins): Solve N+1
		docInfo, err := be.DB.FindDocInfoByRefKey(ctx, types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		})
		if err != nil {
			return nil, err
		}

		actorID, err := info.ID.ToActorID()
		if err != nil {
			return nil, err
		}

		if err := be.ClusterClient.DetachDocument(
			ctx,
			project,
			actorID,
			docID,
			docInfo.Key,
		); err != nil {
			return nil, err
		}
	}

	return be.DB.DeactivateClient(ctx, refKey)
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
) (*database.ClientInfo, error) {
	info, err := be.DB.FindClientInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, err
	}

	if err := info.EnsureActivated(); err != nil {
		return nil, err
	}

	return info, nil
}
