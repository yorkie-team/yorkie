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
	"github.com/yorkie-team/yorkie/test/helper"
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

func RunIsDocumentAttachedTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("single document IsDocumentAttached test", func(t *testing.T) {
		ctx := context.Background()

		// 00. Create two clients and a document
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		c2, err := db.ActivateClient(ctx, projectID, t.Name()+"2")
		assert.NoError(t, err)
		d1, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, c1.ID, helper.TestDocKey(t), true)
		assert.NoError(t, err)

		// 01. Check if document is attached without attaching
		attached, err := db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)

		// 02. Check if document is attached after attaching
		assert.NoError(t, c1.AttachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 03. Check if document is attached after detaching
		assert.NoError(t, c1.DetachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)

		// 04. Check if document is attached after two clients attaching
		assert.NoError(t, c1.AttachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		assert.NoError(t, c2.AttachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 05. Check if document is attached after a client detaching
		assert.NoError(t, c1.DetachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 06. Check if document is attached after another client detaching
		assert.NoError(t, c2.DetachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("two documents IsDocumentAttached test", func(t *testing.T) {
		ctx := context.Background()

		// 00. Create a client and two documents
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		d1, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, c1.ID, helper.TestDocKey(t)+"1", true)
		assert.NoError(t, err)
		d2, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, c1.ID, helper.TestDocKey(t)+"2", true)
		assert.NoError(t, err)

		// 01. Check if documents are attached after attaching
		assert.NoError(t, c1.AttachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err := db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		assert.NoError(t, c1.AttachDocument(d2.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d2))
		attached, err = db.IsDocumentAttached(ctx, projectID, d2.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 02. Check if a document is attached after detaching another document
		assert.NoError(t, c1.DetachDocument(d2.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d2))
		attached, err = db.IsDocumentAttached(ctx, projectID, d2.ID)
		assert.NoError(t, err)
		assert.False(t, attached)
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 03. Check if a document is attached after detaching remaining document
		assert.NoError(t, c1.DetachDocument(d1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, projectID, d1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)
	})
}
