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
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
	projectID types.ID,
	clientID types.ID,
) (*database.ClientInfo, error) {
	clientInfo, err := db.FindClientInfoByID(
		ctx,
		projectID,
		clientID,
	)
	if err != nil {
		return nil, err
	}

	for docKey, v := range clientInfo.Documents {
		for docID, clientDocInfo := range v {
			isAttached, err := clientInfo.IsAttached(docKey, docID)
			if err != nil {
				return nil, err
			}
			if !isAttached {
				continue
			}

			if err := clientInfo.DetachDocument(docKey, docID); err != nil {
				return nil, err
			}

			// TODO(hackerwins): We need to remove the presence of the client from the document.
			// Be careful that housekeeping is executed by the leader. And documents are sharded
			// by the servers in the cluster. So, we need to consider the case where the leader is
			// not the same as the server that handles the document.

			if err := db.UpdateSyncedSeq(
				ctx,
				clientInfo,
				docKey,
				docID,
				clientDocInfo.ServerSeq,
			); err != nil {
				return nil, err
			}
		}
	}

	return db.DeactivateClient(ctx, projectID, clientID)
}

// FindClientInfo finds the client with the given id.
func FindClientInfo(
	ctx context.Context,
	db database.Database,
	project *types.Project,
	clientID *time.ActorID,
) (*database.ClientInfo, error) {
	return db.FindClientInfoByID(
		ctx,
		project.ID,
		types.IDFromActorID(clientID),
	)
}
