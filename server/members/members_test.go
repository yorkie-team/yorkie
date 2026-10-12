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

package members_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/members"
)

func TestMembers(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)

	be := &backend.Backend{DB: db}

	invitedBy := types.ID("000000000000000000000002")

	t.Run("List test", func(t *testing.T) {
		// 00. Create project.
		project, err := db.CreateProjectInfo(ctx, t.Name(), invitedBy)
		assert.NoError(t, err)
		projectID := project.ID

		username1 := fmt.Sprintf("%s-u1", t.Name())
		username2 := fmt.Sprintf("%s-u2", t.Name())

		// 01. Create users.
		u1, err := db.CreateUserInfo(ctx, username1, "pw")
		assert.NoError(t, err)
		u2, err := db.CreateUserInfo(ctx, username2, "pw")
		assert.NoError(t, err)

		// 02. Create members directly
		_, err = db.UpsertMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)
		_, err = db.UpsertMemberInfo(ctx, projectID, u2.ID, invitedBy, database.Admin)
		assert.NoError(t, err)

		// 03. List.
		list, err := members.List(ctx, be, projectID)
		assert.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("UpdateRole test", func(t *testing.T) {
		// 00. Create project.
		project, err := db.CreateProjectInfo(ctx, t.Name(), invitedBy)
		assert.NoError(t, err)
		projectID := project.ID

		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user and member.
		u1, err := db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)
		_, err = db.UpsertMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)

		// 02. Update role.
		updated, rotated, err := members.UpdateRole(ctx, be, projectID, username, database.Admin.String())
		assert.NoError(t, err)
		assert.Equal(t, database.Admin.String(), updated.Role)
		assert.False(t, rotated)
	})

	t.Run("Remove test", func(t *testing.T) {
		// 00. Create project.
		project, err := db.CreateProjectInfo(ctx, t.Name(), invitedBy)
		assert.NoError(t, err)
		projectID := project.ID

		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user and member.
		u1, err := db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)
		_, err = db.UpsertMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)

		// 02. Remove.
		rotated, err := members.Remove(ctx, be, projectID, username)
		assert.NoError(t, err)
		assert.False(t, rotated)

		// 03. Verify removed.
		list, err := members.List(ctx, be, projectID)
		assert.NoError(t, err)
		assert.Len(t, list, 0)
	})

	t.Run("UpdateRole invalid role test", func(t *testing.T) {
		// 00. Create project.
		project, err := db.CreateProjectInfo(ctx, t.Name(), invitedBy)
		assert.NoError(t, err)
		projectID := project.ID

		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user.
		_, err = db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)

		// 02. UpdateRole with invalid role.
		_, _, err = members.UpdateRole(ctx, be, projectID, username, "invalid-role")
		assert.Equal(t, errors.ErrCodeInvalidArgument, errors.StatusOf(err))
		assert.Equal(t, database.ErrInvalidMemberRole.Code(), errors.ErrorInfoOf(err).Code)
	})

	// The secret key carries no user, so a copy outlives the membership it was
	// read under. Losing the role that could read it must replace the key, and
	// only the secret key: the public key identifies the project to SDK clients.
	t.Run("secret key follows the roles that can read it test", func(t *testing.T) {
		tests := []struct {
			name    string
			from    database.MemberRole
			to      database.MemberRole // empty means the member is removed
			rotated bool
		}{
			{"admin demoted to member", database.Admin, database.Member, true},
			{"admin removed", database.Admin, "", true},
			{"admin kept as admin", database.Admin, database.Admin, false},
			{"member promoted to admin", database.Member, database.Admin, false},
			{"member removed", database.Member, "", false},
		}

		for i, tc := range tests {
			project, err := db.CreateProjectInfo(ctx, fmt.Sprintf("%s-%d", t.Name(), i), invitedBy)
			assert.NoError(t, err)

			username := fmt.Sprintf("%s-u%d", t.Name(), i)
			user, err := db.CreateUserInfo(ctx, username, "pw")
			assert.NoError(t, err)
			_, err = db.UpsertMemberInfo(ctx, project.ID, user.ID, invitedBy, tc.from)
			assert.NoError(t, err)

			var rotated bool
			if tc.to == "" {
				rotated, err = members.Remove(ctx, be, project.ID, username)
			} else {
				_, rotated, err = members.UpdateRole(ctx, be, project.ID, username, tc.to.String())
			}
			assert.NoError(t, err, tc.name)
			assert.Equal(t, tc.rotated, rotated, tc.name)

			after, err := db.FindProjectInfoByID(ctx, project.ID)
			assert.NoError(t, err)
			assert.Equal(t, project.PublicKey, after.PublicKey, tc.name)
			if !tc.rotated {
				assert.Equal(t, project.SecretKey, after.SecretKey, tc.name)
				continue
			}

			assert.NotEqual(t, project.SecretKey, after.SecretKey, tc.name)
			_, err = db.FindProjectInfoBySecretKey(ctx, project.SecretKey)
			assert.ErrorIs(t, err, database.ErrProjectNotFound, tc.name)
		}
	})
}
