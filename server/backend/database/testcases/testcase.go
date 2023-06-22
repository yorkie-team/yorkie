/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package testcases contains testcases for database. It is used by database
// implementations to test their own implementations with the same testcases.
package testcases

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	mongodb "go.mongodb.org/mongo-driver/mongo"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// RunUpdateClientInfoAfterPushPullTest executes the testcases for the given database.
func RunUpdateClientInfoAfterPushPullTest(t *testing.T, db database.Database, projectID types.ID) {
	dummyClientID := types.ID("000000000000000000000000")
	ctx := context.Background()

	t.Run("document is not attached in clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
	})

	t.Run("document attach test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("update server_seq and client_seq in clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		// update with larger seq
		clientInfo.Documents[docInfo.ID].ServerSeq = 3
		clientInfo.Documents[docInfo.ID].ClientSeq = 5
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(3))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(5))
		assert.NoError(t, err)

		// update with smaller seq(should be ignored)
		clientInfo.Documents[docInfo.ID].ServerSeq = 2
		clientInfo.Documents[docInfo.ID].ClientSeq = 3
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(3))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(5))
		assert.NoError(t, err)
	})

	t.Run("detach document test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.DetachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentDetached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("remove document test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentRemoved)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("invalid clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		clientInfo.ID = "invalid clientInfo id"
		assert.Error(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		clientInfo.ID = dummyClientID
		assert.Error(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo), mongodb.ErrNoDocuments)
	})
}
