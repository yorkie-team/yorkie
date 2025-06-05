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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/database/testcases"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	dummyProjectID = types.ID("000000000000000000000000")
	projectOneID   = types.ID("000000000000000000000001")
	projectTwoID   = types.ID("000000000000000000000002")
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

	t.Run("FindNextNCyclingProjectInfos test", func(t *testing.T) {
		testcases.RunFindNextNCyclingProjectInfosTest(t, cli)
	})

	t.Run("FindDeactivateCandidatesPerProject test", func(t *testing.T) {
		testcases.RunFindDeactivateCandidatesPerProjectTest(t, cli)
	})

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

	t.Run("RunFindLatestChangeInfoTest test", func(t *testing.T) {
		testcases.RunFindLatestChangeInfoTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindClosestSnapshotInfo test", func(t *testing.T) {
		testcases.RunFindClosestSnapshotInfoTest(t, cli, dummyProjectID)
	})

	t.Run("ListUserInfos test", func(t *testing.T) {
		t.Skip("TODO(hackerwins): time is returned as Local")
		testcases.RunListUserInfosTest(t, cli)
	})

	t.Run("FindUserInfoByID test", func(t *testing.T) {
		testcases.RunFindUserInfoByIDTest(t, cli)
	})

	t.Run("FindUserInfoByName test", func(t *testing.T) {
		testcases.RunFindUserInfoByNameTest(t, cli)
	})

	t.Run("FindProjectInfoBySecretKey test", func(t *testing.T) {
		testcases.RunFindProjectInfoBySecretKeyTest(t, cli)
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

	t.Run("FindClientInfosByAttachedDocRefKey test", func(t *testing.T) {
		testcases.RunFindClientInfosByAttachedDocRefKeyTest(t, cli, dummyProjectID)
	})

	t.Run("DeactivateClient test", func(t *testing.T) {
		testcases.RunDeactivateClientTest(t, cli)
	})
}

func TestClient_RotateProjectKeys(t *testing.T) {
	t.Run("success: should rotate project API keys", func(t *testing.T) {
		// Given
		ctx := context.Background()
		client := setupTestWithDummyData(t)
		defer func() {
			assert.NoError(t, client.Close())
		}()

		// Create a test project
		projectInfo, err := client.CreateProjectInfo(ctx, "test-project-1", dummyProjectID, "1h")
		assert.NoError(t, err)

		originalPublicKey := projectInfo.PublicKey
		originalSecretKey := projectInfo.SecretKey

		// When
		updatedProject, err := client.RotateProjectKeys(
			ctx,
			dummyProjectID,
			projectInfo.ID,
			"new-public-key",
			"new-secret-key",
		)

		// Then
		assert.NoError(t, err)
		assert.Equal(t, "new-public-key", updatedProject.PublicKey)
		assert.Equal(t, "new-secret-key", updatedProject.SecretKey)
		assert.NotEqual(t, originalPublicKey, updatedProject.PublicKey)
		assert.NotEqual(t, originalSecretKey, updatedProject.SecretKey)
	})

	t.Run("fail: should return error when project not found", func(t *testing.T) {
		// Given
		ctx := context.Background()
		client := setupTestWithDummyData(t)
		defer func() {
			assert.NoError(t, client.Close())
		}()

		// When
		_, err := client.RotateProjectKeys(
			ctx,
			dummyProjectID,
			types.ID("000000000000000000000003"),
			"new-public-key",
			"new-secret-key",
		)

		// Then
		assert.Error(t, err)
		assert.ErrorIs(t, err, database.ErrProjectNotFound)
	})

	t.Run("fail: should return error when user is not owner", func(t *testing.T) {
		// Given
		ctx := context.Background()
		client := setupTestWithDummyData(t)
		defer func() {
			assert.NoError(t, client.Close())
		}()

		// Create a test project
		projectInfo, err := client.CreateProjectInfo(ctx, "test-project-2", dummyProjectID, "1h")
		assert.NoError(t, err)

		// When
		_, err = client.RotateProjectKeys(
			ctx,
			types.ID("000000000000000000000003"),
			projectInfo.ID,
			"new-public-key",
			"new-secret-key",
		)

		// Then
		assert.Error(t, err)
		assert.ErrorIs(t, err, database.ErrProjectNotFound)
	})
}
