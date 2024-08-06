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
	"os"
	"reflect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
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
	be *backend.Backend,
	refKey types.ClientRefKey,
	rpcAddr string,
) (*database.ClientInfo, error) {
	// NOTE(hackerwins): Before deactivating the client, we need to detach all
	// attached documents from the client.
	// Because detachments and deactivation are separate steps, failure of steps
	// must be considered. If each step of detachments is failed, some documents
	// are still attached and the client is not deactivated. In this case,
	// the client or housekeeping process should retry the deactivation.
	apiHost := os.Getenv("GATEWAY_HOST")
	if apiHost == "" {
		apiHost = rpcAddr
	}

	clientInfo, err := be.DB.FindClientInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, err
	}

	// 01. Detach attached documents from the client.
	actorID, err := clientInfo.ID.ToActorID()
	if err != nil {
		return nil, err
	}

	projectInfo, err := be.DB.FindProjectInfoByID(ctx, clientInfo.ProjectID)
	if err != nil {
		return nil, err
	}
	project := projectInfo.ToProject()

	auth := getAuthToken(ctx)

	cli, err := clientInfo.ToClient(apiHost, project.PublicKey, auth)
	if err != nil {
		return nil, err
	}

	for docID, info := range clientInfo.Documents {
		if info.Status != database.DocumentAttached {
			continue
		}

		docInfo, err := be.DB.FindDocInfoByRefKey(ctx, types.DocRefKey{
			ProjectID: clientInfo.ProjectID,
			DocID:     docID,
		})
		if err != nil {
			return nil, err
		}

		doc, err := packs.BuildDocForCheckpoint(ctx, be, docInfo, info.ServerSeq, info.ClientSeq, actorID)
		if err != nil {
			return nil, err
		}
		cli.SetAttach(ctx, doc, docID)

		if err := cli.Detach(ctx, doc); err != nil {
			return nil, err
		}

		// TODO(hackerwins): This is a temporary solution to detach the document
		// from the client. Documents are sharded between multiple servers in the
		// cluster to simplify the implementation including the distributed lock.
		// In the future, we need to request the detachments to the load balancer
		// and the load balancer will forward the request to the server that has
		// the document.
		//if _, err = packs.PushPull(ctx, be, project, clientInfo, docInfo, doc.CreateChangePack(), packs.PushPullOptions{
		//	Mode:   types.SyncModePushOnly,
		//	Status: document.StatusDetached,
		//}); err != nil {
		//	return nil, err
		//}
	}

	// 02. Deactivate the client.
	clientInfo, err = be.DB.DeactivateClient(ctx, refKey)
	if err != nil {
		return nil, err
	}

	return clientInfo, nil
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

func isEmptyCtx(ctx context.Context) bool {
	emptyCtxType := reflect.TypeOf(context.Background())
	return reflect.TypeOf(ctx) == emptyCtxType
}

func getAuthToken(ctx context.Context) string {
	if !isEmptyCtx(ctx) {
		md := metadata.From(ctx)
		return md.Authorization
	}

	return ""
}
