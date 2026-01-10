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
		projectID := types.ID("000000000000000000000011")
		username1 := fmt.Sprintf("%s-u1", t.Name())
		username2 := fmt.Sprintf("%s-u2", t.Name())

		// 01. Create users.
		u1, err := db.CreateUserInfo(ctx, username1, "pw")
		assert.NoError(t, err)
		u2, err := db.CreateUserInfo(ctx, username2, "pw")
		assert.NoError(t, err)

		// 02. Create members directly
		_, err = db.CreateMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)
		_, err = db.CreateMemberInfo(ctx, projectID, u2.ID, invitedBy, database.Admin)
		assert.NoError(t, err)

		// 03. List.
		list, err := members.List(ctx, be, projectID)
		assert.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("UpdateRole test", func(t *testing.T) {
		projectID := types.ID("000000000000000000000012")
		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user and member.
		u1, err := db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)
		_, err = db.CreateMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)

		// 02. Update role.
		updated, err := members.UpdateRole(ctx, be, projectID, username, database.Admin.String())
		assert.NoError(t, err)
		assert.Equal(t, database.Admin.String(), updated.Role)
	})

	t.Run("Remove test", func(t *testing.T) {
		projectID := types.ID("000000000000000000000013")
		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user and member.
		u1, err := db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)
		_, err = db.CreateMemberInfo(ctx, projectID, u1.ID, invitedBy, database.Member)
		assert.NoError(t, err)

		// 02. Remove.
		assert.NoError(t, members.Remove(ctx, be, projectID, username))

		// 03. Verify removed.
		list, err := members.List(ctx, be, projectID)
		assert.NoError(t, err)
		assert.Len(t, list, 0)
	})

	t.Run("UpdateRole invalid role test", func(t *testing.T) {
		projectID := types.ID("000000000000000000000015")
		username := fmt.Sprintf("%s-u1", t.Name())

		// 01. Create user.
		_, err := db.CreateUserInfo(ctx, username, "pw")
		assert.NoError(t, err)

		// 02. UpdateRole with invalid role.
		_, err = members.UpdateRole(ctx, be, projectID, username, "invalid-role")
		assert.Equal(t, errors.ErrCodeInvalidArgument, errors.StatusOf(err))
		assert.Equal(t, database.ErrInvalidMemberRole.Code(), errors.ErrorInfoOf(err).Code)
	})
}
