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

// Package authz provides the authorization related business logic.
package authz

import (
	"context"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	// ErrInsufficientPermission is returned when user lacks required permission
	ErrInsufficientPermission = errors.PermissionDenied("insufficient permission")
)

// FindUserRole returns the user's role in the project.
// Returns Owner, Admin, Member or an error if the user has no access.
func FindUserRole(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	projectID types.ID,
) (database.MemberRole, error) {
	// 1. Find project
	info, err := be.DB.FindProjectInfoByID(ctx, projectID)
	if err != nil {
		return "", err
	}

	// 2. Check if user is the owner
	if info.Owner == userID {
		return database.Owner, nil
	}

	// 3. Check if user is a member
	member, err := be.DB.FindMemberInfo(ctx, projectID, userID)
	if err != nil {
		return "", err
	}

	return member.Role, nil
}

// FindUserRoleByName returns the user's role in the project by project name.
// Returns projectID, role, and error.
func FindUserRoleByName(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	projectName string,
) (types.ID, database.MemberRole, error) {
	// 1. Find project by name (no longer requires owner)
	info, err := be.DB.FindProjectInfoByName(ctx, projectName)
	if err != nil {
		return "", "", err
	}

	// 2. Check if user is the owner
	if info.Owner == userID {
		return info.ID, database.Owner, nil
	}

	// 3. Check if user is a member
	member, err := be.DB.FindMemberInfo(ctx, info.ID, userID)
	if err != nil {
		return "", "", err
	}

	return info.ID, member.Role, nil
}

// CheckPermission checks if the user has the required permission level.
// Returns an error if the user doesn't have sufficient permission.
func CheckPermission(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	projectID types.ID,
	required database.MemberRole,
) error {
	role, err := FindUserRole(ctx, be, userID, projectID)
	if err != nil {
		return err
	}

	if !role.IsAtLeast(required) {
		return fmt.Errorf(
			"user has '%s' but '%s' required: %w",
			role,
			required,
			ErrInsufficientPermission,
		)
	}

	return nil
}

// CheckPermissionByName checks permission by project name.
// Returns projectID if the user has sufficient permission.
func CheckPermissionByName(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	projectName string,
	required database.MemberRole,
) (types.ID, error) {
	projectID, role, err := FindUserRoleByName(ctx, be, userID, projectName)
	if err != nil {
		return "", err
	}

	if !role.IsAtLeast(required) {
		return "", fmt.Errorf(
			"user has '%s' but '%s' required: %w",
			role,
			required,
			ErrInsufficientPermission,
		)
	}

	return projectID, nil
}
