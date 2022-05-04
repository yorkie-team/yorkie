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
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestChangeInfo(t *testing.T) {
	t.Run("comparing actorID equals after calling ToChange test", func(t *testing.T) {
		actorID := time.ActorID{}
		_, err := rand.Read(actorID.Bytes())
		assert.NoError(t, err)

		expectedID := actorID.String()
		changeInfo := database.ChangeInfo{
			ActorID: types.ID(expectedID),
		}

		change, err := changeInfo.ToChange()
		assert.NoError(t, err)
		assert.Equal(t, change.ID().ActorID().String(), expectedID)
	})
}
