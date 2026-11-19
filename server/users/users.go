/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package users provides the user related business logic.
package users

import (
	"context"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// SignUp signs up a new user.
func SignUp(
	ctx context.Context,
	be *backend.Backend,
	username,
	password string,
) (*types.User, error) {
	hashed, err := database.HashedPassword(password)
	if err != nil {
		return nil, fmt.Errorf("cannot hash password: %w", err)
	}

	info, err := be.DB.CreateUserInfo(ctx, username, hashed)
	if err != nil {
		return nil, err
	}

	return info.ToUser(), nil
}

// IsCorrectPassword checks if the password is correct.
func IsCorrectPassword(
	ctx context.Context,
	be *backend.Backend,
	username,
	password string,
) (*types.User, error) {
	info, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return nil, err
	}

	if err := database.CompareHashAndPassword(
		info.HashedPassword,
		password,
	); err != nil {
		return nil, err
	}

	return info.ToUser(), nil
}

// GetOrCreateUserByProvider returns the user of the given subject under the
// given auth provider, creating it with the given username when missing.
func GetOrCreateUserByProvider(
	ctx context.Context,
	be *backend.Backend,
	provider string,
	subject string,
	username string,
) (*types.User, error) {
	info, err := be.DB.GetOrCreateUserInfoByProvider(ctx, provider, subject, username)
	if err != nil {
		return nil, err
	}

	return info.ToUser(), nil
}

// GetUserByName returns a user by the given username.
func GetUserByName(
	ctx context.Context,
	be *backend.Backend,
	username string,
) (*types.User, error) {
	info, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return nil, err
	}

	return info.ToUser(), nil
}

// GetUserByID returns a user by ID.
func GetUserByID(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
) (*types.User, error) {
	info, err := be.DB.FindUserInfoByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return info.ToUser(), nil
}

// DeleteAccountByName deletes a user by name, along with every project
// membership it holds.
//
// The memberships are swept twice, around the user document. They are keyed by
// user ID but only reachable by username (members.Remove resolves the name), so
// a row left behind by a deleted user can never be listed or removed again:
// members.List fails the whole project listing on the missing user. The
// deletions are not one transaction, so the first sweep leaves the recoverable
// state — an account that lost its roles and can be invited back — if the user
// deletion then fails.
//
// The second sweep is what makes the pair safe against a concurrent
// AcceptInvite. Nothing on the member write path re-checks that the user still
// exists (invites.Accept upserts a row keyed by user ID alone), so an accept
// that overlaps this call can insert a row after the first sweep. Sweeping
// again after the user document is gone removes it: a request starting later
// cannot reach the write at all, because the admin interceptor resolves the
// caller by username on every call and now answers ErrUserNotFound. What
// remains is only an upsert already in flight when the second sweep runs, which
// needs the member write itself to verify the user to close.
//
// This drops the roles, not the credentials they could read. A copied project
// secret key outlives the role it was read under; RotateProjectKeys is still
// the owner's remedy, by the same reasoning as demotion — and a project whose
// own owner self-deletes keeps an owner ID naming no user
// (docs/design/admin-credentials.md).
func DeleteAccountByName(
	ctx context.Context,
	be *backend.Backend,
	username string,
) error {
	info, err := be.DB.FindUserInfoByName(ctx, username)
	if err != nil {
		return err
	}

	if err := be.DB.DeleteMemberInfosByUser(ctx, info.ID); err != nil {
		return err
	}

	if err := be.DB.DeleteUserInfoByName(ctx, username); err != nil {
		return err
	}

	if err := be.DB.DeleteMemberInfosByUser(ctx, info.ID); err != nil {
		return err
	}

	return nil
}

// ChangePassword changes the password for a user.
func ChangePassword(
	ctx context.Context,
	be *backend.Backend,
	username,
	newPassword string,
) error {
	hashedNewPassword, err := database.HashedPassword(newPassword)
	if err != nil {
		return fmt.Errorf("cannot hash newPassword: %w", err)
	}

	if err := be.DB.ChangeUserPassword(ctx, username, hashedNewPassword); err != nil {
		return err
	}

	return nil
}
