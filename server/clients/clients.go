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
	"github.com/yorkie-team/yorkie/system"
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
	project *types.Project,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	info, err := FindActiveClientInfo(ctx, db, refKey)
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): Inject gatewayAddr
	systemClient, err := system.Dial("localhost:8080")
	if err != nil {
		return nil, err
	}

	for docID, clientDocInfo := range info.Documents {
		// TODO(hackerwins): Solve N+1
		if clientDocInfo.Status == database.DocumentDetached {
			continue
		}

		docInfo, err := db.FindDocInfoByRefKey(ctx, types.DocRefKey{
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

		if err := systemClient.DetachDocument(ctx, project.ID, actorID, docID, project.PublicKey, docInfo.Key); err != nil {
			return nil, err
		}
	}

	info, err = db.DeactivateClient(ctx, refKey)
	if err != nil {
		return nil, err
	}

	return info, err
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
