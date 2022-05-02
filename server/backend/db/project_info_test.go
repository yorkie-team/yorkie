/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/db"
)

func TestProjectInfo(t *testing.T) {
	t.Run("require auth test", func(t *testing.T) {
		// 1. Specify which methods to allow
		info := &db.ProjectInfo{
			AuthWebhookURL:     "ValidWebhookURL",
			AuthWebhookMethods: []string{string(types.ActivateClient)},
		}
		assert.True(t, info.RequireAuth(types.ActivateClient))
		assert.False(t, info.RequireAuth(types.DetachDocument))

		// 2. Allow all
		info2 := &db.ProjectInfo{
			AuthWebhookURL:     "ValidWebhookURL",
			AuthWebhookMethods: []string{},
		}
		assert.True(t, info2.RequireAuth(types.ActivateClient))
		assert.True(t, info2.RequireAuth(types.DetachDocument))

		// 3. Empty webhook URL
		info3 := &db.ProjectInfo{
			AuthWebhookURL: "",
		}
		assert.False(t, info3.RequireAuth(types.ActivateClient))
	})

	t.Run("validation test", func(t *testing.T) {
		conf := &db.ProjectInfo{
			AuthWebhookMethods: []string{"ActivateClient"},
		}
		assert.NoError(t, conf.Validate())

		// 2. Included invalid methods
		conf = &db.ProjectInfo{
			AuthWebhookMethods: []string{"InvalidMethod"},
		}
		assert.Error(t, conf.Validate())
	})
}
