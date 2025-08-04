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
	"strconv"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mongodb "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	dummyOwnerID              = types.ID("000000000000000000000000")
	otherOwnerID              = types.ID("000000000000000000000001")
	dummyClientID             = types.ID("000000000000000000000000")
	clientDeactivateThreshold = "1h"

	nodeIDOne = "node-1"
	nodeIDTwo = "node-2"
)

func RunLeadershipTest(
	t *testing.T,
	db database.Database,
) {
	t.Run("TryLeadership should work for new leadership", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		leaseDuration := 30 * gotime.Second

		info, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)
		assert.Equal(t, nodeIDOne, info.Hostname)
		assert.NotEmpty(t, info.LeaseToken)
		assert.Equal(t, int64(1), info.Term)
		assert.False(t, info.IsExpired())
	})

	t.Run("TryLeadership should return existing leader when valid", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		// First node acquires leadership
		leaseDuration := 30 * gotime.Second

		info1, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)

		// Second node tries to acquire leadership
		info2, err := db.TryLeadership(ctx, nodeIDTwo, "", leaseDuration)
		require.NoError(t, err)

		// Should return the first node's leadership
		assert.Equal(t, nodeIDOne, info2.Hostname)
		assert.Equal(t, info1.LeaseToken, info2.LeaseToken)
	})

	t.Run("TryLeadership should allow takeover after expiry", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		// First node acquires leadership with short lease
		shortLease := 100 * gotime.Millisecond

		_, err := db.TryLeadership(ctx, nodeIDOne, "", shortLease)
		require.NoError(t, err)

		// Wait for lease to expire
		gotime.Sleep(150 * gotime.Millisecond)

		// Second node acquires leadership
		leaseDuration := 30 * gotime.Second

		info, err := db.TryLeadership(ctx, nodeIDTwo, "", leaseDuration)
		require.NoError(t, err)

		assert.Equal(t, nodeIDTwo, info.Hostname)
		assert.Equal(t, int64(2), info.Term) // Term should increment
	})

	t.Run("TryLeadership should work for renewal with valid token", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		leaseDuration := 30 * gotime.Second

		// Acquire leadership
		info, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)

		// Renew leadership
		renewedInfo, err := db.TryLeadership(ctx, nodeIDOne, info.LeaseToken, leaseDuration)
		require.NoError(t, err)

		assert.Equal(t, nodeIDOne, renewedInfo.Hostname)
		assert.NotEqual(t, info.LeaseToken, renewedInfo.LeaseToken) // Token should change
		// NOTE(raararaara): Because expires_at is based on MongoDB server time ($$NOW),
		// and renewal requests can occur within the same millisecond,
		// expires_at may not strictly increase. Token change confirms renewal.
		assert.True(t, renewedInfo.ExpiresAt.Compare(info.ExpiresAt) >= 0) // Expiry should extend
		assert.Equal(t, info.Term, renewedInfo.Term)                       // Term should stay same
	})

	t.Run("TryLeadership should fail with invalid token", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		leaseDuration := 30 * gotime.Second

		// Acquire leadership
		_, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)

		// Try to renew with invalid token
		_, err = db.TryLeadership(ctx, nodeIDOne, "invalid-token", leaseDuration)
		assert.ErrorIs(t, err, database.ErrInvalidLeaseToken)
	})

	t.Run("TryLeadership should fail for wrong node with token", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		leaseDuration := 30 * gotime.Second

		// Acquire leadership
		info, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)

		// Try to renew from different node
		_, err = db.TryLeadership(ctx, nodeIDTwo, info.LeaseToken, leaseDuration)
		assert.ErrorIs(t, err, database.ErrInvalidLeaseToken)
	})

	t.Run("FindLeadership should return current leader", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, db.ClearLeadership(ctx))

		// No leadership initially
		info, err := db.FindLeadership(ctx)
		require.NoError(t, err)
		assert.Nil(t, info)

		// Acquire leadership
		leaseDuration := 30 * gotime.Second

		acquired, err := db.TryLeadership(ctx, nodeIDOne, "", leaseDuration)
		require.NoError(t, err)

		// Get leadership should return the same info
		info, err = db.FindLeadership(ctx)
		require.NoError(t, err)
		require.NotNil(t, info)

		assert.Equal(t, acquired.Hostname, info.Hostname)
		assert.Equal(t, acquired.LeaseToken, info.LeaseToken)
		assert.Equal(t, acquired.Term, info.Term)
	})
}

// RunFindDocInfoTest runs the FindDocInfo test for the given db.
func RunFindDocInfoTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("find docInfo test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		_, err = db.FindDocInfoByRefKey(ctx, types.DocRefKey{
			ProjectID: projectID,
			DocID:     dummyClientID,
		})
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		firstInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)
		assert.Equal(t, docKey, firstInfo.Key)

		secondInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)
		assert.Equal(t, docKey, secondInfo.Key)
		assert.Equal(t, firstInfo.ID, secondInfo.ID)
	})

	t.Run("find docInfo by key test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		// 01. Create a document
		docKey := helper.TestDocKey(t)
		info, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)
		assert.Equal(t, docKey, info.Key)

		// 02. Remove the document
		_, _, err = db.CreateChangeInfos(ctx, info.RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)

		// 03. Find the document
		info, err = db.FindDocInfoByKey(ctx, projectID, docKey)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)
	})
}

// RunFindDocInfosByKeysTest runs the FindDocInfosByKeys test for the given db.
func RunFindDocInfosByKeysTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("find docInfos by keys test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		// 01. Create documents
		docKeys := []key.Key{
			"test", "test$3", "test123", "test$0",
			"search$test", "abcde", "test abc",
		}
		for _, docKey := range docKeys {
			_, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
			assert.NoError(t, err)
		}

		// 02. Find documents
		infos, err := db.FindDocInfosByKeys(ctx, projectID, docKeys)
		assert.NoError(t, err)

		actualKeys := make([]key.Key, len(infos))
		for i, info := range infos {
			actualKeys[i] = info.Key
		}

		assert.ElementsMatch(t, docKeys, actualKeys)
		assert.Len(t, infos, len(docKeys))
	})

	t.Run("find docInfos by empty key slice test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		// 01. Create documents
		docKeys := []key.Key{
			"test", "test$3", "test123", "test$0",
			"search$test", "abcde", "test abc",
		}
		for _, docKey := range docKeys {
			_, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
			assert.NoError(t, err)
		}

		// 02. Find documents
		infos, err := db.FindDocInfosByKeys(ctx, projectID, nil)
		assert.NoError(t, err)
		assert.Len(t, infos, 0)
	})

	t.Run("find docInfos by keys where some keys are not found test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		// 01. Create documents
		docKeys := []key.Key{
			"exist-key1", "exist-key2", "exist-key3",
		}
		for _, docKey := range docKeys {
			_, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
			assert.NoError(t, err)
		}

		// 02. append a key that does not exist
		docKeysWithNonExistKey := append(docKeys, "non-exist-key")

		// 03. Find documents
		infos, err := db.FindDocInfosByKeys(ctx, projectID, docKeysWithNonExistKey)
		assert.NoError(t, err)

		actualKeys := make([]key.Key, len(infos))
		for i, info := range infos {
			actualKeys[i] = info.Key
		}

		assert.ElementsMatch(t, docKeys, actualKeys)
		assert.Len(t, infos, len(docKeys))
	})
}

// RunFindProjectInfoBySecretKeyTest runs the FindProjectInfoBySecretKey test for the given db.
func RunFindProjectInfoBySecretKeyTest(
	t *testing.T,
	db database.Database,
) {
	t.Run("FindProjectInfoBySecretKey test", func(t *testing.T) {
		ctx := context.Background()

		username := "admin@yorkie.dev"
		password := "hashed-password"

		_, project, err := db.EnsureDefaultUserAndProject(ctx, username, password, clientDeactivateThreshold)
		assert.NoError(t, err)

		info2, err := db.FindProjectInfoBySecretKey(ctx, project.SecretKey)
		assert.NoError(t, err)

		assert.Equal(t, project.ID, info2.ID)
	})
}

// RunFindProjectInfoByNameTest runs the FindProjectInfoByName test for the given db.
func RunFindProjectInfoByNameTest(
	t *testing.T,
	db database.Database,
) {
	t.Run("project test", func(t *testing.T) {
		ctx := context.Background()
		suffixes := []int{0, 1, 2}
		for _, suffix := range suffixes {
			_, err := db.CreateProjectInfo(
				ctx,
				fmt.Sprintf("%s-%d", t.Name(), suffix),
				dummyOwnerID,
				clientDeactivateThreshold,
			)
			assert.NoError(t, err)
		}

		_, err := db.CreateProjectInfo(ctx, t.Name(), otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		// Lists all projects that the dummyOwnerID is the owner.
		projects, err := db.ListProjectInfos(ctx, dummyOwnerID)
		assert.NoError(t, err)
		assert.Len(t, projects, len(suffixes))

		_, err = db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		project, err := db.FindProjectInfoByName(ctx, dummyOwnerID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, project.Name, t.Name())

		newName := fmt.Sprintf("%s-%d", t.Name(), 3)
		fields := &types.UpdatableProjectFields{Name: &newName}
		_, err = db.UpdateProjectInfo(ctx, dummyOwnerID, project.ID, fields)
		assert.NoError(t, err)
		_, err = db.FindProjectInfoByName(ctx, dummyOwnerID, newName)
		assert.NoError(t, err)
	})

	t.Run("FindProjectInfoByName test", func(t *testing.T) {
		ctx := context.Background()

		info1, err := db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		_, err = db.CreateProjectInfo(ctx, t.Name(), otherOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		info2, err := db.FindProjectInfoByName(ctx, dummyOwnerID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, info1.ID, info2.ID)
	})
}

// RunFindDocInfosByQueryTest runs the FindDocInfosByQuery test for the given db.
func RunFindDocInfosByQueryTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("search docInfos test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKeys := []string{
			"test", "test$3", "test-search", "test$0",
			"search$test", "abcde", "test abc",
			"test0", "test1", "test2", "test3", "test10",
			"test11", "test20", "test21", "test22", "test23"}
		for _, docKey := range docKeys {
			_, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), key.Key(docKey))
			assert.NoError(t, err)
		}

		res, err := db.FindDocInfosByQuery(ctx, projectID, "test", 10)
		assert.NoError(t, err)

		var keys []string
		for _, info := range res.Elements {
			keys = append(keys, info.Key.String())
		}

		assert.EqualValues(t, []string{
			"test", "test abc", "test$0", "test$3", "test-search",
			"test0", "test1", "test10", "test11", "test2"}, keys)
		assert.Equal(t, 15, res.TotalCount)
	})
}

// RunFindChangesBetweenServerSeqsTest runs the FindChangesBetweenServerSeqs test for the given db.
func RunFindChangesBetweenServerSeqsTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("insert and find changes test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		refKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := range 10 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()

		// Store changes
		_, _, err := db.CreateChangeInfos(
			ctx,
			docInfo.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)

		// Find changes
		loadedChanges, err := db.FindChangesBetweenServerSeqs(
			ctx,
			refKey,
			6,
			10,
		)
		assert.NoError(t, err)
		assert.Len(t, loadedChanges, 5)
	})
}

// RunFindChangeInfosBetweenServerSeqsTest runs the FindChangeInfosBetweenServerSeqs test for the given db.
func RunFindChangeInfosBetweenServerSeqsTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("continues editing without any interference from other users test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		updatedClientInfo, _ := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())

		// Record the serverSeq value at the time the PushPull request came in.
		initialServerSeq := docInfo.ServerSeq

		// The serverSeq of the checkpoint that the server has should always be the same as
		// the serverSeq of the user's checkpoint that came in as a request, if no other user interfered.
		reqPackCheckpointServerSeq := updatedClientInfo.Checkpoint(docInfo.ID).ServerSeq

		changeInfos, err := db.FindChangeInfosBetweenServerSeqs(
			ctx,
			docInfo.RefKey(),
			reqPackCheckpointServerSeq+1,
			initialServerSeq,
		)

		assert.NoError(t, err)
		assert.Len(t, changeInfos, 0)
	})

	t.Run("retrieving a document with snapshot that reflect the latest doc info test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		refKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 01. Create a document and store changes
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := range 5 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}

		pack := doc.CreateChangePack()

		_, _, err := db.CreateChangeInfos(
			ctx,
			docInfo.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)

		// 02. Create a snapshot that reflect the latest doc info
		updatedDocInfo, _ := db.FindDocInfoByRefKey(ctx, refKey)
		assert.Equal(t, int64(6), updatedDocInfo.ServerSeq)

		pack = change.NewPack(
			updatedDocInfo.Key,
			change.InitialCheckpoint.NextServerSeq(updatedDocInfo.ServerSeq),
			nil,
			doc.VersionVector(),
			nil,
		)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.Equal(t, int64(6), doc.Checkpoint().ServerSeq)

		assert.NoError(t, db.CreateSnapshotInfo(ctx, refKey, doc.InternalDocument()))

		// 03. Find changeInfos with snapshot that reflect the latest doc info
		snapshotInfo, _ := db.FindClosestSnapshotInfo(
			ctx,
			refKey,
			updatedDocInfo.ServerSeq,
			false,
		)

		changeInfos, _ := db.FindChangeInfosBetweenServerSeqs(
			ctx,
			refKey,
			snapshotInfo.ServerSeq+1,
			updatedDocInfo.ServerSeq,
		)

		assert.Len(t, changeInfos, 0)
	})

	t.Run("store changes and find changes test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		refKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 01. Create a document and store changes
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := range 5 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()

		_, _, err := db.CreateChangeInfos(
			ctx,
			docInfo.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)

		// 02. Find changes
		changeInfos, err := db.FindChangeInfosBetweenServerSeqs(
			ctx,
			refKey,
			1,
			6,
		)
		assert.NoError(t, err)
		assert.Len(t, changeInfos, 6)

		changeInfos, err = db.FindChangeInfosBetweenServerSeqs(
			ctx,
			refKey,
			3,
			3,
		)
		assert.NoError(t, err)
		assert.Len(t, changeInfos, 1)
	})
}

// RunFindLatestChangeInfoTest runs the FindLatestChangeInfoByActor test for the given db.
func RunFindLatestChangeInfoTest(t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("store changes and find latest changeInfo test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		// 01. Activate client and find document info.
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)
		refKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 02. Create a document and store changes.
		bytesID, err := clientInfo.ID.Bytes()
		assert.NoError(t, err)
		actorID, _ := time.ActorIDFromBytes(bytesID)
		assert.NoError(t, err)

		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := range 5 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()
		_, _, err = db.CreateChangeInfos(
			ctx,
			docInfo.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)

		// 03. Find all changes and determine the maximum Lamport timestamp.
		changes, err := db.FindChangesBetweenServerSeqs(ctx, refKey, 1, 10)
		assert.NoError(t, err)
		maxLamport := int64(0)
		for _, ch := range changes {
			maxLamport = max(maxLamport, ch.ID().Lamport())
		}

		// 04. Find the latest change info by actor before the given server sequence.
		latestChangeInfo, err := db.FindLatestChangeInfoByActor(
			ctx,
			refKey,
			types.ID(actorID.String()),
			10,
		)
		assert.NoError(t, err)
		assert.Equal(t, maxLamport, latestChangeInfo.Lamport)
	})
}

// RunFindClosestSnapshotInfoTest runs the FindClosestSnapshotInfo test for the given db.
func RunFindClosestSnapshotInfoTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("store and find snapshots test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)

		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))

		docRefKey := docInfo.RefKey()

		assert.NoError(t, db.CreateSnapshotInfo(ctx, docRefKey, doc.InternalDocument()))
		snapshot, err := db.FindClosestSnapshotInfo(ctx, docRefKey, change.MaxCheckpoint.ServerSeq, true)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), snapshot.ServerSeq)

		pack := change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(1), nil, doc.VersionVector(), nil)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.NoError(t, db.CreateSnapshotInfo(ctx, docRefKey, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docRefKey, change.MaxCheckpoint.ServerSeq, true)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), snapshot.ServerSeq)

		pack = change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(2), nil, doc.VersionVector(), nil)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.NoError(t, db.CreateSnapshotInfo(ctx, docRefKey, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docRefKey, change.MaxCheckpoint.ServerSeq, true)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), snapshot.ServerSeq)

		snapshot, err = db.FindClosestSnapshotInfo(ctx, docRefKey, 1, true)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), snapshot.ServerSeq)
	})
}

// RunListUserInfosTest runs the ListUserInfos test for the given db.
func RunListUserInfosTest(t *testing.T, db database.Database) {
	t.Run("user test", func(t *testing.T) {
		ctx := context.Background()
		username := "admin@yorkie.dev"
		password := "hashed-password"

		info, err := db.CreateUserInfo(ctx, username, password)
		assert.NoError(t, err)
		assert.Equal(t, username, info.Username)

		_, err = db.CreateUserInfo(ctx, username, password)
		assert.ErrorIs(t, err, database.ErrUserAlreadyExists)

		infos, err := db.ListUserInfos(ctx)
		assert.NoError(t, err)
		assert.Len(t, infos, 1)
		assert.Equal(t, infos[0], info)
	})
}

// RunFindUserInfoByIDTest runs the FindUserInfoByID test for the given db.
func RunFindUserInfoByIDTest(t *testing.T, db database.Database) {
	t.Run("RunFindUserInfoByID test", func(t *testing.T) {
		ctx := context.Background()

		username := "findUserInfoTestAccount"
		password := "temporary-password"

		user, _, err := db.EnsureDefaultUserAndProject(ctx, username, password, clientDeactivateThreshold)
		assert.NoError(t, err)

		info1, err := db.FindUserInfoByID(ctx, user.ID)
		assert.NoError(t, err)

		assert.Equal(t, user.ID, info1.ID)
	})
}

// RunFindUserInfoByNameTest runs the FindUserInfoByName test for the given db.
func RunFindUserInfoByNameTest(t *testing.T, db database.Database) {
	t.Run("RunFindUserInfoByName test", func(t *testing.T) {
		ctx := context.Background()

		username := "findUserInfoTestAccount"
		password := "temporary-password"

		user, _, err := db.EnsureDefaultUserAndProject(ctx, username, password, clientDeactivateThreshold)
		assert.NoError(t, err)

		info1, err := db.FindUserInfoByName(ctx, user.Username)
		assert.NoError(t, err)

		assert.Equal(t, user.ID, info1.ID)
	})
}

// RunActivateClientDeactivateClientTest runs the ActivateClient and DeactivateClient tests for the given db.
func RunActivateClientDeactivateClientTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("activate and find client test", func(t *testing.T) {
		ctx := context.Background()
		_, err := db.FindClientInfoByRefKey(ctx, types.ClientRefKey{
			ProjectID: projectID,
			ClientID:  dummyClientID,
		})
		assert.ErrorIs(t, err, database.ErrClientNotFound)

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		found, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, clientInfo.Key, found.Key)
	})

	t.Run("activate/deactivate client test", func(t *testing.T) {
		ctx := context.Background()

		// try to deactivate the client with not exists ID.
		_, err := db.DeactivateClient(ctx, types.ClientRefKey{
			ProjectID: projectID,
			ClientID:  dummyClientID,
		})
		assert.ErrorIs(t, err, database.ErrClientNotFound)

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientActivated, clientInfo.Status)

		// try to activate the client twice.
		clientInfo, err = db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientActivated, clientInfo.Status)

		clientInfo, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientDeactivated, clientInfo.Status)

		// already deactivated
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.ErrorIs(t, err, database.ErrClientNotFound)
	})
}

// RunTryAttachingAndDeactivateClientTest runs the TryAttaching and DeactivateClient tests for the given db.
func RunTryAttachingAndDeactivateClientTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("TryAttaching test", func(t *testing.T) {
		ctx := context.Background()

		// 01. activate a client
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), nil)
		assert.NoError(t, err)

		// 02. create a document
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), key.Key(fmt.Sprintf("tests$%s", t.Name())))
		assert.NoError(t, err)

		// 03. success case: client is activated, and document is not attached
		_, err = db.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		assert.NoError(t, err)

		// 04. success case: client is activated, and document is already attaching
		_, err = db.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		assert.NoError(t, err)

		// 05. failure case: client is activated, but document is already attached
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.NoError(t, err)
		_, err = db.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		assert.Error(t, err)

		// 06. failure case: client is deactivated
		assert.NoError(t, clientInfo.DetachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		_, err = db.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		assert.Error(t, err)
	})

	t.Run("DeactivateClient test", func(t *testing.T) {
		ctx := context.Background()

		// 01. success case: client is activated
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), nil)
		assert.NoError(t, err)
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)

		// 02. failure case: client has attaching document
		clientInfo, err = db.ActivateClient(ctx, projectID, t.Name(), nil)
		assert.NoError(t, err)
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), key.Key(fmt.Sprintf("tests$%s", t.Name())))
		assert.NoError(t, err)
		_, err = db.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		assert.NoError(t, err)
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.Error(t, err)

		// 03. failure case: client has attached document
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.NoError(t, err)
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.Error(t, err)

		// 04. failure case: client is already deactivated
		assert.NoError(t, clientInfo.DetachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		_, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.Error(t, err)
	})
}

// RunUpdateProjectInfoTest runs the UpdateProjectInfo tests for the given db.
func RunUpdateProjectInfoTest(t *testing.T, db database.Database) {
	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		ctx := context.Background()
		existName := "already"
		newName := "changed-name"
		newName2 := newName + "2"
		newAuthWebhookURL := "http://localhost:3000"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		newEventWebhookURL := "http://localhost:4000"
		newEventWebhookEvents := []string{string(types.DocRootChanged)}
		newClientDeactivateThreshold := "1h"

		info, err := db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)
		_, err = db.CreateProjectInfo(ctx, existName, dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		id := info.ID

		// 01. Update all fields test
		fields := &types.UpdatableProjectFields{
			Name:                      &newName,
			AuthWebhookURL:            &newAuthWebhookURL,
			AuthWebhookMethods:        &newAuthWebhookMethods,
			EventWebhookURL:           &newEventWebhookURL,
			EventWebhookEvents:        &newEventWebhookEvents,
			ClientDeactivateThreshold: &newClientDeactivateThreshold,
		}
		assert.NoError(t, fields.Validate())
		res, err := db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err := db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newName, updateInfo.Name)
		assert.Equal(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)
		assert.Equal(t, newEventWebhookURL, updateInfo.EventWebhookURL)
		assert.Equal(t, newEventWebhookEvents, updateInfo.EventWebhookEvents)

		// 02. Update name field test
		fields = &types.UpdatableProjectFields{
			Name: &newName2,
		}
		assert.NoError(t, fields.Validate())
		res, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err = db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.NotEqual(t, newName, updateInfo.Name)
		assert.Equal(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newEventWebhookURL, updateInfo.EventWebhookURL)
		assert.Equal(t, newEventWebhookEvents, updateInfo.EventWebhookEvents)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 03. Update authWebhookURL and eventWebhookURL test
		newEventWebhookURL2 := newEventWebhookURL + "2"
		newAuthWebhookURL2 := newAuthWebhookURL + "2"
		fields = &types.UpdatableProjectFields{
			EventWebhookURL: &newEventWebhookURL2,
			AuthWebhookURL:  &newAuthWebhookURL2,
		}
		assert.NoError(t, fields.Validate())
		res, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err = db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newName2, updateInfo.Name)
		assert.NotEqual(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookURL2, updateInfo.AuthWebhookURL)
		assert.NotEqual(t, newEventWebhookURL, updateInfo.EventWebhookURL)
		assert.Equal(t, newEventWebhookURL2, updateInfo.EventWebhookURL)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 04. Update EventWebhookEvents test
		var newEventWebhookEvents2 []string
		newAuthWebhookMethods2 := []string{
			string(types.DetachDocument),
			string(types.PushPull),
		}
		fields = &types.UpdatableProjectFields{
			AuthWebhookMethods: &newAuthWebhookMethods2,
			EventWebhookEvents: &newEventWebhookEvents2,
		}
		assert.NoError(t, fields.Validate())
		res, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err = db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newEventWebhookEvents2, updateInfo.EventWebhookEvents)
		assert.Equal(t, newAuthWebhookMethods2, updateInfo.AuthWebhookMethods)

		// 05. Update clientDeactivateThreshold test
		clientDeactivateThreshold2 := "2h"
		fields = &types.UpdatableProjectFields{
			ClientDeactivateThreshold: &clientDeactivateThreshold2,
		}
		assert.NoError(t, fields.Validate())
		res, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err = db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newName2, updateInfo.Name)
		assert.Equal(t, newAuthWebhookURL2, updateInfo.AuthWebhookURL)
		assert.Equal(t, newEventWebhookURL2, updateInfo.EventWebhookURL)
		assert.Equal(t, newAuthWebhookMethods2, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newEventWebhookEvents2, updateInfo.EventWebhookEvents)
		assert.NotEqual(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 06. Duplicated name test
		fields = &types.UpdatableProjectFields{Name: &existName}
		_, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.ErrorIs(t, err, database.ErrProjectNameAlreadyExists)

		// 07. OwnerID not match test
		fields = &types.UpdatableProjectFields{Name: &existName}
		_, err = db.UpdateProjectInfo(ctx, otherOwnerID, id, fields)
		assert.ErrorIs(t, err, database.ErrProjectNotFound)
	})
}

// RunFindDocInfosByPagingTest runs the FindDocInfosByPaging tests for the given db.
func RunFindDocInfosByPagingTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("simple FindDocInfosByPaging test", func(t *testing.T) {
		ctx := context.Background()

		pageSize := 5
		totalSize := 9
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfos := make([]*database.DocInfo, 0, totalSize)
		for i := range totalSize {
			docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), key.Key(fmt.Sprintf("%d", i)))
			assert.NoError(t, err)
			docInfos = append(docInfos, docInfo)
		}

		docKeys := make([]key.Key, 0, totalSize)
		docKeysInReverse := make([]key.Key, 0, totalSize)
		for _, docInfo := range docInfos {
			docKeys = append(docKeys, docInfo.Key)
			docKeysInReverse = append([]key.Key{docInfo.Key}, docKeysInReverse...)
		}

		// initial page, offset is empty
		infos, err := db.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{PageSize: pageSize})
		assert.NoError(t, err)
		AssertKeys(t, docKeysInReverse[:pageSize], infos)

		// backward
		infos, err = db.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:   infos[len(infos)-1].ID,
			PageSize: pageSize,
		})
		assert.NoError(t, err)
		AssertKeys(t, docKeysInReverse[pageSize:], infos)

		// backward again
		emptyInfos, err := db.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:   infos[len(infos)-1].ID,
			PageSize: pageSize,
		})
		assert.NoError(t, err)
		AssertKeys(t, nil, emptyInfos)

		// forward
		infos, err = db.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:    infos[0].ID,
			PageSize:  pageSize,
			IsForward: true,
		})
		assert.NoError(t, err)
		AssertKeys(t, docKeys[totalSize-pageSize:], infos)

		// forward again
		emptyInfos, err = db.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:    infos[len(infos)-1].ID,
			PageSize:  pageSize,
			IsForward: true,
		})
		assert.NoError(t, err)
		AssertKeys(t, nil, emptyInfos)
	})

	t.Run("complex FindDocInfosByPaging test", func(t *testing.T) {
		const testDocCnt = 25
		ctx := context.Background()

		// dummy project setup
		testProjectInfo, err := db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		// dummy document setup
		var dummyDocInfos []*database.DocInfo
		for i := 0; i <= testDocCnt; i++ {
			testDocKey := key.Key(fmt.Sprintf("%s%02d", "testdockey", i))
			docInfo, err := db.FindOrCreateDocInfo(ctx, types.ClientRefKey{
				ProjectID: testProjectInfo.ID,
				ClientID:  dummyClientID,
			}, testDocKey)
			assert.NoError(t, err)
			dummyDocInfos = append(dummyDocInfos, docInfo)
		}

		cases := []struct {
			name       string
			offset     types.ID
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
				offset:     dummyDocInfos[13].ID,
				pageSize:   0,
				isForward:  false,
				testResult: helper.NewRangeSlice(12, 0),
			},
			{
				name:       "FindDocInfosByPaging --forward --offset test",
				offset:     dummyDocInfos[13].ID,
				pageSize:   0,
				isForward:  true,
				testResult: helper.NewRangeSlice(14, testDocCnt),
			},
			{
				name:       "FindDocInfosByPaging --size --offset test",
				offset:     dummyDocInfos[13].ID,
				pageSize:   10,
				isForward:  false,
				testResult: helper.NewRangeSlice(12, 3),
			},
			{
				name:       "FindDocInfosByPaging --size --forward --offset test",
				offset:     dummyDocInfos[13].ID,
				pageSize:   10,
				isForward:  true,
				testResult: helper.NewRangeSlice(14, 23),
			},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				ctx := context.Background()
				testPaging := types.Paging[types.ID]{
					Offset:    c.offset,
					PageSize:  c.pageSize,
					IsForward: c.isForward,
				}

				docInfos, err := db.FindDocInfosByPaging(ctx, testProjectInfo.ID, testPaging)
				assert.NoError(t, err)

				for idx, docInfo := range docInfos {
					resultIdx := c.testResult[idx]
					assert.Equal(t, dummyDocInfos[resultIdx].Key, docInfo.Key)
					assert.Equal(t, dummyDocInfos[resultIdx].ID, docInfo.ID)
					assert.Equal(t, dummyDocInfos[resultIdx].ProjectID, docInfo.ProjectID)
				}
			})
		}
	})

	t.Run("FindDocInfosByPaging with docInfoRemovedAt test", func(t *testing.T) {
		const testDocCnt = 5
		ctx := context.Background()

		// 01. Initialize a project and create documents.
		projectInfo, err := db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		var docInfos []*database.DocInfo
		for i := range testDocCnt {
			testDocKey := key.Key("key" + strconv.Itoa(i))
			docInfo, err := db.FindOrCreateDocInfo(ctx, types.ClientRefKey{
				ProjectID: projectInfo.ID,
				ClientID:  dummyClientID,
			}, testDocKey)
			assert.NoError(t, err)
			docInfos = append(docInfos, docInfo)
		}

		docKeysInReverse := make([]key.Key, 0, testDocCnt)
		for _, docInfo := range docInfos {
			docKeysInReverse = append([]key.Key{docInfo.Key}, docKeysInReverse...)
		}

		// 02. List the documents.
		result, err := db.FindDocInfosByPaging(ctx, projectInfo.ID, types.Paging[types.ID]{
			PageSize:  10,
			IsForward: false,
		})
		assert.NoError(t, err)
		assert.Len(t, result, len(docInfos))
		AssertKeys(t, docKeysInReverse, result)

		// 03. Remove some documents.
		_, _, err = db.CreateChangeInfos(ctx, docInfos[1].RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)
		_, _, err = db.CreateChangeInfos(ctx, docInfos[3].RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)

		// 04. List the documents again and check the filtered result.
		result, err = db.FindDocInfosByPaging(ctx, projectInfo.ID, types.Paging[types.ID]{
			PageSize:  10,
			IsForward: false,
		})
		assert.NoError(t, err)
		assert.Len(t, result, len(docInfos)-2)
		AssertKeys(t, []key.Key{docKeysInReverse[0], docKeysInReverse[2], docKeysInReverse[4]}, result)
	})
}

// RunCreateChangeInfosTest runs the CreateChangeInfos tests for the given db.
func RunCreateChangeInfosTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("set RemovedAt in docInfo test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)

		// 01. Create a client and a document then attach the document to the client.
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 02. Remove the document and check the document is removed.
		_, _, err := db.CreateChangeInfos(ctx, docInfo.RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)
		docInfo, err = db.FindDocInfoByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, false, docInfo.RemovedAt.IsZero())
	})

	t.Run("reuse same key to create docInfo test ", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)

		// 01. Create a client and a document then attach the document to the client.
		clientInfo1, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo1, _ := db.FindOrCreateDocInfo(ctx, clientInfo1.RefKey(), docKey)
		docRefKey1 := docInfo1.RefKey()
		assert.NoError(t, clientInfo1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo1))

		// 02. Remove the document.
		assert.NoError(t, clientInfo1.RemoveDocument(docRefKey1.DocID))
		_, _, err := db.CreateChangeInfos(ctx, docInfo1.RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)

		// 03. Create a document with same key and check they have same key but different id.
		docInfo2, _ := db.FindOrCreateDocInfo(ctx, clientInfo1.RefKey(), docKey)
		docRefKey2 := docInfo2.RefKey()
		assert.NoError(t, clientInfo1.AttachDocument(docRefKey2.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo2))
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
	})

	t.Run("set removed_at in docInfo test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// Set removed_at in docInfo and store changes
		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		_, _, err := db.CreateChangeInfos(ctx, docInfo.RefKey(), change.InitialCheckpoint, []*database.ChangeInfo{}, true)
		assert.NoError(t, err)

		// Check whether removed_at is set in docInfo
		docInfo, err = db.FindDocInfoByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.NotEqual(t, gotime.Time{}, docInfo.RemovedAt)

		// Check whether DocumentRemoved status is set in clientInfo
		clientInfo, err = db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		assert.NotEqual(t, database.DocumentRemoved, clientInfo.Documents[docInfo.ID].Status)
	})

	t.Run("set updated_at in docInfo test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)

		// 01. Create a client and a document then attach the document to the client.
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo1, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.Equal(t, docInfo1.Owner, clientInfo.ID)
		assert.NotEqual(t, gotime.Date(1, gotime.January, 1, 0, 0, 0, 0, gotime.UTC), docInfo1.UpdatedAt)
		assert.Equal(t, docInfo1.CreatedAt, docInfo1.UpdatedAt)
		refKey := docInfo1.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(refKey.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo1))

		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)

		// 02. Update document only presence
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("key", "val")
			return nil
		}))
		pack := doc.CreateChangePack()
		updatedAt := docInfo1.UpdatedAt
		_, _, err := db.CreateChangeInfos(
			ctx,
			docInfo1.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)
		docInfo2, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.Equal(t, updatedAt, docInfo2.UpdatedAt)

		// 03. Update document presence and operation
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("key", "val")
			root.SetNewArray("array")
			return nil
		}))
		pack = doc.CreateChangePack()
		updatedAt = docInfo2.UpdatedAt
		_, _, err = db.CreateChangeInfos(
			ctx,
			docInfo2.RefKey(),
			pack.Checkpoint,
			toChangeInfos(t, refKey, pack.Changes),
			false,
		)
		assert.NoError(t, err)
		docInfo3, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NotEqual(t, updatedAt, docInfo3.UpdatedAt)
	})
}

// RunUpdateClientInfoAfterPushPullTest runs the UpdateClientInfoAfterPushPull tests for the given db.
func RunUpdateClientInfoAfterPushPullTest(t *testing.T, db database.Database, projectID types.ID) {
	dummyClientID := types.ID("000000000000000000000000")
	ctx := context.Background()

	t.Run("document is not attached in clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
	})

	t.Run("document attach test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("update server_seq and client_seq in clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		// update with larger seq
		clientInfo.Documents[docInfo.ID].ServerSeq = 3
		clientInfo.Documents[docInfo.ID].ClientSeq = 5
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(3))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(5))
		assert.NoError(t, err)

		// update with smaller seq(should be ignored)
		clientInfo.Documents[docInfo.ID].ServerSeq = 2
		clientInfo.Documents[docInfo.ID].ClientSeq = 3
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(3))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(5))
		assert.NoError(t, err)
	})

	t.Run("detach document test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.DetachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentDetached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("remove document test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		clientInfo.Documents[docInfo.ID].ServerSeq = 1
		clientInfo.Documents[docInfo.ID].ClientSeq = 1
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		result, err = db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentRemoved)
		assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(0))
		assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(0))
		assert.NoError(t, err)
	})

	t.Run("invalid clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		assert.NoError(t, err)

		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		clientInfo.ID = "invalid clientInfo id"
		assert.Error(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		clientInfo.ID = dummyClientID
		assert.Error(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo), mongodb.ErrNoDocuments)
	})
}

// RunIsDocumentAttachedTest runs the IsDocumentAttached tests for the given db.
func RunIsDocumentAttachedTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("single document IsDocumentAttached test", func(t *testing.T) {
		ctx := context.Background()

		// 00. Create two clients and a document
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1", map[string]string{"userID": t.Name() + "1"})
		assert.NoError(t, err)
		c2, err := db.ActivateClient(ctx, projectID, t.Name()+"2", map[string]string{"userID": t.Name() + "2"})
		assert.NoError(t, err)
		d1, err := db.FindOrCreateDocInfo(ctx, c1.RefKey(), helper.TestDocKey(t))
		assert.NoError(t, err)

		// 01. Check if document is attached without attaching
		docRefKey1 := d1.RefKey()
		attached, err := db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)

		// 02. Check if document is attached after attaching
		assert.NoError(t, c1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		// 03. Check if document is attached after detaching
		assert.NoError(t, c1.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)

		// 04. Check if document is attached after two clients attaching
		assert.NoError(t, c1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		assert.NoError(t, c2.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		// 05. Check if document is attached after a client detaching
		assert.NoError(t, c1.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		// 06. Check if document is attached after another client detaching
		assert.NoError(t, c2.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("two documents IsDocumentAttached test", func(t *testing.T) {
		ctx := context.Background()

		// 00. Create a client and two documents
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1", map[string]string{"userID": t.Name() + "1"})
		assert.NoError(t, err)
		d1, err := db.FindOrCreateDocInfo(ctx, c1.RefKey(), helper.TestDocKey(t)+"1")
		assert.NoError(t, err)
		d2, err := db.FindOrCreateDocInfo(ctx, c1.RefKey(), helper.TestDocKey(t)+"2")
		assert.NoError(t, err)

		// 01. Check if documents are attached after attaching
		docRefKey1 := d1.RefKey()
		assert.NoError(t, c1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err := db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		docRefKey2 := d2.RefKey()
		assert.NoError(t, c1.AttachDocument(docRefKey2.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d2))
		attached, err = db.IsDocumentAttached(ctx, docRefKey2, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		// 02. Check if a document is attached after detaching another document
		assert.NoError(t, c1.DetachDocument(docRefKey2.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d2))
		attached, err = db.IsDocumentAttached(ctx, docRefKey2, "")
		assert.NoError(t, err)
		assert.False(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)

		// 03. Check if a document is attached after detaching remaining document
		assert.NoError(t, c1.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("IsDocumentAttached exclude client info test", func(t *testing.T) {
		ctx := context.Background()

		// 00. Create two clients and a document
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1", map[string]string{"userID": t.Name() + "1"})
		assert.NoError(t, err)
		c2, err := db.ActivateClient(ctx, projectID, t.Name()+"2", map[string]string{"userID": t.Name() + "2"})
		assert.NoError(t, err)
		d1, err := db.FindOrCreateDocInfo(ctx, c1.RefKey(), helper.TestDocKey(t))
		assert.NoError(t, err)

		// 01. Check if document is attached without attaching
		docRefKey1 := d1.RefKey()
		attached, err := db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)

		// 02. Check if document is attached after attaching
		assert.NoError(t, c1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)

		// 03. Check if document is attached after detaching
		assert.NoError(t, c1.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)

		// 04. Check if document is attached after two clients attaching
		assert.NoError(t, c1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		assert.NoError(t, c2.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c2.ID)
		assert.NoError(t, err)
		assert.True(t, attached)

		// 05. Check if document is attached after a client detaching
		assert.NoError(t, c1.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c1, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.True(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c1.ID)
		assert.NoError(t, err)
		assert.True(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c2.ID)
		assert.NoError(t, err)
		assert.False(t, attached)

		// 06. Check if document is attached after another client detaching
		assert.NoError(t, c2.DetachDocument(docRefKey1.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, c2, d1))
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, "")
		assert.NoError(t, err)
		assert.False(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c1.ID)
		assert.NoError(t, err)
		assert.False(t, attached)
		attached, err = db.IsDocumentAttached(ctx, docRefKey1, c2.ID)
		assert.NoError(t, err)
		assert.False(t, attached)
	})
}

// RunFindNextNCyclingProjectInfosTest runs the FindNextNCyclingProjectInfos tests for the given db.
func RunFindNextNCyclingProjectInfosTest(t *testing.T, db database.Database) {
	t.Run("FindNextNCyclingProjectInfos cyclic search test", func(t *testing.T) {
		ctx := context.Background()

		projectCnt := 10
		projects := make([]*database.ProjectInfo, 0)
		for i := range projectCnt {
			p, err := db.CreateProjectInfo(
				ctx,
				fmt.Sprintf("%s-%d-RunFindNextNCyclingProjectInfos", t.Name(), i),
				otherOwnerID,
				clientDeactivateThreshold,
			)
			assert.NoError(t, err)
			projects = append(projects, p)
		}

		lastProjectID := database.DefaultProjectID
		pageSize := 2

		for i := range 10 {
			projectInfos, err := db.FindNextNCyclingProjectInfos(ctx, pageSize, lastProjectID)
			assert.NoError(t, err)

			lastProjectID = projectInfos[len(projectInfos)-1].ID

			assert.Equal(t, projects[((i+1)*pageSize-1)%projectCnt].ID, lastProjectID)
		}

	})
}

// RunFindDeactivateCandidatesPerProjectTest runs the FindDeactivateCandidatesPerProject tests for the given db.
func RunFindDeactivateCandidatesPerProjectTest(t *testing.T, db database.Database) {
	t.Run("FindDeactivateCandidatesPerProject candidate search test", func(t *testing.T) {
		ctx := context.Background()

		p1, err := db.CreateProjectInfo(
			ctx,
			fmt.Sprintf("%s-FindDeactivateCandidatesPerProject", t.Name()),
			otherOwnerID,
			clientDeactivateThreshold,
		)
		assert.NoError(t, err)

		_, err = db.ActivateClient(ctx, p1.ID, t.Name()+"1-1", map[string]string{"userID": t.Name() + "1-1"})
		assert.NoError(t, err)

		_, err = db.ActivateClient(ctx, p1.ID, t.Name()+"1-2", map[string]string{"userID": t.Name() + "1-2"})
		assert.NoError(t, err)

		p2, err := db.CreateProjectInfo(
			ctx,
			fmt.Sprintf("%s-FindDeactivateCandidatesPerProject-2", t.Name()),
			otherOwnerID,
			"0s",
		)
		assert.NoError(t, err)

		c1, err := db.ActivateClient(ctx, p2.ID, t.Name()+"2-1", map[string]string{"userID": t.Name() + "2-1"})
		assert.NoError(t, err)

		c2, err := db.ActivateClient(ctx, p2.ID, t.Name()+"2-2", map[string]string{"userID": t.Name() + "2-2"})
		assert.NoError(t, err)

		candidates1, err := db.FindDeactivateCandidatesPerProject(ctx, p1, 10)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(candidates1))

		candidates2, err := db.FindDeactivateCandidatesPerProject(ctx, p2, 10)
		assert.NoError(t, err)

		idList := make([]types.ID, len(candidates2))
		for i, candidate := range candidates2 {
			idList[i] = candidate.ID
		}
		assert.Equal(t, 2, len(candidates2))
		assert.Contains(t, idList, c1.ID)
		assert.Contains(t, idList, c2.ID)
	})
}

// RunFindClientInfosByAttachedDocRefKeyTest runs the FindClientInfosByAttachedDocRefKey tests for the given db.
func RunFindClientInfosByAttachedDocRefKeyTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("FindClientInfosByAttachedDocRefKey test", func(t *testing.T) {
		ctx := context.Background()

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo1, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo1.RefKey(), docKey)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo1.AttachDocument(docRefKey.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))

		clientInfos, err := db.FindAttachedClientInfosByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(clientInfos))
		assert.Equal(t, clientInfo1.ID, clientInfos[0].ID)

		clientInfo2, _ := db.ActivateClient(ctx, projectID, t.Name()+"2", map[string]string{"userID": t.Name() + "2"})
		assert.NoError(t, clientInfo2.AttachDocument(docRefKey.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo))
		clientInfos, err = db.FindAttachedClientInfosByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(clientInfos))
		assert.Equal(t, clientInfo1.ID, clientInfos[0].ID)
		assert.Equal(t, clientInfo2.ID, clientInfos[1].ID)

		assert.NoError(t, clientInfo1.DetachDocument(docRefKey.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))
		clientInfos, err = db.FindAttachedClientInfosByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(clientInfos))
		assert.Equal(t, clientInfo2.ID, clientInfos[0].ID)

		assert.NoError(t, clientInfo2.DetachDocument(docRefKey.DocID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo))
		clientInfos, err = db.FindAttachedClientInfosByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(clientInfos))
	})
}

// RunPurgeDocument runs the RunPurgeDocument tests for the given db.
func RunPurgeDocument(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("PurgeDocument test", func(t *testing.T) {
		ctx := context.Background()

		// 01. Create a client and a document then attach the document to the client.
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name(), map[string]string{"userID": t.Name()})
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, _ := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docRefKey.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 02. Purge the document and check the document is purged.
		counts, err := db.PurgeDocument(ctx, docRefKey)
		assert.NoError(t, err)
		docInfo, err = db.FindDocInfoByRefKey(ctx, docRefKey)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)

		// NOTE(raararaara): This test is only checking the document is purged.
		assert.Equal(t, int64(0), counts["changes"])
		assert.Equal(t, int64(0), counts["snapshots"])
		assert.Equal(t, int64(0), counts["versionVectors"])
	})
}

// AssertKeys checks the equivalence between the provided expectedKeys and the keys in the given infos.
func AssertKeys(t *testing.T, expectedKeys []key.Key, infos []*database.DocInfo) {
	var keys []key.Key
	for _, info := range infos {
		keys = append(keys, info.Key)
	}
	assert.EqualValues(t, expectedKeys, keys)
}

func toChangeInfos(t *testing.T, docKey types.DocRefKey, changes []*change.Change) []*database.ChangeInfo {
	changeInfos := make([]*database.ChangeInfo, len(changes))
	for i, cn := range changes {
		info, err := database.NewFromChange(docKey, cn)
		assert.NoError(t, err)
		changeInfos[i] = info
	}
	return changeInfos
}
