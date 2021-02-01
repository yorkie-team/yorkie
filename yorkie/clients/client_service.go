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
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

// Activate activates the given client.
func Activate(
	ctx context.Context,
	be *backend.Backend,
	clientKey string,
) (*db.ClientInfo, error) {
	return be.DB.ActivateClient(ctx, clientKey)
}

// Deactivate deactivates the given client.
func Deactivate(
	ctx context.Context,
	be *backend.Backend,
	clientID []byte,
) (*db.ClientInfo, error) {
	return be.DB.DeactivateClient(ctx, db.IDFromBytes(clientID))
}

// FindClientAndDocument finds the client and the document.
func FindClientAndDocument(
	ctx context.Context,
	be *backend.Backend,
	clientID []byte,
	pack *change.Pack,
	createDocIfNotExist bool,
) (*db.ClientInfo, *db.DocInfo, error) {
	clientInfo, err := be.DB.FindClientInfoByID(ctx, db.IDFromBytes(clientID))
	if err != nil {
		return nil, nil, err
	}

	docInfo, err := be.DB.FindDocInfoByKey(
		ctx,
		clientInfo,
		pack.DocumentKey.BSONKey(),
		createDocIfNotExist,
	)
	if err != nil {
		return nil, nil, err
	}

	return clientInfo, docInfo, nil
}
