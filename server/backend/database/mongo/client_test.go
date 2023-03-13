/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package mongo_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClient(t *testing.T) {
	ctx := context.Background()
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName(),
		PingTimeout:       "5s",
	}
	assert.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	assert.NoError(t, err)

	dummyProjectID := types.ID("000000000000000000000000")
	dummyOwnerID := types.ID("000000000000000000000000")
	otherOwnerID := types.ID("000000000000000000000001")
	clientDeactivateThreshold := "1h"

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		info, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		existName := "already"
		_, err = cli.CreateProjectInfo(ctx, existName, dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		id := info.ID
		newName := "changed-name"
		newAuthWebhookURL := "http://localhost:3000"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		newClientDeactivateThreshold := "2h"

		// update total project_field
		fields := &types.UpdatableProjectFields{
			Name:                      &newName,
			AuthWebhookURL:            &newAuthWebhookURL,
			AuthWebhookMethods:        &newAuthWebhookMethods,
			ClientDeactivateThreshold: &newClientDeactivateThreshold,
		}

		err = fields.Validate()
		assert.NoError(t, err)
		res, err := cli.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)

		updateInfo, err := cli.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)

		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newName, updateInfo.Name)
		assert.Equal(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// update one field
		newName2 := newName + "2"
		fields = &types.UpdatableProjectFields{
			Name: &newName2,
		}
		err = fields.Validate()
		assert.NoError(t, err)
		res, err = cli.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)

		updateInfo, err = cli.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)

		// check only name is updated
		assert.Equal(t, res, updateInfo)
		assert.NotEqual(t, newName, updateInfo.Name)
		assert.Equal(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// check duplicate name error
		fields = &types.UpdatableProjectFields{Name: &existName}
		_, err = cli.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.ErrorIs(t, err, database.ErrProjectNameAlreadyExists)
	})

	t.Run("FindProjectInfoByName test", func(t *testing.T) {
		info1, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		_, err = cli.CreateProjectInfo(ctx, t.Name(), otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		info2, err := cli.FindProjectInfoByName(ctx, dummyOwnerID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, info1.ID, info2.ID)
	})

	t.Run("set removed_at in docInfo test", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		docInfo, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo.ID, docKey, true)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set removed_at in docInfo and store changes
		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		err = cli.CreateChangeInfos(ctx, dummyProjectID, docInfo, 0, pack.Changes, true)
		assert.NoError(t, err)

		// Check whether removed_at is set in docInfo
		docInfo, err = cli.FindDocInfoByID(ctx, dummyProjectID, docInfo.ID)
		assert.NoError(t, err)
		assert.NotEqual(t, time.Time{}, docInfo.RemovedAt)

		// Check whether DocumentRemoved status is set in clientInfo
		clientInfo, err := cli.FindClientInfoByID(ctx, dummyProjectID, clientInfo.ID)
		assert.NoError(t, err)
		assert.NotEqual(t, database.DocumentRemoved, clientInfo.Documents[docInfo.ID].Status)
	})

	t.Run("reuse same key to create docInfo test ", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo1, _ := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		docInfo1, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo1.ID, docKey, true)
		assert.NoError(t, clientInfo1.AttachDocument(docInfo1.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo1))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set removed_at in docInfo and store changes
		err = cli.CreateChangeInfos(ctx, dummyProjectID, docInfo1, 0, pack.Changes, true)
		assert.NoError(t, err)

		// Use same key to create docInfo
		clientInfo2, _ := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		docInfo2, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo2.ID, docKey, true)
		assert.NoError(t, clientInfo2.AttachDocument(docInfo2.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo2))

		// Check they have same key but different id
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
	})
}
