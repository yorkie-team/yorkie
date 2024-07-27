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

// DeleteAccountByName deletes a user by name.
func DeleteAccountByName(
	ctx context.Context,
	be *backend.Backend,
	username string,
) error {
	if err := be.DB.DeleteUserInfoByName(ctx, username); err != nil {
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
