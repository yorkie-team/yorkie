//go:build complex

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

package complex

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
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

	t.Run("RunFindDocInfosByKeys test", func(t *testing.T) {
		testcases.RunFindDocInfosByKeysTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindDocInfosByQuery test", func(t *testing.T) {
		t.Skip("TODO(hackerwins): the order of docInfos is different with memDB")
		testcases.RunFindDocInfosByQueryTest(t, cli, projectOneID)
	})

	t.Run("RunFindChangesBetweenServerSeqs test", func(t *testing.T) {
		testcases.RunFindChangesBetweenServerSeqsTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindChangeInfosBetweenServerSeqsTest test", func(t *testing.T) {
		testcases.RunFindChangeInfosBetweenServerSeqsTest(t, cli, dummyProjectID)
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

	t.Run("TryAttachingAndDeactivateClient test", func(t *testing.T) {
		testcases.RunTryAttachingAndDeactivateClientTest(t, cli, dummyProjectID)
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

	t.Run("FindDocInfoByRefKey with duplicate ID test", func(t *testing.T) {
		ctx := context.Background()

		projectID1 := types.ID("000000000000000000000000")
		projectID2 := types.ID("FFFFFFFFFFFFFFFFFFFFFFFF")

		docKey1 := key.Key(fmt.Sprintf("%s%d", t.Name(), 1))
		docKey2 := key.Key(fmt.Sprintf("%s%d", t.Name(), 2))

		// 01. Initialize a project and create a document.
		docInfo1, err := cli.FindOrCreateDocInfo(ctx, types.ClientRefKey{
			ProjectID: projectID1,
			ClientID:  dummyClientID,
		}, docKey1)
		assert.NoError(t, err)

		// 02. Create an extra document with duplicate ID.
		err = helper.CreateDummyDocumentWithID(
			shardedDBNameForMongoClient,
			projectID2,
			docInfo1.ID,
			docKey2,
		)
		assert.NoError(t, err)

		// 03. Check if there are two documents with the same ID.
		infos, err := helper.FindDocInfosWithID(
			shardedDBNameForMongoClient,
			docInfo1.ID,
		)
		assert.NoError(t, err)
		assert.Len(t, infos, 2)

		// 04. Check if the document is correctly found using docKey and docID.
		result, err := cli.FindDocInfoByRefKey(
			ctx,
			types.DocRefKey{
				ProjectID: projectID1,
				DocID:     docInfo1.ID,
			},
		)
		assert.NoError(t, err)
		assert.Equal(t, docInfo1.Key, result.Key)
		assert.Equal(t, docInfo1.ID, result.ID)
	})
}
