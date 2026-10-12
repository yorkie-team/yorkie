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

// Package members provides business logic for managing project members.
package members

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/projects"
)

// List returns all members of a project.
func List(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
) ([]*types.Member, error) {
	memberInfos, err := be.DB.ListMemberInfos(ctx, projectID)
	if err != nil {
		return nil, err
	}

	members := make([]*types.Member, len(memberInfos))
	for i, info := range memberInfos {
		// Get user info to include username
		userInfo, err := be.DB.FindUserInfoByID(ctx, info.UserID)
		if err != nil {
			return nil, err
		}

		member := info.ToMember()
		member.Username = userInfo.Username
		members[i] = member
	}

	return members, nil
}

// Remove removes a user from a project. It reports whether the project's
// secret key was rotated because the removed user could read it.
func Remove(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	username string,
) (bool, error) {
	// Find the user by username
	userInfo, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return false, err
	}

	prev, err := be.DB.FindMemberInfo(ctx, projectID, userInfo.ID)
	if err != nil {
		return false, err
	}

	// Delete the member
	if err := be.DB.DeleteMemberInfo(ctx, projectID, userInfo.ID); err != nil {
		return false, err
	}

	return revokeSecretKey(ctx, be, projectID, prev.Role, "")
}

// UpdateRole updates the role of a project member. It reports whether the
// project's secret key was rotated because the member lost the role that let
// it read the key.
func UpdateRole(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	username string,
	role string,
) (*types.Member, bool, error) {
	memberRole, err := database.NewMemberRole(role)
	if err != nil {
		return nil, false, err
	}

	// Find the user by username
	userInfo, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return nil, false, err
	}

	prev, err := be.DB.FindMemberInfo(ctx, projectID, userInfo.ID)
	if err != nil {
		return nil, false, err
	}

	// Update the role
	memberInfo, err := be.DB.UpdateMemberRole(ctx, projectID, userInfo.ID, memberRole)
	if err != nil {
		return nil, false, err
	}

	rotated, err := revokeSecretKey(ctx, be, projectID, prev.Role, memberRole)
	if err != nil {
		return nil, false, err
	}

	// Convert to Member and add username
	member := memberInfo.ToMember()
	member.Username = userInfo.Username

	return member, rotated, nil
}

// revokeSecretKey rotates the project's secret key when a user goes from a
// role that may read it to one that may not. An empty next role means the
// user left the project.
//
// NOTE(hackerwins): The secret key carries no user, so the role a user held
// when reading it is not checked when the key is used. Without the rotation, an
// admin who is demoted or removed would keep the project's full authority for
// as long as it kept a copy of the key.
func revokeSecretKey(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	prev database.MemberRole,
	next database.MemberRole,
) (bool, error) {
	if !projects.CanReadSecretKey(prev) || projects.CanReadSecretKey(next) {
		return false, nil
	}

	if _, err := projects.RotateSecretKey(ctx, be, projectID); err != nil {
		return false, err
	}

	return true, nil
}
