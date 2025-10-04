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
		project := database.NewProjectInfo(t.Name(), dummyOwnerID)

		testName := "testName"
		testURL := "testUrl"
		testMethods := []string{"testMethod"}
		testAuthWebhookMaxRetries := uint64(10)
		testAuthWebhookMinWaitInterval := "10ms"
		testAuthWebhookMaxWaitInterval := "1s"
		testAuthWebhookRequestTimeout := "500ms"
		testEvents := []string{"testEvent"}
		testEventWebhookMaxRetries := uint64(20)
		testEventWebhookMinWaitInterval := "8ms"
		testEventWebhookMaxWaitInterval := "3s"
		testEventWebhookRequestTimeout := "1s"
		testClientDeactivateThreshold := "2h"
		testSnapshotThreshold := int64(100)
		testSnapshotInterval := int64(80)
		testMaxSubscribersPerDocument := 10
		testMaxAttachmentsPerDocument := 10
		testRemoveOnDetach := false

		project.UpdateFields(&types.UpdatableProjectFields{Name: &testName})
		assert.Equal(t, testName, project.Name)

		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookURL: &testURL})
		assert.Equal(t, testURL, project.AuthWebhookURL)

		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookMethods: &testMethods})
		assert.Equal(t, testMethods, project.AuthWebhookMethods)

		project.UpdateFields(&types.UpdatableProjectFields{
			AuthWebhookMaxRetries:      &testAuthWebhookMaxRetries,
			AuthWebhookMinWaitInterval: &testAuthWebhookMinWaitInterval,
			AuthWebhookMaxWaitInterval: &testAuthWebhookMaxWaitInterval,
			AuthWebhookRequestTimeout:  &testAuthWebhookRequestTimeout,
		})
		assert.Equal(t, testAuthWebhookMaxRetries, project.AuthWebhookMaxRetries)
		assert.Equal(t, testAuthWebhookMinWaitInterval, project.AuthWebhookMinWaitInterval)
		assert.Equal(t, testAuthWebhookMaxWaitInterval, project.AuthWebhookMaxWaitInterval)
		assert.Equal(t, testAuthWebhookRequestTimeout, project.AuthWebhookRequestTimeout)

		project.UpdateFields(&types.UpdatableProjectFields{EventWebhookURL: &testURL})
		assert.Equal(t, testURL, project.EventWebhookURL)

		project.UpdateFields(&types.UpdatableProjectFields{EventWebhookEvents: &testEvents})
		assert.Equal(t, testEvents, project.EventWebhookEvents)

		project.UpdateFields(&types.UpdatableProjectFields{
			EventWebhookMaxRetries:      &testEventWebhookMaxRetries,
			EventWebhookMinWaitInterval: &testEventWebhookMinWaitInterval,
			EventWebhookMaxWaitInterval: &testEventWebhookMaxWaitInterval,
			EventWebhookRequestTimeout:  &testEventWebhookRequestTimeout,
		})
		assert.Equal(t, testEventWebhookMaxRetries, project.EventWebhookMaxRetries)
		assert.Equal(t, testEventWebhookMinWaitInterval, project.EventWebhookMinWaitInterval)
		assert.Equal(t, testEventWebhookMaxWaitInterval, project.EventWebhookMaxWaitInterval)
		assert.Equal(t, testEventWebhookRequestTimeout, project.EventWebhookRequestTimeout)

		assert.Equal(t, dummyOwnerID, project.Owner)

		project.UpdateFields(&types.UpdatableProjectFields{
			ClientDeactivateThreshold: &testClientDeactivateThreshold,
		})
		assert.Equal(t, testClientDeactivateThreshold, project.ClientDeactivateThreshold)

		project.UpdateFields(&types.UpdatableProjectFields{
			SnapshotThreshold: &testSnapshotThreshold,
			SnapshotInterval:  &testSnapshotInterval,
		})
		assert.Equal(t, testSnapshotThreshold, project.SnapshotThreshold)
		assert.Equal(t, testSnapshotInterval, project.SnapshotInterval)

		project.UpdateFields(&types.UpdatableProjectFields{
			MaxSubscribersPerDocument: &testMaxSubscribersPerDocument,
		})
		assert.Equal(t, testMaxSubscribersPerDocument, project.MaxSubscribersPerDocument)

		project.UpdateFields(&types.UpdatableProjectFields{
			MaxAttachmentsPerDocument: &testMaxAttachmentsPerDocument,
		})
		assert.Equal(t, testMaxAttachmentsPerDocument, project.MaxAttachmentsPerDocument)

		project.UpdateFields(&types.UpdatableProjectFields{
			RemoveOnDetach: &testRemoveOnDetach,
		})
		assert.Equal(t, testRemoveOnDetach, project.RemoveOnDetach)
	})
}
