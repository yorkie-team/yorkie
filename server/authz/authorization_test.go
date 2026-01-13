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

package authz_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/authz"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/test/helper"
)

func setupBackend(t *testing.T) *backend.Backend {
	conf := helper.TestConfig()
	conf.Backend.UseDefaultProject = false
	conf.Mongo = &mongo.Config{
		ConnectionTimeout:  "5s",
		ConnectionURI:      "mongodb://localhost:27017",
		YorkieDatabase:     helper.TestDBName() + "-integration",
		PingTimeout:        "5s",
		CacheStatsEnabled:  false,
		CacheStatsInterval: "30s",
		ProjectCacheSize:   helper.MongoProjectCacheSize,
		ProjectCacheTTL:    helper.MongoProjectCacheTTL,
		ClientCacheSize:    helper.MongoClientCacheSize,
		DocCacheSize:       helper.MongoDocCacheSize,
		ChangeCacheSize:    helper.MongoChangeCacheSize,
		VectorCacheSize:    helper.MongoVectorCacheSize,
	}

	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.Membership,
		conf.Housekeeping,
		metrics,
		nil,
		nil,
	)
	assert.NoError(t, err)

	return be
}

func TestAuthorization(t *testing.T) {
	be := setupBackend(t)
	ctx := context.Background()

	// Setup: Create test users
	owner, err := be.DB.CreateUserInfo(ctx, "test-owner", "password")
	assert.NoError(t, err)

	admin, err := be.DB.CreateUserInfo(ctx, "test-admin", "password")
	assert.NoError(t, err)

	member, err := be.DB.CreateUserInfo(ctx, "test-member", "password")
	assert.NoError(t, err)

	stranger, err := be.DB.CreateUserInfo(ctx, "test-stranger", "password")
	assert.NoError(t, err)

	// Setup: Create test project
	project, err := be.DB.CreateProjectInfo(ctx, "test-project", owner.ID)
	assert.NoError(t, err)

	// Setup: Add members with different roles
	_, err = be.DB.UpsertMemberInfo(ctx, project.ID, admin.ID, owner.ID, database.Admin)
	assert.NoError(t, err)

	_, err = be.DB.UpsertMemberInfo(ctx, project.ID, member.ID, owner.ID, database.Member)
	assert.NoError(t, err)

	t.Run("GetUserRole test", func(t *testing.T) {
		// Test owner
		role, err := authz.FindUserRole(ctx, be, owner.ID, project.ID)
		assert.NoError(t, err)
		assert.Equal(t, database.Owner, role)

		// Test admin
		role, err = authz.FindUserRole(ctx, be, admin.ID, project.ID)
		assert.NoError(t, err)
		assert.Equal(t, database.Admin, role)

		// Test member
		role, err = authz.FindUserRole(ctx, be, member.ID, project.ID)
		assert.NoError(t, err)
		assert.Equal(t, database.Member, role)

		// Test stranger (no access)
		_, err = authz.FindUserRole(ctx, be, stranger.ID, project.ID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, database.ErrMemberNotFound)
	})

	t.Run("GetUserRoleByName test", func(t *testing.T) {
		// Test owner
		projectID, role, err := authz.FindUserRoleByName(ctx, be, owner.ID, project.Name)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)
		assert.Equal(t, database.Owner, role)

		// Test admin
		projectID, role, err = authz.FindUserRoleByName(ctx, be, admin.ID, project.Name)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)
		assert.Equal(t, database.Admin, role)

		// Test member
		projectID, role, err = authz.FindUserRoleByName(ctx, be, member.ID, project.Name)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)
		assert.Equal(t, database.Member, role)

		// Test stranger (no access)
		_, _, err = authz.FindUserRoleByName(ctx, be, stranger.ID, project.Name)
		assert.Error(t, err)
	})

	t.Run("CheckPermission test", func(t *testing.T) {
		// Owner can do everything
		assert.NoError(t, authz.CheckPermission(ctx, be, owner.ID, project.ID, database.Owner))
		assert.NoError(t, authz.CheckPermission(ctx, be, owner.ID, project.ID, database.Admin))
		assert.NoError(t, authz.CheckPermission(ctx, be, owner.ID, project.ID, database.Member))

		// Admin can do admin and member tasks
		assert.Error(t, authz.CheckPermission(ctx, be, admin.ID, project.ID, database.Owner))
		assert.NoError(t, authz.CheckPermission(ctx, be, admin.ID, project.ID, database.Admin))
		assert.NoError(t, authz.CheckPermission(ctx, be, admin.ID, project.ID, database.Member))

		// Member can only do member tasks
		assert.Error(t, authz.CheckPermission(ctx, be, member.ID, project.ID, database.Owner))
		assert.Error(t, authz.CheckPermission(ctx, be, member.ID, project.ID, database.Admin))
		assert.NoError(t, authz.CheckPermission(ctx, be, member.ID, project.ID, database.Member))

		// Stranger has no access
		assert.Error(t, authz.CheckPermission(ctx, be, stranger.ID, project.ID, database.Member))
	})

	t.Run("CheckPermissionByName test", func(t *testing.T) {
		// Owner can access
		projectID, err := authz.CheckPermissionByName(ctx, be, owner.ID, project.Name, database.Admin)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)

		// Admin can access with admin permission
		projectID, err = authz.CheckPermissionByName(ctx, be, admin.ID, project.Name, database.Admin)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)

		// Member cannot access admin-level operations
		_, err = authz.CheckPermissionByName(ctx, be, member.ID, project.Name, database.Admin)
		assert.Error(t, err)
		assert.ErrorIs(t, err, authz.ErrInsufficientPermission)

		// Member can access member-level operations
		projectID, err = authz.CheckPermissionByName(ctx, be, member.ID, project.Name, database.Member)
		assert.NoError(t, err)
		assert.Equal(t, project.ID, projectID)

		// Stranger has no access
		_, err = authz.CheckPermissionByName(ctx, be, stranger.ID, project.Name, database.Member)
		assert.Error(t, err)
	})
}
