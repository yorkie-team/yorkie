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

// Package members provides business logic for managing project members.
package members

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
)

// Invite invites a user to a project with the specified role.
func Invite(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	username string,
	role string,
	invitedBy types.ID,
) (*types.Member, error) {
	// Find the user by username
	userInfo, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return nil, err
	}

	// Create the member in database
	memberInfo, err := be.DB.CreateMemberInfo(ctx, projectID, userInfo.ID, invitedBy, role)
	if err != nil {
		return nil, err
	}

	// Convert to Member and add username
	member := memberInfo.ToMember()
	member.Username = userInfo.Username

	return member, nil
}

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
	for i, memberInfo := range memberInfos {
		// Get user info to include username
		userInfo, err := be.DB.FindUserInfoByID(ctx, memberInfo.UserID)
		if err != nil {
			return nil, err
		}

		member := memberInfo.ToMember()
		member.Username = userInfo.Username
		members[i] = member
	}

	return members, nil
}

// Remove removes a user from a project.
func Remove(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	username string,
) error {
	// Find the user by username
	userInfo, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return err
	}

	// Delete the member
	return be.DB.DeleteMemberInfo(ctx, projectID, userInfo.ID)
}

// UpdateRole updates the role of a project member.
func UpdateRole(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	username string,
	role string,
) (*types.Member, error) {
	// Find the user by username
	userInfo, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return nil, err
	}

	// Update the role
	memberInfo, err := be.DB.UpdateMemberRole(ctx, projectID, userInfo.ID, role)
	if err != nil {
		return nil, err
	}

	// Convert to Member and add username
	member := memberInfo.ToMember()
	member.Username = userInfo.Username

	return member, nil
}
