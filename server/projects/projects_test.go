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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/sync"
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

// The secret key authorizes the project-scoped admin procedures by itself, so
// a user who may read the project but not act as its admin must not see it.
func TestSecretKeyVisibility(t *testing.T) {
	ctx := context.Background()
	be := newTestBackend(t)

	const (
		adminID  = types.ID("000000000000000000000001")
		memberID = types.ID("000000000000000000000002")
	)

	info, err := be.DB.CreateProjectInfo(ctx, t.Name(), testOwnerID)
	assert.NoError(t, err)
	assert.NotEmpty(t, info.SecretKey)

	_, err = be.DB.UpsertMemberInfo(ctx, info.ID, adminID, testOwnerID, database.Admin)
	assert.NoError(t, err)
	_, err = be.DB.UpsertMemberInfo(ctx, info.ID, memberID, testOwnerID, database.Member)
	assert.NoError(t, err)

	tests := []struct {
		name      string
		userID    types.ID
		role      database.MemberRole
		secretKey string
	}{
		{"owner", testOwnerID, database.Owner, info.SecretKey},
		{"admin", adminID, database.Admin, info.SecretKey},
		{"member", memberID, database.Member, ""},
	}

	t.Run("project and role test", func(t *testing.T) {
		for _, tc := range tests {
			project, role, err := projects.ProjectAndRole(ctx, be, tc.userID, info.Name)
			assert.NoError(t, err, tc.name)
			assert.Equal(t, tc.role, role, tc.name)
			assert.Equal(t, tc.secretKey, project.SecretKey, tc.name)

			// Only the secret key is withheld; the rest of the project stays.
			assert.Equal(t, info.ID, project.ID, tc.name)
			assert.Equal(t, info.PublicKey, project.PublicKey, tc.name)
		}
	})

	t.Run("list projects test", func(t *testing.T) {
		for _, tc := range tests {
			list, err := projects.ListProjects(ctx, be, tc.userID)
			assert.NoError(t, err, tc.name)
			assert.Len(t, list, 1, tc.name)
			assert.Equal(t, info.ID, list[0].ID, tc.name)
			assert.Equal(t, tc.secretKey, list[0].SecretKey, tc.name)
		}
	})

	// The role read-back is a second query, so it can fail on its own: a
	// membership revoked between the two reads leaves a project whose row is
	// already gone. Listing kept working before the read-back existed and must
	// keep working now, with the key withheld rather than handed out.
	t.Run("unresolved member role test", func(t *testing.T) {
		be := &backend.Backend{DB: failingMemberDB{be.DB}, Lockers: sync.New()}

		list, err := projects.ListProjects(ctx, be, memberID)
		assert.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, info.ID, list[0].ID)
		assert.Empty(t, list[0].SecretKey)
	})
}

// failingMemberDB fails every membership read, leaving the rest of the database
// intact.
type failingMemberDB struct {
	database.Database
}

func (d failingMemberDB) FindMemberInfo(
	_ context.Context,
	_ types.ID,
	_ types.ID,
) (*database.MemberInfo, error) {
	return nil, database.ErrMemberNotFound
}
