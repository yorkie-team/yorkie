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

package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestProjectInfo(t *testing.T) {
	t.Run("validation test", func(t *testing.T) {
		conf := &database.ProjectInfo{
			AuthWebhookMethods: []string{"ActivateClient"},
		}
		assert.NoError(t, conf.Validate())

		// 2. Included invalid methods
		conf = &database.ProjectInfo{
			AuthWebhookMethods: []string{"InvalidMethod"},
		}
		assert.Error(t, conf.Validate())
	})
	t.Run("SetField test", func(t *testing.T) {
		project := database.NewProjectInfo(t.Name())

		testName := "testName"
		testURL := "testUrl"
		testMethod := []string{"testMethod"}

		assert.NoError(t, project.SetField("Name", &testName))
		assert.Equal(t, testName, project.Name)

		assert.NoError(t, project.SetField("AuthWebhookURL", &testURL))
		assert.Equal(t, testURL, project.AuthWebhookURL)

		assert.NoError(t, project.SetField("AuthWebhookMethods", &testMethod))
		assert.Equal(t, testMethod, project.AuthWebhookMethods)

		project = &database.ProjectInfo{
			AuthWebhookMethods: []string{"InvalidMethod"},
		}
		assert.ErrorIs(t, project.SetField("wrong field name", &testName), database.ErrNotUpdatableFieldName)
	})
}
