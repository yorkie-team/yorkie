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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestProjectCacheInvalidation(t *testing.T) {
	ctx := context.Background()

	t.Run("cache invalidation on UpdateProject test", func(t *testing.T) {
		// Create test server
		svr := helper.TestServer()
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		// Create admin client
		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()

		// Create a test project
		project, err := adminCli.CreateProject(ctx, "test-project-cache")
		assert.NoError(t, err)
		assert.NotNil(t, project)

		// Update project name
		newName := "updated-project-cache"
		updatedProject, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				Name: &newName,
			},
		)
		assert.NoError(t, err)
		assert.Equal(t, newName, updatedProject.Name)

		// Verify the project can be retrieved with the new name
		retrievedProject, err := adminCli.GetProject(ctx, newName)
		assert.NoError(t, err)
		assert.Equal(t, newName, retrievedProject.Name)
		assert.Equal(t, updatedProject.PublicKey, retrievedProject.PublicKey)
	})

	t.Run("cache invalidation on RotateProjectKeys test", func(t *testing.T) {
		// Create test server
		svr := helper.TestServer()
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		// Create admin client
		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()

		// Create a test project
		project, err := adminCli.CreateProject(ctx, "test-project-rotate")
		assert.NoError(t, err)
		assert.NotNil(t, project)
		oldPublicKey := project.PublicKey

		// Rotate project keys
		rotatedProject, err := adminCli.RotateProjectKeys(ctx, project.ID.String())
		assert.NoError(t, err)
		assert.NotEqual(t, oldPublicKey, rotatedProject.PublicKey)

		// Verify the project can be retrieved with new keys
		retrievedProject, err := adminCli.GetProject(ctx, rotatedProject.Name)
		assert.NoError(t, err)
		assert.Equal(t, rotatedProject.PublicKey, retrievedProject.PublicKey)
		assert.NotEqual(t, oldPublicKey, retrievedProject.PublicKey)
	})
}
