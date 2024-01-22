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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestProjectInfo(t *testing.T) {
	t.Run("update fields test", func(t *testing.T) {
		dummyOwnerID := types.ID("000000000000000000000000")
		clientDeactivateThreshold := "1h"
		project := database.NewProjectInfo(t.Name(), dummyOwnerID, clientDeactivateThreshold)

		testName := "testName"
		testURL := "testUrl"
		testMethods := []string{"testMethod"}
		testClientDeactivateThreshold := "2h"

		project.UpdateFields(&types.UpdatableProjectFields{Name: &testName})
		assert.Equal(t, testName, project.Name)

		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookURL: &testURL})
		assert.Equal(t, testURL, project.AuthWebhookURL)

		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookMethods: &testMethods})
		assert.Equal(t, testMethods, project.AuthWebhookMethods)
		assert.Equal(t, dummyOwnerID, project.Owner)

		project.UpdateFields(&types.UpdatableProjectFields{
			ClientDeactivateThreshold: &testClientDeactivateThreshold,
		})
		assert.Equal(t, testClientDeactivateThreshold, project.ClientDeactivateThreshold)
	})
}
