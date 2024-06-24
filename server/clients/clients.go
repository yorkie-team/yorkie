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

	"github.com/yorkie-team/yorkie/api/types"
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
	db database.Database,
	project *types.Project,
	clientKey string,
) (*database.ClientInfo, error) {
	return db.ActivateClient(ctx, project.ID, clientKey)
}

// Deactivate deactivates the given client.
func Deactivate(
	ctx context.Context,
	db database.Database,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	clientInfo, err := db.FindClientInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, err
	}

	// 01. Detach documents from client.
	for docID := range clientInfo.Documents {
		// 01-1. Check if given document is attached.
		isAttached, err := clientInfo.IsAttached(docID)
		if err != nil {
			return nil, err
		}
		if !isAttached {
			continue
		}

		docInfo, err := db.FindDocInfoByRefKey(ctx, types.DocRefKey{
			ProjectID: refKey.ProjectID,
			DocID:     docID,
		})
		if err != nil {
			return nil, err
		}

		// 01-2. Modify status of given document.
		if err := clientInfo.DetachDocument(docID); err != nil {
			return nil, err
		}

		// 01-3. Update DB
		if err := db.UpdateClientDocumentInfo(ctx, clientInfo, docInfo); err != nil {
			return nil, err
		}
	}
	updatedClientInfo, err := db.DeactivateClient(ctx, refKey)
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): We need to remove the presence of the client from the document.
	// Be careful that housekeeping is executed by the leader. And documents are sharded
	// by the servers in the cluster. So, we need to consider the case where the leader is
	// not the same as the server that handles the document.

	// 02. Update syncedSeq if
	for docID, clientDocInfo := range clientInfo.Documents {
		if err := db.UpdateSyncedSeq(
			ctx,
			clientInfo,
			types.DocRefKey{
				ProjectID: refKey.ProjectID,
				DocID:     docID,
			},
			clientDocInfo.ServerSeq,
		); err != nil {
			return nil, err
		}
	}

	return updatedClientInfo, err
}

// FindActiveClientInfo find the active client info by the given ref key.
func FindActiveClientInfo(
	ctx context.Context,
	db database.Database,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	info, err := db.FindClientInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, err
	}

	if err := info.EnsureActivated(); err != nil {
		return nil, err
	}

	return info, nil
}
