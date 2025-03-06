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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestProjectInfo(t *testing.T) {
	t.Run("update fields test", func(t *testing.T) {
		// Setup a dummy project for testing
		dummyOwnerID := types.ID("000000000000000000000000")
		clientDeactivateThreshold := "1h"
		project := database.NewProjectInfo(t.Name(), dummyOwnerID, clientDeactivateThreshold)

		// Prepare test values
		testName := "testName"
		testURL := "testUrl"
		testMethods := []string{"testMethod"}
		testEvents := []string{"testEvent"}
		testClientDeactivateThreshold := "2h"
		testAttachCountLimitPerDocument := 10

		// Test updating name field
		project.UpdateFields(&types.UpdatableProjectFields{Name: &testName})
		assert.Equal(t, testName, project.Name)

		// Test updating auth webhook URL field
		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookURL: &testURL})
		assert.Equal(t, testURL, project.AuthWebhookURL)

		// Test updating auth webhook methods field
		project.UpdateFields(&types.UpdatableProjectFields{AuthWebhookMethods: &testMethods})
		assert.Equal(t, testMethods, project.AuthWebhookMethods)

		// Test updating event webhook URL field
		project.UpdateFields(&types.UpdatableProjectFields{EventWebhookURL: &testURL})
		assert.Equal(t, testURL, project.EventWebhookURL)

		// Test updating event webhook methods field
		project.UpdateFields(&types.UpdatableProjectFields{EventWebhookEvents: &testEvents})
		assert.Equal(t, testEvents, project.EventWebhookEvents)

		assert.Equal(t, dummyOwnerID, project.Owner) // Owner should remain unchanged

		// Test updating client deactivate threshold field
		project.UpdateFields(&types.UpdatableProjectFields{
			ClientDeactivateThreshold: &testClientDeactivateThreshold,
		})
		assert.Equal(t, testClientDeactivateThreshold, project.ClientDeactivateThreshold)

		// Test updating connection count limit per document field
		project.UpdateFields(&types.UpdatableProjectFields{
			AttachCountLimitPerDocument: &testAttachCountLimitPerDocument,
		})
		assert.Equal(t, testAttachCountLimitPerDocument, project.AttachCountLimitPerDocument)
	})

	t.Run("new project info test", func(t *testing.T) {
		// Setup test values
		dummyOwnerID := types.ID("000000000000000000000000")
		clientDeactivateThreshold := "1h"
		projectName := "test-project"

		// Create a new project info
		project := database.NewProjectInfo(projectName, dummyOwnerID, clientDeactivateThreshold)

		// Verify all fields are correctly initialized
		assert.Equal(t, projectName, project.Name)
		assert.Equal(t, dummyOwnerID, project.Owner)
		assert.Equal(t, clientDeactivateThreshold, project.ClientDeactivateThreshold)
		assert.Equal(t, 0, project.AttachCountLimitPerDocument)               // Default should be 0 (no limit)
		assert.NotEmpty(t, project.PublicKey)                                 // Public key should be auto-generated
		assert.NotEmpty(t, project.SecretKey)                                 // Secret key should be auto-generated
		assert.NotZero(t, project.CreatedAt)                                  // Creation time should be set
		assert.True(t, project.CreatedAt.Before(time.Now().Add(time.Second))) // Creation time should be recent
	})

	t.Run("deep copy test", func(t *testing.T) {
		// Setup a project with various fields set
		dummyOwnerID := types.ID("000000000000000000000000")
		clientDeactivateThreshold := "1h"
		project := database.NewProjectInfo("test-project", dummyOwnerID, clientDeactivateThreshold)
		project.ID = types.ID("123456789012345678901234")
		project.AuthWebhookURL = "https://example.com"
		project.AuthWebhookMethods = []string{"attach", "detach"}
		project.AttachCountLimitPerDocument = 10
		project.UpdatedAt = time.Now()

		// Create a deep copy
		copiedProject := project.DeepCopy()

		// Verify all fields are correctly copied
		assert.Equal(t, project.ID, copiedProject.ID)
		assert.Equal(t, project.Name, copiedProject.Name)
		assert.Equal(t, project.Owner, copiedProject.Owner)
		assert.Equal(t, project.PublicKey, copiedProject.PublicKey)
		assert.Equal(t, project.SecretKey, copiedProject.SecretKey)
		assert.Equal(t, project.AuthWebhookURL, copiedProject.AuthWebhookURL)
		assert.Equal(t, project.AuthWebhookMethods, copiedProject.AuthWebhookMethods)
		assert.Equal(t, project.ClientDeactivateThreshold, copiedProject.ClientDeactivateThreshold)
		assert.Equal(t, project.AttachCountLimitPerDocument, copiedProject.AttachCountLimitPerDocument)
		assert.Equal(t, project.CreatedAt, copiedProject.CreatedAt)
		assert.Equal(t, project.UpdatedAt, copiedProject.UpdatedAt)

		// Modify copy and verify it doesn't affect original (proving it's a deep copy)
		copiedProject.Name = "modified-name"
		copiedProject.AttachCountLimitPerDocument = 20
		assert.NotEqual(t, project.Name, copiedProject.Name)
		assert.NotEqual(t, project.AttachCountLimitPerDocument, copiedProject.AttachCountLimitPerDocument)
	})

	t.Run("to project test", func(t *testing.T) {
		// Setup a project info with various fields set
		dummyOwnerID := types.ID("000000000000000000000000")
		clientDeactivateThreshold := "1h"
		projectInfo := database.NewProjectInfo("test-project", dummyOwnerID, clientDeactivateThreshold)
		projectInfo.ID = types.ID("123456789012345678901234")
		projectInfo.AuthWebhookURL = "https://example.com"
		projectInfo.AuthWebhookMethods = []string{"attach", "detach"}
		projectInfo.AttachCountLimitPerDocument = 10
		projectInfo.UpdatedAt = time.Now()

		// Convert to types.Project
		project := projectInfo.ToProject()

		// Verify all fields are correctly converted
		assert.Equal(t, projectInfo.ID, project.ID)
		assert.Equal(t, projectInfo.Name, project.Name)
		assert.Equal(t, projectInfo.Owner, project.Owner)
		assert.Equal(t, projectInfo.PublicKey, project.PublicKey)
		assert.Equal(t, projectInfo.SecretKey, project.SecretKey)
		assert.Equal(t, projectInfo.AuthWebhookURL, project.AuthWebhookURL)
		assert.Equal(t, projectInfo.AuthWebhookMethods, project.AuthWebhookMethods)
		assert.Equal(t, projectInfo.ClientDeactivateThreshold, project.ClientDeactivateThreshold)
		assert.Equal(t, projectInfo.AttachCountLimitPerDocument, project.AttachCountLimitPerDocument)
		assert.Equal(t, projectInfo.CreatedAt, project.CreatedAt)
		assert.Equal(t, projectInfo.UpdatedAt, project.UpdatedAt)
	})

	t.Run("client deactivate threshold as time duration test", func(t *testing.T) {
		dummyOwnerID := types.ID("000000000000000000000000")

		// Test with a valid complex duration (1 hour and 30 minutes)
		project := database.NewProjectInfo("test-project", dummyOwnerID, "1h30m")
		duration, err := project.ClientDeactivateThresholdAsTimeDuration()
		assert.NoError(t, err)
		assert.Equal(t, 90*time.Minute, duration)

		// Test with another valid duration (24 hours)
		project.ClientDeactivateThreshold = "24h"
		duration, err = project.ClientDeactivateThresholdAsTimeDuration()
		assert.NoError(t, err)
		assert.Equal(t, 24*time.Hour, duration)

		// Test with an invalid duration string
		project.ClientDeactivateThreshold = "invalid"
		_, err = project.ClientDeactivateThresholdAsTimeDuration()
		assert.Error(t, err)
		assert.Equal(t, database.ErrInvalidTimeDurationString, err)
	})
}
