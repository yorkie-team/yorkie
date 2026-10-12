/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package projects_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/projects"
)

func TestProjectFromSecretKey(t *testing.T) {
	ctx := context.Background()
	be := newTestBackend(t)
	_, defaultProject, err := be.DB.EnsureDefaultUserAndProject(ctx, "admin", "admin-password")
	assert.NoError(t, err)

	t.Run("secret key resolves its project test", func(t *testing.T) {
		project, err := projects.ProjectFromSecretKey(ctx, be, defaultProject.SecretKey)
		assert.NoError(t, err)
		assert.Equal(t, database.DefaultProjectID, project.ID)
	})

	// The secret key is the only credential the admin path checks, so a
	// missing one must not resolve to any project, the default one included.
	t.Run("keyless secret key resolves no project test", func(t *testing.T) {
		for _, secretKey := range []string{"", " \t ", " ", "  \t"} {
			project, err := projects.ProjectFromSecretKey(ctx, be, secretKey)
			assert.ErrorIs(t, err, projects.ErrSecretKeyNotProvided, "secret key %q", secretKey)
			assert.Nil(t, project, "secret key %q", secretKey)
		}
	})
}
