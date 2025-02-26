/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestDocInfo(t *testing.T) {

	t.Run("server sequence test", func(t *testing.T) {
		docInfo := &database.DocInfo{
			ID:        types.ID("doc1"),
			ProjectID: types.ID("project1"),
			Key:       key.Key("test-doc"),
			ServerSeq: 0,
		}

		// Test increasing server sequence
		assert.Equal(t, int64(1), docInfo.IncreaseServerSeq())
		assert.Equal(t, int64(1), docInfo.ServerSeq)

		assert.Equal(t, int64(2), docInfo.IncreaseServerSeq())
		assert.Equal(t, int64(2), docInfo.ServerSeq)
	})

	t.Run("removed status test", func(t *testing.T) {
		docInfo := &database.DocInfo{
			ID:        types.ID("doc1"),
			ProjectID: types.ID("project1"),
			Key:       key.Key("test-doc"),
		}

		// Test initial state
		assert.False(t, docInfo.IsRemoved())

		// Test after setting removed time
		docInfo.RemovedAt = time.Now()
		assert.True(t, docInfo.IsRemoved())

		// Test with zero time
		docInfo.RemovedAt = time.Time{}
		assert.False(t, docInfo.IsRemoved())
	})

	t.Run("connected clients test", func(t *testing.T) {
		// Create a new DocInfo
		docInfo := &database.DocInfo{
			ID:        types.ID("doc1"),
			ProjectID: types.ID("project1"),
			Key:       key.Key("test-doc"),
			Owner:     types.ID("client1"),
		}

		// Test initial state
		assert.Equal(t, 0, docInfo.GetConnectedClientCount())
		assert.False(t, docInfo.IsClientConnected(types.ID("client1")))

		// Add a client
		docInfo.AddConnectedClient(types.ID("client1"))
		assert.Equal(t, 1, docInfo.GetConnectedClientCount())
		assert.True(t, docInfo.IsClientConnected(types.ID("client1")))

		// Add another client
		docInfo.AddConnectedClient(types.ID("client2"))
		assert.Equal(t, 2, docInfo.GetConnectedClientCount())
		assert.True(t, docInfo.IsClientConnected(types.ID("client2")))

		// Remove a client
		docInfo.RemoveConnectedClient(types.ID("client1"))
		assert.Equal(t, 1, docInfo.GetConnectedClientCount())
		assert.False(t, docInfo.IsClientConnected(types.ID("client1")))
		assert.True(t, docInfo.IsClientConnected(types.ID("client2")))

		// Remove non-existent client (should not error)
		docInfo.RemoveConnectedClient(types.ID("client3"))
		assert.Equal(t, 1, docInfo.GetConnectedClientCount())

		// Remove last client
		docInfo.RemoveConnectedClient(types.ID("client2"))
		assert.Equal(t, 0, docInfo.GetConnectedClientCount())
		assert.False(t, docInfo.IsClientConnected(types.ID("client2")))
	})

	t.Run("deep copy test", func(t *testing.T) {
		// Create original DocInfo with connected clients
		original := &database.DocInfo{
			ID:        types.ID("doc1"),
			ProjectID: types.ID("project1"),
			Key:       key.Key("test-doc"),
			ServerSeq: 5,
			Owner:     types.ID("client1"),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		original.AddConnectedClient(types.ID("client1"))
		original.AddConnectedClient(types.ID("client2"))

		// Create a deep copy
		copy := original.DeepCopy()

		// Verify the copy has the same values
		assert.Equal(t, original.ID, copy.ID)
		assert.Equal(t, original.ProjectID, copy.ProjectID)
		assert.Equal(t, original.Key, copy.Key)
		assert.Equal(t, original.ServerSeq, copy.ServerSeq)
		assert.Equal(t, original.Owner, copy.Owner)
		assert.Equal(t, original.CreatedAt, copy.CreatedAt)
		assert.Equal(t, original.UpdatedAt, copy.UpdatedAt)
		assert.Equal(t, original.GetConnectedClientCount(), copy.GetConnectedClientCount())
		assert.True(t, copy.IsClientConnected(types.ID("client1")))
		assert.True(t, copy.IsClientConnected(types.ID("client2")))

		// Modify the copy and verify it doesn't affect the original
		copy.ServerSeq = 10
		copy.RemoveConnectedClient(types.ID("client1"))
		assert.Equal(t, int64(5), original.ServerSeq)
		assert.Equal(t, int64(10), copy.ServerSeq)
		assert.Equal(t, 2, original.GetConnectedClientCount())
		assert.Equal(t, 1, copy.GetConnectedClientCount())
		assert.True(t, original.IsClientConnected(types.ID("client1")))
		assert.False(t, copy.IsClientConnected(types.ID("client1")))
	})

	t.Run("ref key test", func(t *testing.T) {
		docInfo := &database.DocInfo{
			ID:        types.ID("doc1"),
			ProjectID: types.ID("project1"),
			Key:       key.Key("test-doc"),
		}

		refKey := docInfo.RefKey()
		assert.Equal(t, types.ID("project1"), refKey.ProjectID)
		assert.Equal(t, types.ID("doc1"), refKey.DocID)
	})
}
