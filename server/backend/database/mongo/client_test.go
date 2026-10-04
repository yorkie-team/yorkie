//go:build integration

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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/key"
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
		ConnectionTimeout:  "5s",
		ConnectionURI:      "mongodb://localhost:27017",
		YorkieDatabase:     helper.TestDBName(),
		PingTimeout:        "5s",
		CacheStatsInterval: helper.MongoCacheStatsInterval,
		ProjectCacheSize:   helper.MongoProjectCacheSize,
		ProjectCacheTTL:    helper.MongoProjectCacheTTL,
		ClientCacheSize:    helper.MongoClientCacheSize,
		DocCacheSize:       helper.MongoDocCacheSize,
		ChangeCacheSize:    helper.MongoChangeCacheSize,
		VectorCacheSize:    helper.MongoVectorCacheSize,
	}
	assert.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	assert.NoError(t, err)

	return cli
}

func TestClient(t *testing.T) {
	cli := setupTestWithDummyData(t)

	t.Run("RunLeadership Test", func(t *testing.T) {
		testcases.RunLeadershipTest(t, cli)
	})

	t.Run("RunFindDocInfo test", func(t *testing.T) {
		testcases.RunFindDocInfoTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindDocInfosByKeysAndIDs test", func(t *testing.T) {
		testcases.RunFindDocInfosByKeysAndIDsTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindDocInfosByQuery test", func(t *testing.T) {
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

	t.Run("ListMemberInfos test", func(t *testing.T) {
		testcases.RunListMemberInfosTest(t, cli)
	})

	t.Run("UpdateMemberRole test", func(t *testing.T) {
		testcases.RunUpdateMemberRoleTest(t, cli)
	})

	t.Run("DeleteMemberInfo test", func(t *testing.T) {
		testcases.RunDeleteMemberInfoTest(t, cli)
	})

	t.Run("CreateInviteInfo test", func(t *testing.T) {
		testcases.RunCreateInviteInfoTest(t, cli)
	})

	t.Run("FindInviteInfoByToken test", func(t *testing.T) {
		testcases.RunFindInviteInfoByTokenTest(t, cli)
	})

	t.Run("DeleteExpiredInviteInfos test", func(t *testing.T) {
		testcases.RunDeleteExpiredInviteInfosTest(t, cli)
	})

	t.Run("ActivateClientDeactivateClient test", func(t *testing.T) {
		testcases.RunActivateClientDeactivateClientTest(t, cli, dummyProjectID)
	})

	t.Run("TryAttachingAndDeactivateClient test", func(t *testing.T) {
		testcases.RunTryAttachingAndDeactivateClientTest(t, cli, dummyProjectID)
	})

	t.Run("AttachResumeCheckpoint test", func(t *testing.T) {
		testcases.RunAttachResumeCheckpointTest(t, cli, dummyProjectID)
	})

	t.Run("VersionVectorStableActor test", func(t *testing.T) {
		testcases.RunVersionVectorStableActorTest(t, cli, dummyProjectID)
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

	t.Run("CompactChangeInfos test", func(t *testing.T) {
		testcases.RunCompactChangeInfosTest(t, cli, dummyProjectID)
	})

	t.Run("SnapshotLiveSize test", func(t *testing.T) {
		testcases.RunSnapshotLiveSizeTest(t, cli, dummyProjectID)
	})

	t.Run("UpdateClientInfoAfterPushPull test", func(t *testing.T) {
		testcases.RunUpdateClientInfoAfterPushPullTest(t, cli, dummyProjectID)
	})

	t.Run("IsDocumentAttachedOrAttaching test", func(t *testing.T) {
		testcases.RunIsDocumentAttachedOrAttachingTest(t, cli, dummyProjectID)
	})

	t.Run("FindClientInfosByAttachedDocRefKey test", func(t *testing.T) {
		testcases.RunFindClientInfosByAttachedDocRefKeyTest(t, cli, dummyProjectID)
	})

	t.Run("FindAttachedClientCountsByDocIDs test", func(t *testing.T) {
		testcases.RunFindAttachedClientCountsByDocIDsTest(t, cli, dummyProjectID)
	})

	t.Run("RunFindCandidates test", func(t *testing.T) {
		testcases.RunFindCandidatesTest(t, cli, dummyProjectID)
	})

	t.Run("FindCompactionCandidates test", func(t *testing.T) {
		testcases.RunFindCompactionCandidatesTest(t, cli, dummyProjectID)
	})
}

// TestClient_ClientCacheUnderConcurrentWrites checks that the client cache
// ends up with what the database holds when requests of one client race. A
// PushPull on one document, an attach of another and a plain read all touch
// the same client row; if an older copy of the row reaches the cache after a
// newer one, the cache forgets the attach and later requests of the client
// fail with "document not attached".
func TestClient_ClientCacheUnderConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	cli := setupTestWithDummyData(t)
	defer func() { assert.NoError(t, cli.Close()) }()

	info, err := cli.ActivateClient(ctx, dummyProjectID, t.Name(), nil)
	require.NoError(t, err)
	refKey := info.RefKey()
	docKey := func(name string) key.Key {
		return key.Key(fmt.Sprintf("tests$%s-%s-%s", t.Name(), info.ID, name))
	}
	attach := func(docInfo *database.DocInfo) error {
		attaching, err := cli.TryAttaching(ctx, refKey, docInfo.ID)
		if err != nil {
			return err
		}
		if err := attaching.AttachDocument(
			docInfo.ID, false, docInfo.Epoch, 0, change.InitialCheckpoint,
		); err != nil {
			return err
		}
		return cli.UpdateClientInfoAfterPushPull(ctx, attaching, docInfo)
	}

	busy, err := cli.FindOrCreateDocInfo(ctx, refKey, docKey("busy"), false)
	require.NoError(t, err)
	require.NoError(t, attach(busy))
	pushPuller, err := cli.FindClientInfoByRefKey(ctx, refKey)
	require.NoError(t, err)

	for i := range 200 {
		docInfo, err := cli.FindOrCreateDocInfo(ctx, refKey, docKey(fmt.Sprint(i)), false)
		require.NoError(t, err)

		// Advance the busy document's checkpoint so the PushPull write is not
		// skipped as already cached.
		pp := pushPuller.DeepCopy()
		require.NoError(t, pp.UpdateCheckpoint(busy.ID, change.NewCheckpoint(0, uint32(i+1))))

		var ppErr, attachErr, readErr error
		var wg sync.WaitGroup
		wg.Go(func() { ppErr = cli.UpdateClientInfoAfterPushPull(ctx, pp, busy) })
		wg.Go(func() { attachErr = attach(docInfo) })
		wg.Go(func() {
			for range 4 {
				if _, readErr = cli.FindClientInfoByRefKey(ctx, refKey); readErr != nil {
					return
				}
			}
		})
		wg.Wait()
		require.NoError(t, ppErr)
		require.NoError(t, attachErr)
		require.NoError(t, readErr)

		stored, err := cli.FindClientInfoByRefKey(ctx, refKey, true)
		require.NoError(t, err)
		require.NoError(t, stored.EnsureDocumentAttached(docInfo.ID))
		cached, err := cli.FindClientInfoByRefKey(ctx, refKey)
		require.NoError(t, err)
		require.NoError(t, cached.EnsureDocumentAttached(docInfo.ID), "iteration %d", i)
	}
}

func TestClient_CompactChangeInfosAcrossNodes(t *testing.T) {
	// Two clients on one database stand in for two server nodes: each keeps
	// its own document cache.
	nodeA := setupTestWithDummyData(t)
	nodeB := setupTestWithDummyData(t)
	defer func() {
		assert.NoError(t, nodeA.Close())
		assert.NoError(t, nodeB.Close())
	}()

	testcases.RunCompactChangeInfosAcrossNodesTest(t, nodeA, nodeB, dummyProjectID)
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
		projectInfo, err := client.CreateProjectInfo(ctx, "test-project-1", dummyProjectID)
		assert.NoError(t, err)

		originalPublicKey := projectInfo.PublicKey
		originalSecretKey := projectInfo.SecretKey

		// When
		updatedProject, _, err := client.RotateProjectKeys(
			ctx,
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
		_, _, err := client.RotateProjectKeys(
			ctx,
			types.ID("000000000000000000000003"),
			"new-public-key",
			"new-secret-key",
		)

		// Then
		assert.Error(t, err)
		assert.ErrorIs(t, err, database.ErrProjectNotFound)
	})
}
