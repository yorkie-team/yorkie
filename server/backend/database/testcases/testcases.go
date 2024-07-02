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
	mongodb "go.mongodb.org/mongo-driver/mongo"

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
)

// RunFindDocInfoTest runs the FindDocInfo test for the given db.
func RunFindDocInfoTest(
	t *testing.T,
	db database.Database,
	projectID types.ID,
) {
	t.Run("find docInfo test", func(t *testing.T) {
		ctx := context.Background()
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		_, err = db.FindDocInfoByRefKey(context.Background(), types.DocRefKey{
			ProjectID: projectID,
			DocID:     dummyClientID,
		})
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		_, err = db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, false)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)

		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		assert.NoError(t, err)
		assert.Equal(t, docKey, docInfo.Key)
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
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKeys := []string{
			"test", "test$3", "test-search", "test$0",
			"search$test", "abcde", "test abc",
			"test0", "test1", "test2", "test3", "test10",
			"test11", "test20", "test21", "test22", "test23"}
		for _, docKey := range docKeys {
			_, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), key.Key(docKey), true)
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

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		docRefKey := docInfo.RefKey()
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
		for idx := 0; idx < 10; idx++ {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()
		for idx, c := range pack.Changes {
			c.SetServerSeq(int64(idx))
		}

		// Store changes
		err := db.CreateChangeInfos(ctx, projectID, docInfo, 0, pack.Changes, false)
		assert.NoError(t, err)

		// Find changes
		loadedChanges, err := db.FindChangesBetweenServerSeqs(
			ctx,
			docRefKey,
			6,
			10,
		)
		assert.NoError(t, err)
		assert.Len(t, loadedChanges, 5)
	})
}

// RunFindClosestSnapshotInfoTest runs the FindClosestSnapshotInfo test for the given db.
func RunFindClosestSnapshotInfoTest(t *testing.T, db database.Database, projectID types.ID) {
	t.Run("store and find snapshots test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)

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

		pack := change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(1), nil, nil)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.NoError(t, db.CreateSnapshotInfo(ctx, docRefKey, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docRefKey, change.MaxCheckpoint.ServerSeq, true)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), snapshot.ServerSeq)

		pack = change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(2), nil, nil)
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

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
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

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientActivated, clientInfo.Status)

		// try to activate the client twice.
		clientInfo, err = db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientActivated, clientInfo.Status)

		clientInfo, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientDeactivated, clientInfo.Status)

		// try to deactivate the client twice.
		clientInfo, err = db.DeactivateClient(ctx, clientInfo.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientDeactivated, clientInfo.Status)
	})

	t.Run("ensure document detached when deactivate client test", func(t *testing.T) {
		ctx := context.Background()

		// 01. Create a client
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientActivated, clientInfo.Status)

		// 02. Create documents and attach them to the client
		for i := 0; i < 3; i++ {
			docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), helper.TestDocKey(t, i), true)
			assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
			clientInfo.Documents[docInfo.ID].ServerSeq = 1
			clientInfo.Documents[docInfo.ID].ClientSeq = 1
			assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

			result, err := db.FindClientInfoByRefKey(ctx, clientInfo.RefKey())
			assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentAttached)
			assert.Equal(t, result.Documents[docInfo.ID].ServerSeq, int64(1))
			assert.Equal(t, result.Documents[docInfo.ID].ClientSeq, uint32(1))
			assert.NoError(t, err)
		}

		// 03. Remove one document
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), helper.TestDocKey(t, 2), true)
		assert.NoError(t, err)
		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 04. Deactivate the client
		result, err := db.DeactivateClient(ctx, types.ClientRefKey{
			ProjectID: projectID,
			ClientID:  clientInfo.ID,
		})
		assert.NoError(t, err)

		// 05. Check whether doc.Status is reflected properly.
		// If it was `DocumentAttached`, it should be changed to `DocumentDetached`.
		for i := 0; i < 2; i++ {
			docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), helper.TestDocKey(t, i), true)
			assert.NoError(t, err)
			assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentDetached)
		}
		// If it was `DocumentRemoved`, it should be remained `DocumentRemoved`.
		docInfo, err = db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), helper.TestDocKey(t, 2), true)
		assert.NoError(t, err)
		assert.Equal(t, result.Documents[docInfo.ID].Status, database.DocumentRemoved)
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
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 03. Update authWebhookURL test
		newAuthWebhookURL2 := newAuthWebhookURL + "2"
		fields = &types.UpdatableProjectFields{
			AuthWebhookURL: &newAuthWebhookURL2,
		}
		assert.NoError(t, fields.Validate())
		res, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.NoError(t, err)
		updateInfo, err = db.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, res, updateInfo)
		assert.Equal(t, newName2, updateInfo.Name)
		assert.NotEqual(t, newAuthWebhookURL, updateInfo.AuthWebhookURL)
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.Equal(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 04. Update clientDeactivateThreshold test
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
		assert.Equal(t, newAuthWebhookMethods, updateInfo.AuthWebhookMethods)
		assert.NotEqual(t, newClientDeactivateThreshold, updateInfo.ClientDeactivateThreshold)

		// 05. Duplicated name test
		fields = &types.UpdatableProjectFields{Name: &existName}
		_, err = db.UpdateProjectInfo(ctx, dummyOwnerID, id, fields)
		assert.ErrorIs(t, err, database.ErrProjectNameAlreadyExists)

		// 06. OwnerID not match test
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
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfos := make([]*database.DocInfo, 0, totalSize)
		for i := 0; i < totalSize; i++ {
			docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), key.Key(fmt.Sprintf("%d", i)), true)
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
			docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, types.ClientRefKey{
				ProjectID: testProjectInfo.ID,
				ClientID:  dummyClientID,
			}, testDocKey, true)
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
		for i := 0; i < testDocCnt; i++ {
			testDocKey := key.Key("key" + strconv.Itoa(i))
			docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, types.ClientRefKey{
				ProjectID: projectInfo.ID,
				ClientID:  dummyClientID,
			}, testDocKey, true)
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
		err = db.CreateChangeInfos(ctx, projectInfo.ID, docInfos[1], 0, []*change.Change{}, true)
		assert.NoError(t, err)
		err = db.CreateChangeInfos(ctx, projectInfo.ID, docInfos[3], 0, []*change.Change{}, true)
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
		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		// 02. Remove the document and check the document is removed.
		err := db.CreateChangeInfos(ctx, projectID, docInfo, 0, []*change.Change{}, true)
		assert.NoError(t, err)
		docInfo, err = db.FindDocInfoByRefKey(ctx, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, false, docInfo.RemovedAt.IsZero())
	})

	t.Run("reuse same key to create docInfo test ", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)

		// 01. Create a client and a document then attach the document to the client.
		clientInfo1, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo1, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo1.RefKey(), docKey, true)
		docRefKey1 := docInfo1.RefKey()
		assert.NoError(t, clientInfo1.AttachDocument(docRefKey1.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo1))

		// 02. Remove the document.
		assert.NoError(t, clientInfo1.RemoveDocument(docRefKey1.DocID))
		err := db.CreateChangeInfos(ctx, projectID, docInfo1, 0, []*change.Change{}, true)
		assert.NoError(t, err)

		// 03. Create a document with same key and check they have same key but different id.
		docInfo2, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo1.RefKey(), docKey, true)
		docRefKey2 := docInfo2.RefKey()
		assert.NoError(t, clientInfo1.AttachDocument(docRefKey2.DocID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo2))
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
	})

	t.Run("set removed_at in docInfo test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		docRefKey := docInfo.RefKey()
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set removed_at in docInfo and store changes
		assert.NoError(t, clientInfo.RemoveDocument(docInfo.ID))
		err := db.CreateChangeInfos(ctx, projectID, docInfo, 0, pack.Changes, true)
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
}

// RunUpdateClientInfoAfterPushPullTest runs the UpdateClientInfoAfterPushPull tests for the given db.
func RunUpdateClientInfoAfterPushPullTest(t *testing.T, db database.Database, projectID types.ID) {
	dummyClientID := types.ID("000000000000000000000000")
	ctx := context.Background()

	t.Run("document is not attached in clientInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		assert.NoError(t, err)

		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
	})

	t.Run("document attach test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
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
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
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
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
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
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
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
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
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
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		c2, err := db.ActivateClient(ctx, projectID, t.Name()+"2")
		assert.NoError(t, err)
		d1, err := db.FindDocInfoByKeyAndOwner(ctx, c1.RefKey(), helper.TestDocKey(t), true)
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
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		d1, err := db.FindDocInfoByKeyAndOwner(ctx, c1.RefKey(), helper.TestDocKey(t)+"1", true)
		assert.NoError(t, err)
		d2, err := db.FindDocInfoByKeyAndOwner(ctx, c1.RefKey(), helper.TestDocKey(t)+"2", true)
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
		c1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		c2, err := db.ActivateClient(ctx, projectID, t.Name()+"2")
		assert.NoError(t, err)
		d1, err := db.FindDocInfoByKeyAndOwner(ctx, c1.RefKey(), helper.TestDocKey(t), true)
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
		for i := 0; i < projectCnt; i++ {
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

		for i := 0; i < 10; i++ {
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

		_, err = db.ActivateClient(ctx, p1.ID, t.Name()+"1-1")
		assert.NoError(t, err)

		_, err = db.ActivateClient(ctx, p1.ID, t.Name()+"1-2")
		assert.NoError(t, err)

		p2, err := db.CreateProjectInfo(
			ctx,
			fmt.Sprintf("%s-FindDeactivateCandidatesPerProject-2", t.Name()),
			otherOwnerID,
			"0s",
		)
		assert.NoError(t, err)

		c1, err := db.ActivateClient(ctx, p2.ID, t.Name()+"2-1")
		assert.NoError(t, err)

		c2, err := db.ActivateClient(ctx, p2.ID, t.Name()+"2-2")
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

// AssertKeys checks the equivalence between the provided expectedKeys and the keys in the given infos.
func AssertKeys(t *testing.T, expectedKeys []key.Key, infos []*database.DocInfo) {
	var keys []key.Key
	for _, info := range infos {
		keys = append(keys, info.Key)
	}
	assert.EqualValues(t, expectedKeys, keys)
}
