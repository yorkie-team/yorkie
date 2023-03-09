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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	ctx        = context.Background()
	testDBName = helper.TestDBName()
	config     = &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    testDBName,
		PingTimeout:       "5s",
	}

	cli  *mongo.Client
	_err error

	dummyOwnerID              = types.ID("000000000000000000000000")
	otherOwnerID              = types.ID("000000000000000000000001")
	dummyClientID             = types.ID("000000000000000000000000")
	dummyProjectName          = "dummyProject"
	clientDeactivateThreshold = "1h"

	testProjectInfo *database.ProjectInfo
	testDocCnt      = 25
)

func setupTestWithDummyData(t *testing.T) {
	assert.NoError(t, config.Validate())

	cli, _err = mongo.Dial(config)
	assert.NoError(t, _err)

	// dummy project setup
	testProjectInfo, _err = cli.CreateProjectInfo(ctx, dummyProjectName, dummyOwnerID, clientDeactivateThreshold)
	assert.NoError(t, _err)

	// dummy document setup
	for i := 0; i <= testDocCnt; i++ {
		testDocKey := key.Key("testdockey" + strconv.Itoa(i))
		_, err := cli.FindDocInfoByKeyAndOwner(ctx, testProjectInfo.ID, dummyClientID, testDocKey, true)
		assert.NoError(t, err)
	}
}

func cleanupTest(cli *mongo.Client, testDBName string) {
	if err := cli.DropDatabase(testDBName); err != nil {
		_ = fmt.Errorf("test cleanup fail: %w", err)
	}
}

func TestClient(t *testing.T) {

	setupTestWithDummyData(t)
	defer cleanupTest(cli, testDBName)

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		existName := "already"
		_, err := cli.CreateProjectInfo(ctx, existName, dummyOwnerID, clientDeactivateThreshold)
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
		info1, err := cli.CreateProjectInfo(ctx, dummyProjectName, dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		_, err = cli.CreateProjectInfo(ctx, dummyProjectName, otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		info2, err := cli.FindProjectInfoByName(ctx, dummyOwnerID, dummyProjectName)
		fmt.Println(info2)
		assert.NoError(t, err)
		assert.Equal(t, info1.ID, info2.ID)
	})

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
			testResult: makeRangeSlice(25, 0),
		},
		{
			name:       "FindDocInfosByPaging --forward test",
			offset:     "",
			pageSize:   0,
			isForward:  true,
			testResult: makeRangeSlice(0, 25),
		},
		{
			name:       "FindDocInfosByPaging --size test",
			offset:     "",
			pageSize:   4,
			isForward:  false,
			testResult: makeRangeSlice(25, 22),
		},
		{
			name:       "FindDocInfosByPaging --size --forward test",
			offset:     "",
			pageSize:   4,
			isForward:  true,
			testResult: makeRangeSlice(0, 3),
		},
		{
			name:       "FindDocInfosByPaging --offset test",
			offset:     findDocIDByDocKey(testProjectInfo.ID, "testdockey13"),
			pageSize:   0,
			isForward:  false,
			testResult: makeRangeSlice(12, 0),
		},
		{
			name:       "FindDocInfosByPaging --forward --offset test",
			offset:     findDocIDByDocKey(testProjectInfo.ID, "testdockey13"),
			pageSize:   0,
			isForward:  true,
			testResult: makeRangeSlice(14, 25),
		},
		{
			name:       "FindDocInfosByPaging --size --offset test",
			offset:     findDocIDByDocKey(testProjectInfo.ID, "testdockey13"),
			pageSize:   10,
			isForward:  false,
			testResult: makeRangeSlice(12, 3),
		},
		{
			name:       "FindDocInfosByPaging --size --forward --offset test",
			offset:     findDocIDByDocKey(testProjectInfo.ID, "testdockey13"),
			pageSize:   10,
			isForward:  true,
			testResult: makeRangeSlice(14, 23),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testPaging := types.Paging[types.ID]{
				Offset:    types.ID(c.offset),
				PageSize:  c.pageSize,
				IsForward: c.isForward,
			}

			docInfos, err := cli.FindDocInfosByPaging(ctx, testProjectInfo.ID, testPaging)
			assert.NoError(t, err)

			fmt.Println(len(docInfos))
			for idx, docInfo := range docInfos {
				fmt.Println(docInfo)
				testDocKey := key.Key("testdockey" + strconv.Itoa(c.testResult[idx]))
				assert.Equal(t, docInfo.Key, testDocKey)
			}
		})
	}
}

func makeRangeSlice(start, end int) []int {
	var slice []int
	if start < end {
		for i := start; i <= end; i++ {
			slice = append(slice, i)
		}
	} else {
		for i := start; i >= end; i-- {
			slice = append(slice, i)
		}
	}
	return slice
}

func findDocIDByDocKey(projectID types.ID, docKey string) string {
	docInfo, err := cli.FindDocInfoByKey(ctx, projectID, key.Key(docKey))
	if err != nil {
		_ = fmt.Errorf("cannot find docID by docKey: %w", err)
		return ""
	}
	return docInfo.ID.String()
}
