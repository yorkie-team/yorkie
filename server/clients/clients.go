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

package clients

import (
	"context"
	"errors"
	"fmt"

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
	clientInfo, err := db.ActivateClient(ctx, project.ID, clientKey)
	if err != nil {
		return nil, fmt.Errorf("activate client: %w", err)
	}

	return clientInfo, nil
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
		return nil, fmt.Errorf("find client info by ID: %w", err)
	}

	for id, clientDocInfo := range clientInfo.Documents {
		isAttached, err := clientInfo.IsAttached(id)
		if err != nil {
			return nil, fmt.Errorf("check attached document: %w", err)
		}
		if !isAttached {
			continue
		}

		if err := clientInfo.DetachDocument(id); err != nil {
			return nil, fmt.Errorf("detach document: %w", err)
		}

		if err := db.UpdateSyncedSeq(
			ctx,
			clientInfo,
			id,
			clientDocInfo.ServerSeq,
		); err != nil {
			return nil, fmt.Errorf("update synced seq: %w", err)
		}
	}

	clientInfo, err = db.DeactivateClient(ctx, projectID, clientID)
	if err != nil {
		return nil, fmt.Errorf("deactivate client: %w", err)
	}

	return clientInfo, nil
}

// FindClientInfo finds the client with the given id.
func FindClientInfo(
	ctx context.Context,
	db database.Database,
	project *types.Project,
	clientID *time.ActorID,
) (*database.ClientInfo, error) {
	clientInfo, err := db.FindClientInfoByID(
		ctx,
		project.ID,
		types.IDFromActorID(clientID),
	)
	if err != nil {
		return nil, fmt.Errorf("find client info by ID: %w", err)
	}

	return clientInfo, err
}
