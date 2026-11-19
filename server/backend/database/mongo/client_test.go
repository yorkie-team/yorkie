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
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

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

	// testDialTimeout is the budget setupTestWithDummyData gives mongo.Dial to
	// connect, ping and build the indexes of a database it is the first to
	// open. The server default is 5s, which suits a process that should fail
	// fast at startup rather than hang; a test binary instead dials while the
	// rest of the suite drives the same mongod under -race, where a cold dial
	// overruns 5s on load alone and require.NoError reports a slow database as
	// a failed test. A database that is genuinely unreachable still fails the
	// test, only later.
	testDialTimeout = "30s"
)

// newTestConfig returns the config setupTestWithDummyData dials with.
func newTestConfig(t *testing.T) *mongo.Config {
	return &mongo.Config{
		ConnectionTimeout:  testDialTimeout,
		ConnectionURI:      "mongodb://localhost:27017",
		YorkieDatabase:     helper.TestDBName() + helper.TestRunSuffix(t),
		PingTimeout:        testDialTimeout,
		CacheStatsInterval: helper.MongoCacheStatsInterval,
		ProjectCacheSize:   helper.MongoProjectCacheSize,
		ProjectCacheTTL:    helper.MongoProjectCacheTTL,
		ClientCacheSize:    helper.MongoClientCacheSize,
		DocCacheSize:       helper.MongoDocCacheSize,
		ChangeCacheSize:    helper.MongoChangeCacheSize,
		VectorCacheSize:    helper.MongoVectorCacheSize,
	}
}

// setupTestWithDummyData dials a mongo.Client for the test and closes it when
// the test ends. A failed dial stops the test there, rather than handing the
// test a nil client to panic on, which would take down every test after it in
// the package. A rerun of the test under -count gets a database of its own,
// since the shared testcases name their documents and users after the test.
func setupTestWithDummyData(t *testing.T) *mongo.Client {
	config := newTestConfig(t)
	require.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cli.Close()) })

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

	t.Run("GetOrCreateUserInfoByProvider test", func(t *testing.T) {
		testcases.RunGetOrCreateUserInfoByProviderTest(t, cli)
	})

	t.Run("FindProjectInfoBySecretKey test", func(t *testing.T) {
		testcases.RunFindProjectInfoBySecretKeyTest(t, cli)
	})

	t.Run("FindProjectInfoByName test", func(t *testing.T) {
		testcases.RunFindProjectInfoByNameTest(t, cli)
	})

	t.Run("CreateProjectInfo defaults test", func(t *testing.T) {
		testcases.RunCreateProjectInfoDefaultsTest(t, cli)
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

	t.Run("RunPurgeDocument test", func(t *testing.T) {
		testcases.RunPurgeDocument(t, cli, dummyProjectID)
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

// TestClient_AttachedClientLookupDoesNotCacheStaleRows pins the behavior of
// FindAttachedClientInfosByRefKey: the rows it reads must not reach the client
// cache. They are read outside the clients' cache locks, so a row it returns
// may already be older than one a concurrent write cached, and caching it
// would make every later request of that client read the stale copy.
func TestClient_AttachedClientLookupDoesNotCacheStaleRows(t *testing.T) {
	// Two clients on one database stand in for two server nodes: each keeps
	// its own client cache.
	ctx := context.Background()
	nodeA := setupTestWithDummyData(t)
	nodeB := setupTestWithDummyData(t)

	info, err := nodeA.ActivateClient(ctx, dummyProjectID, t.Name(), nil)
	require.NoError(t, err)
	refKey := info.RefKey()

	docInfo, err := nodeA.FindOrCreateDocInfo(
		ctx, refKey, key.Key(fmt.Sprintf("tests$%s-%s", t.Name(), info.ID)), false,
	)
	require.NoError(t, err)
	attaching, err := nodeA.TryAttaching(ctx, refKey, docInfo.ID)
	require.NoError(t, err)
	require.NoError(t, attaching.AttachDocument(
		docInfo.ID, false, docInfo.Epoch, 0, change.InitialCheckpoint,
	))
	require.NoError(t, nodeA.UpdateClientInfoAfterPushPull(ctx, attaching, docInfo))

	// nodeB reads the row through the bulk lookup, which is the only way it
	// sees this client at all so far.
	attached, err := nodeB.FindAttachedClientInfosByRefKey(ctx, docInfo.RefKey())
	require.NoError(t, err)
	require.Len(t, attached, 1)
	require.Equal(t, info.ID, attached[0].ID)

	// nodeA detaches the document. If the lookup above had filled nodeB's
	// cache, nodeB would still read the attached copy it cached.
	detaching := attached[0].DeepCopy()
	require.NoError(t, detaching.DetachDocument(docInfo.ID))
	require.NoError(t, nodeA.UpdateClientInfoAfterPushPull(ctx, detaching, docInfo))

	cached, err := nodeB.FindClientInfoByRefKey(ctx, refKey)
	require.NoError(t, err)
	require.ErrorIs(t, cached.EnsureDocumentAttached(docInfo.ID), database.ErrDocumentNotAttached)
}

// TestClient_DocCacheInvalidationAcrossNodes checks that a purge performed on
// one node becomes visible on another once the invalidation reaches it. The
// document row is cached per node and the cache has no TTL, so without the
// drop the second node keeps answering FindDocInfoByRefKey from a copy of a
// row that no longer exists.
func TestClient_DocCacheInvalidationAcrossNodes(t *testing.T) {
	// Two clients on one database stand in for two server nodes: each keeps
	// its own document cache.
	ctx := context.Background()
	nodeA := setupTestWithDummyData(t)
	nodeB := setupTestWithDummyData(t)

	info, err := nodeA.ActivateClient(ctx, dummyProjectID, t.Name(), nil)
	require.NoError(t, err)
	docInfo, err := nodeA.FindOrCreateDocInfo(
		ctx, info.RefKey(), key.Key(fmt.Sprintf("tests$%s-%s", t.Name(), info.ID)), false,
	)
	require.NoError(t, err)
	refKey := docInfo.RefKey()

	// nodeB reads the document, which is what fills its cache.
	_, err = nodeB.FindDocInfoByRefKey(ctx, refKey)
	require.NoError(t, err)

	_, err = nodeA.PurgeDocument(ctx, refKey)
	require.NoError(t, err)

	// This is the call the purge's cluster broadcast makes on every other
	// node. Without it nodeB still reads the document it cached.
	nodeB.InvalidateCache(types.CacheTypeDocument, refKey.CacheKey())

	_, err = nodeB.FindDocInfoByRefKey(ctx, refKey)
	require.ErrorIs(t, err, database.ErrDocumentNotFound)
}

// TestClient_DocCacheUnderReadRacingPurge checks that a read racing a purge
// of the same document cannot leave the purged row in the cache. The read
// fetches the row from MongoDB, then the purge deletes it and drops the
// cached entry; if the read's cache fill lands after that drop, the purged
// document reads back as present for good, as the cache has no TTL.
func TestClient_DocCacheUnderReadRacingPurge(t *testing.T) {
	ctx := context.Background()
	cli := setupTestWithDummyData(t)

	info, err := cli.ActivateClient(ctx, dummyProjectID, t.Name(), nil)
	require.NoError(t, err)

	for i := range 50 {
		docInfo, err := cli.FindOrCreateDocInfo(
			ctx,
			info.RefKey(),
			key.Key(fmt.Sprintf("tests$%s-%s-%d", t.Name(), info.ID, i)),
			false,
		)
		require.NoError(t, err)
		refKey := docInfo.RefKey()

		// Creating the document cached it. Drop that entry so the read below
		// is the miss that reads MongoDB and fills the cache, which is the
		// race the purge has to survive.
		cli.InvalidateCache(types.CacheTypeDocument, refKey.CacheKey())

		var readErr, purgeErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, readErr = cli.FindDocInfoByRefKey(ctx, refKey) })
		wg.Go(func() { _, purgeErr = cli.PurgeDocument(ctx, refKey) })
		wg.Wait()

		require.NoError(t, purgeErr)
		// The read may win or lose the race, but losing it must report the
		// document as gone rather than as some other failure.
		if readErr != nil {
			require.ErrorIs(t, readErr, database.ErrDocumentNotFound)
		}

		_, err = cli.FindDocInfoByRefKey(ctx, refKey)
		require.ErrorIs(t, err, database.ErrDocumentNotFound, "iteration %d", i)
	}
}

func TestClient_CompactChangeInfosAcrossNodes(t *testing.T) {
	// Two clients on one database stand in for two server nodes: each keeps
	// its own document cache.
	nodeA := setupTestWithDummyData(t)
	nodeB := setupTestWithDummyData(t)

	testcases.RunCompactChangeInfosAcrossNodesTest(t, nodeA, nodeB, dummyProjectID)
}

func TestClient_RotateProjectKeys(t *testing.T) {
	t.Run("success: should rotate project API keys", func(t *testing.T) {
		// Given
		ctx := context.Background()
		client := setupTestWithDummyData(t)

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

// TestClient_BackfillUserSubjects checks that users written before the
// subject existed get their username as subject when the server starts, so
// GitHub users keep their accounts and the unique index can be built.
func TestClient_BackfillUserSubjects(t *testing.T) {
	ctx := context.Background()
	config := newTestConfig(t)
	require.NoError(t, config.Validate())

	raw, err := mongodriver.Connect(options.Client().ApplyURI(config.ConnectionURI))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, raw.Disconnect(ctx)) })

	users := raw.Database(config.YorkieDatabase).Collection(mongo.ColUsers)
	result, err := users.InsertOne(ctx, bson.M{
		"username":      "legacy-github-user",
		"auth_provider": database.AuthProviderGitHub,
	})
	require.NoError(t, err)
	legacyID := types.ID(result.InsertedID.(bson.ObjectID).Hex())
	_, err = users.InsertOne(ctx, bson.M{
		"username":        "legacy-local-user",
		"hashed_password": "hashed-password",
	})
	require.NoError(t, err)

	cli, err := mongo.Dial(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cli.Close()) })

	github, err := cli.GetOrCreateUserInfoByProvider(
		ctx, database.AuthProviderGitHub, "legacy-github-user", "legacy-github-user",
	)
	require.NoError(t, err)
	assert.Equal(t, legacyID, github.ID)
	assert.Equal(t, "legacy-github-user", github.Subject)

	local, err := cli.FindUserInfoByName(ctx, "legacy-local-user")
	require.NoError(t, err)
	assert.Equal(t, "legacy-local-user", local.Subject)

	// An older server writes a GitHub user without a subject after this
	// server started, during a rolling update.
	result, err = users.InsertOne(ctx, bson.M{
		"username":      "late-github-user",
		"auth_provider": database.AuthProviderGitHub,
	})
	require.NoError(t, err)
	lateID := types.ID(result.InsertedID.(bson.ObjectID).Hex())

	late, err := cli.GetOrCreateUserInfoByProvider(
		ctx, database.AuthProviderGitHub, "late-github-user", "late-github-user",
	)
	require.NoError(t, err)
	assert.Equal(t, lateID, late.ID)

	// The same backfill never hands a user of one provider to another.
	_, err = users.InsertOne(ctx, bson.M{
		"username":      "late-other-user",
		"auth_provider": "external:other",
	})
	require.NoError(t, err)
	_, err = cli.GetOrCreateUserInfoByProvider(
		ctx, database.AuthProviderGitHub, "late-other-user", "late-other-user",
	)
	assert.ErrorIs(t, err, database.ErrUserAuthProviderMismatch)
}
