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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/memory"
)

func TestDB(t *testing.T) {
	ctx := context.Background()
	memdb, err := memory.New()
	assert.NoError(t, err)

	notExistsID := db.ID("000000000000000000000000")

	t.Run("activate/deactivate client test", func(t *testing.T) {
		// try to deactivate the client with not exists ID.
		_, err = memdb.DeactivateClient(ctx, notExistsID)
		assert.ErrorIs(t, err, db.ErrClientNotFound)

		clientInfo, err := memdb.ActivateClient(ctx, t.Name())
		assert.NoError(t, err)

		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, db.ClientActivated, clientInfo.Status)

		// try to activate the client twice.
		clientInfo, err = memdb.ActivateClient(ctx, t.Name())
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, db.ClientActivated, clientInfo.Status)

		clientID := clientInfo.ID

		clientInfo, err = memdb.DeactivateClient(ctx, clientID)
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, db.ClientDeactivated, clientInfo.Status)

		// try to deactivate the client twice.
		clientInfo, err = memdb.DeactivateClient(ctx, clientID)
		assert.NoError(t, err)
		assert.Equal(t, t.Name(), clientInfo.Key)
		assert.Equal(t, db.ClientDeactivated, clientInfo.Status)
	})

	t.Run("activate and find client test", func(t *testing.T) {
		_, err := memdb.FindClientInfoByID(ctx, notExistsID)
		assert.ErrorIs(t, err, db.ErrClientNotFound)

		clientInfo, err := memdb.ActivateClient(ctx, t.Name())
		assert.NoError(t, err)

		found, err := memdb.FindClientInfoByID(ctx, clientInfo.ID)
		assert.NoError(t, err)
		assert.Equal(t, clientInfo.Key, found.Key)
	})

	t.Run("find docInfo test", func(t *testing.T) {
		clientInfo, err := memdb.ActivateClient(ctx, t.Name())
		assert.NoError(t, err)

		bsonDocKey := fmt.Sprintf("tests$%s", t.Name())
		_, err = memdb.FindDocInfoByKey(ctx, clientInfo, bsonDocKey, false)
		assert.ErrorIs(t, err, db.ErrDocumentNotFound)

		docInfo, err := memdb.FindDocInfoByKey(ctx, clientInfo, bsonDocKey, true)
		assert.NoError(t, err)
		assert.Equal(t, bsonDocKey, docInfo.Key)
	})

	t.Run("update clientInfo after PushPull test", func(t *testing.T) {
		clientInfo, err := memdb.ActivateClient(ctx, t.Name())
		assert.NoError(t, err)

		bsonDocKey := fmt.Sprintf("tests$%s", t.Name())
		docInfo, err := memdb.FindDocInfoByKey(ctx, clientInfo, bsonDocKey, true)
		assert.NoError(t, err)

		err = memdb.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
		assert.ErrorIs(t, err, db.ErrDocumentNeverAttached)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, memdb.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))
	})

	t.Run("insert and find changes test", func(t *testing.T) {
		bsonDocKey := fmt.Sprintf("tests$%s", t.Name())

		clientInfo, _ := memdb.ActivateClient(ctx, t.Name())
		docInfo, _ := memdb.FindDocInfoByKey(ctx, clientInfo, bsonDocKey, true)
		assert.NoError(t, clientInfo.AttachDocument(docInfo.ID))
		assert.NoError(t, memdb.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New("tests", t.Name())
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("array")
			return nil
		}))
		for idx := 0; idx < 10; idx++ {
			assert.NoError(t, doc.Update(func(root *proxy.ObjectProxy) error {
				root.GetArray("array").AddInteger(idx)
				return nil
			}))
		}
		pack := doc.CreateChangePack()
		for idx, change := range pack.Changes {
			change.SetServerSeq(uint64(idx))
		}

		// Store changes
		err = memdb.CreateChangeInfos(ctx, docInfo, 0, pack.Changes)
		assert.NoError(t, err)

		// Find changes
		loadedChanges, err := memdb.FindChangesBetweenServerSeqs(
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
		bsonDocKey := fmt.Sprintf("tests$%s", t.Name())

		clientInfo, _ := memdb.ActivateClient(ctx, t.Name())
		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		docInfo, _ := memdb.FindDocInfoByKey(ctx, clientInfo, bsonDocKey, true)

		doc := document.New("tests", t.Name())
		doc.SetActor(actorID)
		assert.NoError(t, doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("array")
			return nil
		}))

		err = memdb.CreateSnapshotInfo(ctx, docInfo.ID, doc.InternalDocument())
		assert.NoError(t, err)

		snapshot, err := memdb.FindLastSnapshotInfo(ctx, docInfo.ID)
		assert.NoError(t, err)
		assert.NotNil(t, snapshot)
	})
}
