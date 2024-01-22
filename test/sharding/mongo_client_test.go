//go:build sharding

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

package sharding

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
	"github.com/yorkie-team/yorkie/server/backend/database/testcases"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	shardedDBNameForMongoClient = "test-yorkie-meta-mongo-client"
	dummyProjectID              = types.ID("000000000000000000000000")
	projectOneID                = types.ID("000000000000000000000001")
	projectTwoID                = types.ID("000000000000000000000002")
	dummyOwnerID                = types.ID("000000000000000000000000")
	dummyClientID               = types.ID("000000000000000000000000")
	clientDeactivateThreshold   = "1h"
)

func setupMongoClient(databaseName string) (*mongo.Client, error) {
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    databaseName,
		PingTimeout:       "5s",
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	cli, err := mongo.Dial(config)
	if err != nil {
		return nil, err
	}

	return cli, nil
}

func TestClientWithShardedDB(t *testing.T) {
	// Cleanup the previous data in DB
	assert.NoError(t, helper.CleanUpAllCollections(shardedDBNameForMongoClient))

	cli, err := setupMongoClient(shardedDBNameForMongoClient)
	assert.NoError(t, err)

	t.Run("RunFindDocInfo test", func(t *testing.T) {
		testcases.RunFindDocInfoTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindDocInfosByQuery test", func(t *testing.T) {
		t.Skip("TODO(hackerwins): the order of docInfos is different with memDB")
		testcases.RunFindDocInfosByQueryTest(t, cli, projectOneID)
	})

	t.Run("RunFindChangesBetweenServerSeqs test", func(t *testing.T) {
		testcases.RunFindChangesBetweenServerSeqsTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindClosestSnapshotInfo test", func(t *testing.T) {
		testcases.RunFindClosestSnapshotInfoTest(t, cli, dummyProjectID)
	})

	t.Run("ListUserInfos test", func(t *testing.T) {
		t.Skip("TODO(hackerwins): time is returned as Local")
		testcases.RunListUserInfosTest(t, cli)
	})

	t.Run("FindProjectInfoByName test", func(t *testing.T) {
		testcases.RunFindProjectInfoByNameTest(t, cli)
	})

	t.Run("ActivateClientDeactivateClient test", func(t *testing.T) {
		testcases.RunActivateClientDeactivateClientTest(t, cli, dummyProjectID)
	})

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		testcases.RunUpdateProjectInfoTest(t, cli)
	})

	t.Run("FindDocInfosByPaging test", func(t *testing.T) {
		testcases.RunFindDocInfosByPagingTest(t, cli, projectTwoID)
	})

	t.Run("CreateChangeInfo test", func(t *testing.T) {
		testcases.RunCreateChangeInfosTest(t, cli, dummyProjectID)
	})

	t.Run("UpdateClientInfoAfterPushPull test", func(t *testing.T) {
		testcases.RunUpdateClientInfoAfterPushPullTest(t, cli, dummyProjectID)
	})

	t.Run("IsDocumentAttached test", func(t *testing.T) {
		testcases.RunIsDocumentAttachedTest(t, cli, dummyProjectID)
	})

	t.Run("FindDocInfoByKeyAndID with duplicate ID test", func(t *testing.T) {
		ctx := context.Background()

		// 01. Initialize a project and create a document.
		projectInfo, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		docKey1 := key.Key(fmt.Sprintf("%s%d", "duplicateIDTestDocKey", 0))
		docInfo1, err := cli.FindDocInfoByKeyAndOwner(ctx, projectInfo.ID, dummyClientID, docKey1, true)
		assert.NoError(t, err)

		// 02. Create an extra document with duplicate ID.
		docKey2 := key.Key(fmt.Sprintf("%s%d", "duplicateIDTestDocKey", 5))
		err = helper.CreateDummyDocumentWithID(
			shardedDBNameForMongoClient,
			projectInfo.ID,
			docInfo1.ID,
			docKey2,
		)
		assert.NoError(t, err)

		// 03. Check if there are two documents with the same ID.
		infos, err := helper.FindDocInfosWithID(
			shardedDBNameForMongoClient,
			projectInfo.ID,
			docInfo1.ID,
		)
		assert.NoError(t, err)
		assert.Len(t, infos, 2)

		// 04. Check if the document is correctly found using docKey and docID.
		result, err := cli.FindDocInfoByRefKey(
			ctx,
			projectInfo.ID,
			types.DocRefKey{
				Key: docKey1,
				ID:  docInfo1.ID,
			},
		)
		assert.NoError(t, err)
		assert.Equal(t, docInfo1.Key, result.Key)
		assert.Equal(t, docInfo1.ID, result.ID)
	})

	t.Run("FindDocInfosByPaging with duplicate ID test", func(t *testing.T) {
		const totalDocCnt = 10
		const duplicateIDDocCnt = 1
		ctx := context.Background()

		// 01. Initialize a project and create documents.
		projectInfo, err := cli.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		var docInfos []*database.DocInfo
		var duplicateID types.ID
		for i := 0; i < totalDocCnt-duplicateIDDocCnt; i++ {
			testDocKey := key.Key("duplicateIDTestDocKey" + strconv.Itoa(i))
			docInfo, err := cli.FindDocInfoByKeyAndOwner(ctx, projectInfo.ID, dummyClientID, testDocKey, true)
			assert.NoError(t, err)
			docInfos = append(docInfos, docInfo)

			if i == 0 {
				duplicateID = docInfo.ID
			}
		}
		// NOTE(sejongk): sorting is required because doc_id may not sequentially increase in a sharded DB cluster.
		testcases.SortDocInfos(docInfos)

		// 02. Create an extra document with duplicate ID.
		for i := totalDocCnt - duplicateIDDocCnt; i < totalDocCnt; i++ {
			testDocKey := key.Key("duplicateIDTestDocKey" + strconv.Itoa(i))
			err = helper.CreateDummyDocumentWithID(
				shardedDBNameForMongoClient,
				projectInfo.ID,
				duplicateID,
				testDocKey,
			)
			assert.NoError(t, err)
			docInfos = append(docInfos, &database.DocInfo{
				ID:  duplicateID,
				Key: testDocKey,
			})
		}
		testcases.SortDocInfos(docInfos)

		docKeysInReverse := make([]key.Key, 0, totalDocCnt)
		for _, docInfo := range docInfos {
			docKeysInReverse = append([]key.Key{docInfo.Key}, docKeysInReverse...)
		}

		// 03. List the documents.
		result, err := cli.FindDocInfosByPaging(ctx, projectInfo.ID, types.Paging[types.DocRefKey]{
			PageSize:  10,
			IsForward: false,
		})
		assert.NoError(t, err)
		testcases.AssertKeys(t, docKeysInReverse, result)
	})
}
