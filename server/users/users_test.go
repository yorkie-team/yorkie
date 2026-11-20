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
	"github.com/yorkie-team/yorkie/server/invites"
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

	// An invite outlives the session that created it, and Accept grants its
	// role without consulting the creator, so a token left behind would keep
	// handing out membership on behalf of an account that no longer exists.
	t.Run("delete invites with the account test", func(t *testing.T) {
		project, err := db.CreateProjectInfo(ctx, t.Name(), ownerID)
		assert.NoError(t, err)

		leaving, err := users.SignUp(ctx, be, t.Name()+"-leaving", "pw")
		assert.NoError(t, err)
		_, err = db.UpsertMemberInfo(ctx, project.ID, leaving.ID, ownerID, database.Admin)
		assert.NoError(t, err)

		token, _, err := invites.Create(ctx, be, project.ID, database.Admin, leaving.ID, invites.ExpireSevenDays)
		assert.NoError(t, err)

		assert.NoError(t, users.DeleteAccountByName(ctx, be, leaving.Username))

		joining, err := users.SignUp(ctx, be, t.Name()+"-joining", "pw")
		assert.NoError(t, err)
		_, err = invites.Accept(ctx, be, token, joining.ID)
		assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))
	})

	// A project names its owner by user ID and nothing transfers that
	// ownership, so deleting the owner would leave a project no one can
	// administer. The deletion is refused instead.
	t.Run("refuse to delete the owner of a project test", func(t *testing.T) {
		owner, err := users.SignUp(ctx, be, t.Name()+"-owner", "pw")
		assert.NoError(t, err)
		project, err := db.CreateProjectInfo(ctx, t.Name(), owner.ID)
		assert.NoError(t, err)

		err = users.DeleteAccountByName(ctx, be, owner.Username)
		assert.Equal(t, pkgerrors.ErrCodeFailedPrecondition, pkgerrors.StatusOf(err))

		// The account and its project both survive the refusal.
		found, err := users.GetUserByName(ctx, be, owner.Username)
		assert.NoError(t, err)
		assert.Equal(t, owner.ID, found.ID)
		owned, err := db.ListProjectInfos(ctx, owner.ID)
		assert.NoError(t, err)
		assert.Len(t, owned, 1)
		assert.Equal(t, project.ID, owned[0].ID)
	})
}

// acceptingDB inserts a membership while the account is being deleted, standing
// in for an AcceptInvite that authenticated before the deletion started. It
// writes the row either side of the user document, which is the whole of the
// race: before, and the second sweep has to remove it; after, and the member
// write itself has to refuse it.
type acceptingDB struct {
	database.Database

	projectID   types.ID
	userID      types.ID
	afterDelete bool
	err         error
}

func (d *acceptingDB) DeleteUserInfoByName(ctx context.Context, username string) error {
	if !d.afterDelete {
		_, d.err = d.Database.UpsertMemberInfo(ctx, d.projectID, d.userID, d.userID, database.Admin)
	}

	if err := d.Database.DeleteUserInfoByName(ctx, username); err != nil {
		return err
	}

	if d.afterDelete {
		_, d.err = d.Database.UpsertMemberInfo(ctx, d.projectID, d.userID, d.userID, database.Admin)
	}

	return nil
}

// A membership written during the deletion is as unreachable as one written
// before it, so neither interleaving may leave a row behind.
func TestDeleteAccountByNameWithConcurrentAccept(t *testing.T) {
	for _, tc := range []struct {
		name        string
		afterDelete bool
	}{
		{"accept before the user document is deleted test", false},
		{"accept after the user document is deleted test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			db, err := memory.New()
			assert.NoError(t, err)

			ownerID := types.ID("000000000000000000000001")
			project, err := db.CreateProjectInfo(ctx, t.Name(), ownerID)
			assert.NoError(t, err)

			leaving, err := users.SignUp(ctx, &backend.Backend{DB: db}, t.Name()+"-leaving", "pw")
			assert.NoError(t, err)
			_, err = db.UpsertMemberInfo(ctx, project.ID, leaving.ID, ownerID, database.Admin)
			assert.NoError(t, err)

			racing := &acceptingDB{
				Database:    db,
				projectID:   project.ID,
				userID:      leaving.ID,
				afterDelete: tc.afterDelete,
			}
			assert.NoError(t, users.DeleteAccountByName(ctx, &backend.Backend{DB: racing}, leaving.Username))

			// The write that lands after the user document is gone is refused
			// outright; the one that lands before it succeeds and is swept.
			if tc.afterDelete {
				assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(racing.err))
			} else {
				assert.NoError(t, racing.err)
			}

			_, err = db.FindMemberInfo(ctx, project.ID, leaving.ID)
			assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))

			list, err := members.List(ctx, &backend.Backend{DB: db}, project.ID)
			assert.NoError(t, err)
			assert.Empty(t, list)
		})
	}
}

// The member write path keys its row by user ID alone, so it is the only place
// that can keep a membership from being created for an account that is already
// gone.
func TestUpsertMemberInfoForDeletedUser(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)
	be := &backend.Backend{DB: db}

	ownerID := types.ID("000000000000000000000001")
	project, err := db.CreateProjectInfo(ctx, t.Name(), ownerID)
	assert.NoError(t, err)

	leaving, err := users.SignUp(ctx, be, t.Name()+"-leaving", "pw")
	assert.NoError(t, err)
	assert.NoError(t, users.DeleteAccountByName(ctx, be, leaving.Username))

	_, err = db.UpsertMemberInfo(ctx, project.ID, leaving.ID, ownerID, database.Admin)
	assert.Equal(t, pkgerrors.ErrCodeNotFound, pkgerrors.StatusOf(err))

	list, err := members.List(ctx, be, project.ID)
	assert.NoError(t, err)
	assert.Empty(t, list)
}
