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

package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func TestClientInfo(t *testing.T) {
	t.Run("attach/detach document test", func(t *testing.T) {
		docID := db.ID("000000000000000000000000")
		clientInfo := db.ClientInfo{
			Status: db.ClientActivated,
		}

		err := clientInfo.AttachDocument(docID)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(docID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.UpdateCheckpoint(docID, checkpoint.Max)
		assert.NoError(t, err)

		err = clientInfo.DetachDocument(docID)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(docID)
		assert.NoError(t, err)
		assert.False(t, isAttached)
	})
}
