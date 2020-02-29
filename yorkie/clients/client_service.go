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

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/types"
)

func Activate(
	ctx context.Context,
	be *backend.Backend,
	clientKey string,
) (*types.ClientInfo, error) {
	return be.Mongo.ActivateClient(ctx, clientKey)
}

func Deactivate(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
) (*types.ClientInfo, error) {
	return be.Mongo.DeactivateClient(ctx, clientID)
}

func FindClientAndDocument(
	ctx context.Context,
	be *backend.Backend,
	clientID string,
	pack *change.Pack,
) (*types.ClientInfo, *types.DocInfo, error) {
	clientInfo, err := be.Mongo.FindClientInfoByID(ctx, clientID)
	if err != nil {
		return nil, nil, err
	}

	docInfo, err := be.Mongo.FindDocInfoByKey(ctx, clientInfo, pack.DocumentKey.BSONKey())
	if err != nil {
		return nil, nil, err
	}

	return clientInfo, docInfo, nil
}
