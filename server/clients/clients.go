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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
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
	gatewayAddr string,
) (*database.ClientInfo, error) {
	// NOTE(hackerwins): Before deactivating the client, we need to detach all
	// attached documents from the client.
	// Because detachments and deactivation are separate steps, failure of steps
	// must be considered. If each step of detachments is failed, some documents
	// are still attached and the client is not deactivated. In this case,
	// the client or housekeeping process should retry the deactivation.
	clientInfo, err := be.DB.FindClientInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, err
	}

	projectInfo, err := be.DB.FindProjectInfoByID(ctx, clientInfo.ProjectID)
	if err != nil {
		return nil, err
	}
	project := projectInfo.ToProject()

	// 02. Detach attached documents from the client.
	for docID, info := range clientInfo.Documents {
		if info.Status != database.DocumentAttached {
			continue
		}

		data := map[string]interface{}{
			"docID":         docID,
			"clientDocInfo": info,
			"clientInfo":    clientInfo,
			"project":       project,
		}
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}

		httpClient := &http.Client{}
		fmt.Println("sends: ")
		req, err := http.NewRequest(
			"POST",
			"http://"+gatewayAddr+"/detach",
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(types.ShardKey, string(docID))
		req.Header.Set("Authorization", "Bearer your_token_here")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP Status %d", resp.StatusCode)
		}

		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}

		if "success" != strings.TrimSpace(string(body)) {
			return nil, fmt.Errorf("detach request fails")
		}
	}

	// 03. Deactivate the client.
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

//func withShareKey()

/*
func withShardKey[T any](conn *connect.Request[T], keys ...string) *connect.Request[T] {
	conn.Header().Add(types.ShardKey, strings.Join(keys, "/"))

	return conn
}

*/
