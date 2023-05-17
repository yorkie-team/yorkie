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

package memory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

func TestDB(t *testing.T) {
	ctx := context.Background()
	db, err := memory.New()
	assert.NoError(t, err)

	projectID := database.DefaultProjectID
	dummyOwnerID := types.ID("000000000000000000000000")
	otherOwnerID := types.ID("000000000000000000000001")
	notExistsID := types.ID("000000000000000000000000")
	clientDeactivateThreshold := "1h"

	t.Run("activate/deactivate client test", func(t *testing.T) {
		// try to deactivate the client with not exists ID.
		_, err = db.DeactivateClient(ctx, projectID, notExistsID)
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

		clientID := clientInfo.ID

		clientInfo, err = db.DeactivateClient(ctx, projectID, clientID)
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientDeactivated, clientInfo.Status)

		// try to deactivate the client twice.
		clientInfo, err = db.DeactivateClient(ctx, projectID, clientID)
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, database.ClientDeactivated, clientInfo.Status)
	})

	t.Run("project test", func(t *testing.T) {
		suffixes := []int{0, 1, 2}
		for _, suffix := range suffixes {
			_, err = db.CreateProjectInfo(ctx, fmt.Sprintf("%s-%d", t.Name(), suffix), dummyOwnerID, clientDeactivateThreshold)
			assert.NoError(t, err)
		}

		_, err = db.CreateProjectInfo(ctx, t.Name(), otherOwnerID, clientDeactivateThreshold)
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

	t.Run("user test", func(t *testing.T) {
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

	t.Run("activate and find client test", func(t *testing.T) {
		_, err := db.FindClientInfoByID(ctx, projectID, notExistsID)
		assert.ErrorIs(t, err, database.ErrClientNotFound)

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		found, err := db.FindClientInfoByID(ctx, projectID, clientInfo.ID)
		assert.NoError(t, err)
		assert.Equal(t, clientInfo.Key, found.Key)
	})

	t.Run("find docInfo test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		_, err = db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, false)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)

		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)
		assert.Equal(t, docKey, docInfo.Key)
	})

	t.Run("search docInfos test", func(t *testing.T) {
		localDB, err := memory.New()
		assert.NoError(t, err)

		clientInfo, err := localDB.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKeys := []string{
			"test", "test$3", "test-search", "test$0",
			"search$test", "abcde", "test abc",
			"test0", "test1", "test2", "test3", "test10",
			"test11", "test20", "test21", "test22", "test23"}
		for _, docKey := range docKeys {
			_, err := localDB.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, key.Key(docKey), true)
			assert.NoError(t, err)
		}

		res, err := localDB.FindDocInfosByQuery(ctx, projectID, "test", 10)
		assert.NoError(t, err)

		var keys []key.Key
		for _, info := range res.Elements {
			keys = append(keys, info.Key)
		}

		assert.EqualValues(t, []key.Key{
			"test", "test abc", "test$0", "test$3", "test-search",
			"test0", "test1", "test10", "test11", "test2"}, keys)
		assert.Equal(t, 15, res.TotalCount)
	})

	t.Run("set RemovedAt in docInfo test", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set RemovedAt in docInfo and store changes
		err = db.CreateChangeInfos(ctx, projectID, docInfo, 0, pack.Changes, true)
		assert.NoError(t, err)

		// Check whether RemovedAt is set in docInfo
		docInfo, err = db.FindDocInfoByID(ctx, projectID, docInfo.ID)
		assert.NoError(t, err)
		assert.Equal(t, false, docInfo.RemovedAt.IsZero())
	})

	t.Run("reuse same key to create docInfo test ", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo1, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo1, _ := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo1.ID, docKey, true)
		assert.NoError(t, clientInfo1.AttachDocument(docInfo1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo1))

		doc := document.New(key.Key(t.Name()))
		pack := doc.CreateChangePack()

		// Set RemovedAt in docInfo and store changes
		err = db.CreateChangeInfos(ctx, projectID, docInfo1, 0, pack.Changes, true)
		assert.NoError(t, err)

		// Use same key to create docInfo
		clientInfo2, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo2, _ := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo2.ID, docKey, true)
		assert.NoError(t, clientInfo2.AttachDocument(docInfo2.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo2))

		// Check they have same key but different id
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
	})

	t.Run("update clientInfo after PushPull test", func(t *testing.T) {
		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)

		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)

		err = db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
	})

	t.Run("insert and find changes test", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := 0; idx < 10; idx++ {
			assert.NoError(t, doc.Update(func(root *json.Object) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()
		for idx, c := range pack.Changes {
			c.SetServerSeq(int64(idx))
		}

		// Store changes
		err = db.CreateChangeInfos(ctx, projectID, docInfo, 0, pack.Changes, false)
		assert.NoError(t, err)

		// Find changes
		loadedChanges, err := db.FindChangesBetweenServerSeqs(
			ctx,
			docInfo.ID,
			6,
			10,
		)
		assert.NoError(t, err)
		assert.Len(t, loadedChanges, 5)
	})

	t.Run("store and find snapshots test", func(t *testing.T) {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, _ := db.ActivateClient(ctx, projectID, t.Name())
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		docInfo, _ := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)

		doc := document.New(key.Key(t.Name()))
		doc.SetActor(actorID)

		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.SetNewArray("array")
			return nil
		}))

		assert.NoError(t, db.CreateSnapshotInfo(ctx, docInfo.ID, doc.InternalDocument()))
		snapshot, err := db.FindClosestSnapshotInfo(ctx, docInfo.ID, change.MaxCheckpoint.ServerSeq)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), snapshot.ServerSeq)

		pack := change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(1), nil, nil)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.NoError(t, db.CreateSnapshotInfo(ctx, docInfo.ID, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docInfo.ID, change.MaxCheckpoint.ServerSeq)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), snapshot.ServerSeq)

		pack = change.NewPack(doc.Key(), doc.Checkpoint().NextServerSeq(2), nil, nil)
		assert.NoError(t, doc.ApplyChangePack(pack))
		assert.NoError(t, db.CreateSnapshotInfo(ctx, docInfo.ID, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docInfo.ID, change.MaxCheckpoint.ServerSeq)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), snapshot.ServerSeq)

		assert.NoError(t, db.CreateSnapshotInfo(ctx, docInfo.ID, doc.InternalDocument()))
		snapshot, err = db.FindClosestSnapshotInfo(ctx, docInfo.ID, 1)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), snapshot.ServerSeq)
	})

	t.Run("docInfo pagination test", func(t *testing.T) {
		localDB, err := memory.New()
		assert.NoError(t, err)

		assertKeys := func(expectedKeys []key.Key, infos []*database.DocInfo) {
			var keys []key.Key
			for _, info := range infos {
				keys = append(keys, info.Key)
			}
			assert.EqualValues(t, expectedKeys, keys)
		}

		pageSize := 5
		totalSize := 9
		clientInfo, _ := localDB.ActivateClient(ctx, projectID, t.Name())
		for i := 0; i < totalSize; i++ {
			_, err := localDB.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, key.Key(fmt.Sprintf("%d", i)), true)
			assert.NoError(t, err)
		}

		// initial page, offset is empty
		infos, err := localDB.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{PageSize: pageSize})
		assert.NoError(t, err)
		assertKeys([]key.Key{"8", "7", "6", "5", "4"}, infos)

		// backward
		infos, err = localDB.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:   infos[len(infos)-1].ID,
			PageSize: pageSize,
		})
		assert.NoError(t, err)
		assertKeys([]key.Key{"3", "2", "1", "0"}, infos)

		// backward again
		emptyInfos, err := localDB.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:   infos[len(infos)-1].ID,
			PageSize: pageSize,
		})
		assert.NoError(t, err)
		assertKeys(nil, emptyInfos)

		// forward
		infos, err = localDB.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:    infos[0].ID,
			PageSize:  pageSize,
			IsForward: true,
		})
		assert.NoError(t, err)
		assertKeys([]key.Key{"4", "5", "6", "7", "8"}, infos)

		// forward again
		emptyInfos, err = localDB.FindDocInfosByPaging(ctx, projectID, types.Paging[types.ID]{
			Offset:    infos[len(infos)-1].ID,
			PageSize:  pageSize,
			IsForward: true,
		})
		assert.NoError(t, err)
		assertKeys(nil, emptyInfos)
	})

	t.Run("FindDocInfoByID test", func(t *testing.T) {
		localDB, err := memory.New()
		assert.NoError(t, err)

		_, err = localDB.FindDocInfoByID(context.Background(), projectID, notExistsID)
		assert.ErrorIs(t, err, database.ErrDocumentNotFound)
	})

	t.Run("UpdateDocInfoRemovedAt test", func(t *testing.T) {
		docKey := key.Key(fmt.Sprintf("tests$%s", t.Name()))

		clientInfo, err := db.ActivateClient(ctx, projectID, t.Name())
		assert.NoError(t, err)
		docInfo1, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)
		assert.NoError(t, err)
		assert.NoError(t, clientInfo.AttachDocument(docInfo1.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo1))

		assert.True(t, docInfo1.RemovedAt.IsZero())

		err = db.UpdateDocInfoRemovedAt(ctx, projectID, docInfo1.ID)
		assert.NoError(t, err)
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo1))

		assert.True(t, docInfo1.RemovedAt.IsZero())

		docInfo2, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo.ID, docKey, true)

		assert.NoError(t, err)
		assert.NotEqual(t, docInfo1.ID, docInfo2.ID)
		assert.Equal(t, docInfo1.Key, docInfo2.Key)
		assert.Equal(t, docInfo1.ProjectID, docInfo2.ProjectID)
		assert.True(t, docInfo2.RemovedAt.IsZero())
	})

	t.Run("IsAttachedDocument test", func(t *testing.T) {
		ctx := context.Background()
		docKey1 := key.Key(fmt.Sprintf("tests$%s", t.Name()+"1"))

		clientInfo1, err := db.ActivateClient(ctx, projectID, t.Name()+"1")
		assert.NoError(t, err)
		docInfo, err := db.FindDocInfoByKeyAndOwner(ctx, projectID, clientInfo1.ID, docKey1, true)
		assert.NoError(t, err)
		assert.NoError(t, clientInfo1.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))

		// Check document is attached
		isAttached, err := db.IsAttachedDocument(ctx, projectID, docInfo.ID)
		assert.True(t, isAttached)
		assert.NoError(t, err)

		// Check document is detached
		assert.NoError(t, clientInfo1.DetachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo1, docInfo))
		isAttached, err = db.IsAttachedDocument(ctx, projectID, docInfo.ID)
		assert.False(t, isAttached)
		assert.NoError(t, err)

		// Check whether document is attached in any other client
		clientInfo2, err := db.ActivateClient(ctx, projectID, t.Name()+"2")
		assert.NoError(t, err)
		assert.NoError(t, clientInfo2.AttachDocument(docInfo.ID))
		assert.NoError(t, db.UpdateClientInfoAfterPushPull(ctx, clientInfo2, docInfo))

		isAttached, err = db.IsAttachedDocument(ctx, projectID, docInfo.ID)
		assert.True(t, isAttached)
		assert.NoError(t, err)
	})

	t.Run("FindProjectInfoByID test", func(t *testing.T) {
		info, err := db.CreateProjectInfo(ctx, t.Name(), dummyOwnerID, clientDeactivateThreshold)
		assert.NoError(t, err)

		_, err = db.FindProjectInfoByName(ctx, dummyOwnerID, info.Name)
		assert.NoError(t, err)

		_, err = db.FindProjectInfoByName(ctx, otherOwnerID, info.Name)
		assert.ErrorIs(t, err, database.ErrProjectNotFound)
	})

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
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
