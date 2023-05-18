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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	dummyProjectID            = types.ID("000000000000000000000000")
	dummyOwnerID              = types.ID("000000000000000000000000")
	otherOwnerID              = types.ID("000000000000000000000001")
	dummyClientID             = types.ID("000000000000000000000000")
	clientDeactivateThreshold = "1h"
)

func setupTestWithDummyData(t *testing.T) *mongo.Client {
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName(),
		PingTimeout:       "5s",
	}
	assert.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	assert.NoError(t, err)

	return cli
}

func TestClient(t *testing.T) {
	cli := setupTestWithDummyData(t)

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		ctx := context.Background()

		testProjectInfo, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		existName := "already"
		_, err = cli.CreateProjectInfo(ctx, existName, dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		id := testProjectInfo.ID
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
		ctx := context.Background()

		info1, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		_, err = cli.CreateProjectInfo(ctx, t.Name(), otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		info2, err := cli.FindProjectInfoByName(ctx, dummyOwnerID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, info1.ID, info2.ID)
	})

	t.Run("set removed_at in docInfo test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		docInfo, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo.ID, docKey, true)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set removed_at in docInfo and store changes
		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		err := cli.CreateChangeInfos(ctx, dummyProjectID, docInfo, 0, pack.Changes, true)
		assert.NoError(t, err)

		// Check whether removed_at is set in docInfo
		docInfo, err = cli.FindDocInfoByID(ctx, dummyProjectID, docInfo.ID)
		assert.NoError(t, err)
		assert.NotEqual(t, time.Time{}, docInfo.RemovedAt)

		// Check whether DocumentRemoved status is set in clientInfo
		clientInfo, err = cli.FindClientInfoByID(ctx, dummyProjectID, clientInfo.ID)
		assert.NoError(t, err)
		assert.NotEqual(t, database.DocumentRemoved, clientInfo.FindDocumentInfo(docInfo.ID).Status)
	})

	t.Run("reuse same key to create docInfo test ", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo1, _ := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		docInfo1, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo1.ID, docKey, true)
		assert.NoError(t, clientInfo1.AttachDocument(docInfo1.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo1))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set removed_at in docInfo and store changes
		err := cli.CreateChangeInfos(ctx, dummyProjectID, docInfo1, 0, pack.Changes, true)
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

	t.Run("FindDocInfosByPaging test", func(t *testing.T) {
		const testDocCnt = 25

		ctx := context.Background()

		// dummy project setup
		testProjectInfo, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		// dummy document setup
		var dummyDocInfos []*database.DocInfo
		for i := 0; i <= testDocCnt; i++ {
			testDocKey := key.Key("testdockey" + strconv.Itoa(i))
			docInfo, err := cli.FindDocInfoByKeyAndOwner(ctx, testProjectInfo.ID, dummyClientID, testDocKey, true)
			assert.NoError(t, err)
			dummyDocInfos = append(dummyDocInfos, docInfo)
		}

		cases := []struct {
			name       string
			offset     string
			pageSize   int
			isForward  bool
			testResult []int
		}{
			{
				name:       "FindDocInfosByPaging no flag test",
				offset:     "",
				pageSize:   0,
				isForward:  false,
				testResult: helper.NewRangeSlice(testDocCnt, 0),
			},
			{
				name:       "FindDocInfosByPaging --forward test",
				offset:     "",
				pageSize:   0,
				isForward:  true,
				testResult: helper.NewRangeSlice(0, testDocCnt),
			},
			{
				name:       "FindDocInfosByPaging --size test",
				offset:     "",
				pageSize:   4,
				isForward:  false,
				testResult: helper.NewRangeSlice(testDocCnt, testDocCnt-4),
			},
			{
				name:       "FindDocInfosByPaging --size --forward test",
				offset:     "",
				pageSize:   4,
				isForward:  true,
				testResult: helper.NewRangeSlice(0, 3),
			},
			{
				name:       "FindDocInfosByPaging --offset test",
				offset:     dummyDocInfos[13].ID.String(),
				pageSize:   0,
				isForward:  false,
				testResult: helper.NewRangeSlice(12, 0),
			},
			{
				name:       "FindDocInfosByPaging --forward --offset test",
				offset:     dummyDocInfos[13].ID.String(),
				pageSize:   0,
				isForward:  true,
				testResult: helper.NewRangeSlice(14, testDocCnt),
			},
			{
				name:       "FindDocInfosByPaging --size --offset test",
				offset:     dummyDocInfos[13].ID.String(),
				pageSize:   10,
				isForward:  false,
				testResult: helper.NewRangeSlice(12, 3),
			},
			{
				name:       "FindDocInfosByPaging --size --forward --offset test",
				offset:     dummyDocInfos[13].ID.String(),
				pageSize:   10,
				isForward:  true,
				testResult: helper.NewRangeSlice(14, 23),
			},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				ctx := context.Background()
				testPaging := types.Paging[types.ID]{
					Offset:    types.ID(c.offset),
					PageSize:  c.pageSize,
					IsForward: c.isForward,
				}

				docInfos, err := cli.FindDocInfosByPaging(ctx, testProjectInfo.ID, testPaging)
				assert.NoError(t, err)

				for idx, docInfo := range docInfos {
					resultIdx := c.testResult[idx]
					assert.Equal(t, docInfo.Key, dummyDocInfos[resultIdx].Key)
					assert.Equal(t, docInfo.ID, dummyDocInfos[resultIdx].ID)
					assert.Equal(t, docInfo.ProjectID, dummyDocInfos[resultIdx].ProjectID)
				}
			})
		}
	})

	t.Run("UpdateDocInfoRemovedAt test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, err := cli.ActivateClient(ctx, dummyProjectID, t.Name())
		assert.NoError(t, err)
		docInfo1, err := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)
		assert.NoError(t, clientInfo.AttachDocument(docInfo1.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo1))

		assert.True(t, docInfo1.RemovedAt.IsZero())

		err = cli.UpdateDocInfoRemovedAt(ctx, dummyProjectID, docInfo1.ID)
		assert.NoError(t, err)

		assert.True(t, docInfo1.RemovedAt.IsZero())

		docInfo2, _ := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo.ID, docKey, true)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.Equal(t, docInfo1.ProjectID, docInfo2.ProjectID)
		assert.True(t, docInfo2.RemovedAt.IsZero())

		notPresentDocID := types.ID("000000000000000000000011")
		err = cli.UpdateDocInfoRemovedAt(ctx, dummyProjectID, notPresentDocID)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)
	})

	t.Run("IsAttachedDocument test", func(t *testing.T) {
		ctx := context.Background()
		docKey1 := key.Key(fmt.Sprintf("tests$%s", t.Name()+"1"))

		clientInfo1, err := cli.ActivateClient(ctx, dummyProjectID, t.Name()+"1")
		assert.NoError(t, err)
		docInfo, err := cli.FindDocInfoByKeyAndOwner(ctx, dummyProjectID, clientInfo1.ID, docKey1, true)
		assert.NoError(t, err)

		// Check any document is not attached
		isAttached, err := cli.IsAttachedDocument(ctx, dummyProjectID, docInfo.ID)
		assert.False(t, isAttached)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo1.AttachDocument(docInfo.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))

		// Check document is attached
		isAttached, err = cli.IsAttachedDocument(ctx, dummyProjectID, docInfo.ID)
		assert.True(t, isAttached)
		assert.NoError(t, err)

		// Check document is detached
		assert.NoError(t, clientInfo1.DetachDocument(docInfo.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))
		isAttached, err = cli.IsAttachedDocument(ctx, dummyProjectID, docInfo.ID)
		assert.False(t, isAttached)
		assert.NoError(t, err)

		// Check whether document is attached in any other client
		clientInfo2, err := cli.ActivateClient(ctx, dummyProjectID, t.Name()+"2")
		assert.NoError(t, err)
		assert.NoError(t, clientInfo2.AttachDocument(docInfo.ID))
		assert.NoError(t, cli.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo))

		isAttached, err = cli.IsAttachedDocument(ctx, dummyProjectID, docInfo.ID)
		assert.True(t, isAttached)
		assert.NoError(t, err)
	})
}
