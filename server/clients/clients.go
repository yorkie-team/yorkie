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
	// TODO(hackerwins): We need to remove the presence of the client from the document.
	// Be careful that housekeeping is executed by the leader. And documents are sharded
	// by the servers in the cluster. So, we need to consider the case where the leader is
	// not the same as the server that handles the document.

	// TODO(raararaara): When deactivating a client, we need to update three DB properties
	// (ClientInfo.Status, ClientInfo.Documents, SyncedSeq) in DB.
	// Updating the sub-properties of ClientInfo guarantees atomicity as it involves a single MongoDB document.
	// However, SyncedSeqs are stored in separate documents, so we can't ensure atomic updates for both.
	// Currently, if SyncedSeqs update fails, it mainly impacts GC efficiency without causing major issues.
	// We need to consider implementing a correction logic to remove SyncedSeqs in the future.
	clientInfo, err := db.DeactivateClient(ctx, refKey)
	if err != nil {
		return nil, err
	}

	// TODO(raararaara): We're currently updating SyncedSeq one by one. This approach is similar
	// to n+1 query problem. We need to investigate if we can optimize this process by using a single query in the future.
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

	return clientInfo, err
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
