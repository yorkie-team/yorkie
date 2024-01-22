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

package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestClientInfo(t *testing.T) {
	dummyDocKey := key.Key("dummy")
	dummyDocID := types.ID("000000000000000000000000")
	dummyDocRefKey := types.DocRefKey{
		Key: dummyDocKey,
		ID:  dummyDocID,
	}

	t.Run("attach/detach document test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocRefKey)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocRefKey)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.UpdateCheckpoint(dummyDocRefKey, change.MaxCheckpoint)
		assert.NoError(t, err)

		err = clientInfo.EnsureDocumentAttached(dummyDocRefKey)
		assert.NoError(t, err)

		err = clientInfo.DetachDocument(dummyDocRefKey)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(dummyDocRefKey)
		assert.NoError(t, err)
		assert.False(t, isAttached)

		err = clientInfo.AttachDocument(dummyDocRefKey)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(dummyDocRefKey)
		assert.NoError(t, err)
		assert.True(t, isAttached)

	})

	t.Run("check if in project test", func(t *testing.T) {
		dummyProjectID := types.ID("000000000000000000000000")
		clientInfo := database.ClientInfo{
			ProjectID: dummyProjectID,
		}

		err := clientInfo.CheckIfInProject(dummyProjectID)
		assert.NoError(t, err)
	})

	t.Run("check if in project error test", func(t *testing.T) {
		dummyProjectID := types.ID("000000000000000000000000")
		otherProjectID := types.ID("000000000000000000000001")
		clientInfo := database.ClientInfo{
			ProjectID: dummyProjectID,
		}

		err := clientInfo.CheckIfInProject(otherProjectID)
		assert.ErrorIs(t, err, database.ErrClientNotFound)
	})

	t.Run("client deactivate test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocRefKey)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocRefKey)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		clientInfo.Deactivate()

		err = clientInfo.EnsureDocumentAttached(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)
	})

	t.Run("client not activate error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientDeactivated,
		}

		err := clientInfo.AttachDocument(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)

		err = clientInfo.EnsureDocumentAttached(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)

		err = clientInfo.DetachDocument(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)
	})

	t.Run("document not attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}
		err := clientInfo.DetachDocument(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrDocumentNotAttached)
	})

	t.Run("document never attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}
		_, err := clientInfo.IsAttached(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)

		err = clientInfo.UpdateCheckpoint(dummyDocRefKey, change.MaxCheckpoint)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
	})

	t.Run("document already attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocRefKey)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocRefKey)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.AttachDocument(dummyDocRefKey)
		assert.ErrorIs(t, err, database.ErrDocumentAlreadyAttached)
	})
}
