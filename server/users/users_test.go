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

package users_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	pkgerrors "github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/members"
	"github.com/yorkie-team/yorkie/server/users"
)

// A membership of a deleted user is keyed by user ID but only reachable by
// username, so one left behind can never be listed or removed again. Account
// deletion is a role-loss event and has to drop the roles with the account.
func TestDeleteAccountByName(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)
	be := &backend.Backend{DB: db}

	ownerID := types.ID("000000000000000000000001")

	t.Run("delete memberships with the account test", func(t *testing.T) {
		project, err := db.CreateProjectInfo(ctx, t.Name(), ownerID)
		assert.NoError(t, err)

		leaving, err := users.SignUp(ctx, be, t.Name()+"-leaving", "pw")
		assert.NoError(t, err)
		staying, err := users.SignUp(ctx, be, t.Name()+"-staying", "pw")
		assert.NoError(t, err)

		_, err = db.UpsertMemberInfo(ctx, project.ID, leaving.ID, ownerID, database.Admin)
		assert.NoError(t, err)
		_, err = db.UpsertMemberInfo(ctx, project.ID, staying.ID, ownerID, database.Member)
		assert.NoError(t, err)

		assert.NoError(t, users.DeleteAccountByName(ctx, be, leaving.Username))

		// The account and its membership are both gone.
		_, err = users.GetUserByName(ctx, be, leaving.Username)
		assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))
		_, err = db.FindMemberInfo(ctx, project.ID, leaving.ID)
		assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))

		// So the owner still sees the members that remain, instead of an
		// error raised by the row the deleted user left behind.
		list, err := members.List(ctx, be, project.ID)
		assert.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, staying.Username, list[0].Username)
	})

	t.Run("delete account without membership test", func(t *testing.T) {
		user, err := users.SignUp(ctx, be, t.Name(), "pw")
		assert.NoError(t, err)

		assert.NoError(t, users.DeleteAccountByName(ctx, be, user.Username))
	})

	t.Run("delete unknown account test", func(t *testing.T) {
		err := users.DeleteAccountByName(ctx, be, t.Name())
		assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))
	})
}
