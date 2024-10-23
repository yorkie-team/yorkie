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
	"github.com/yorkie-team/yorkie/cluster"
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
) (*database.ClientInfo, error) {
	return be.DB.ActivateClient(ctx, project.ID, clientKey)
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

	// TODO(hackerwins): Introduce cluster client pool.
	// - https://connectrpc.com/docs/go/deployment/
	cli, err := cluster.Dial(be.Config.GatewayAddr)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

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

		if err := cli.DetachDocument(ctx, project, actorID, docID, project.PublicKey, docInfo.Key); err != nil {
			return nil, err
		}

		if err := be.DB.UpdateVersionVector(
			ctx,
			info,
			types.DocRefKey{
				ProjectID: refKey.ProjectID,
				DocID:     docID,
			},
			nil,
		); err != nil {
			return nil, err
		}
	}

	return be.DB.DeactivateClient(ctx, refKey)
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
