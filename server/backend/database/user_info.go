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

package database

import (
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yorkie-team/yorkie/api/types"
)

// ServerActorID is the ID of the server actor.
var ServerActorID = types.ID("000000000000000000000000")

var (
	// ErrMismatchedPassword is returned when the password is mismatched.
	ErrMismatchedPassword = fmt.Errorf("mismatched password")
)

// UserInfo is a structure representing information of a user.
type UserInfo struct {
	// ID is the unique ID of the user.
	ID types.ID `bson:"_id"`

	// AuthProvider is the provider of the authentication. Valid values are
	// "github" and empty string for local authentication.
	AuthProvider string `bson:"auth_provider"`

	// Username is the username of the user.
	Username string `bson:"username"`

	// HashedPassword is the hashed password of the user. It is empty if the
	// user is authenticated by github.
	HashedPassword string `bson:"hashed_password"`

	// CreatedAt is the time when the user was created.
	CreatedAt time.Time `bson:"created_at"`

	// AccessedAt is the time when the user was last accessed.
	AccessedAt time.Time `bson:"accessed_at"`
}

// NewUserInfo creates a new UserInfo of the given username.
func NewUserInfo(username, hashedPassword string) *UserInfo {
	return &UserInfo{
		Username:       username,
		HashedPassword: hashedPassword,
		CreatedAt:      time.Now(),
	}
}

// DeepCopy returns a deep copy of the UserInfo
func (i *UserInfo) DeepCopy() *UserInfo {
	if i == nil {
		return nil
	}

	return &UserInfo{
		ID:             i.ID,
		AuthProvider:   i.AuthProvider,
		Username:       i.Username,
		HashedPassword: i.HashedPassword,
		CreatedAt:      i.CreatedAt,
	}
}

// ToUser converts the UserInfo to a User.
func (i *UserInfo) ToUser() *types.User {
	return &types.User{
		ID:           i.ID,
		AuthProvider: i.AuthProvider,
		Username:     i.Username,
		CreatedAt:    i.CreatedAt,
	}
}

// HashedPassword hashes the given password.
func HashedPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("cannot hash password: %w", err)
	}

	return string(hashed), nil
}

// CompareHashAndPassword compares the hashed password and the password.
func CompareHashAndPassword(hashed, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password)); err != nil {
		return ErrMismatchedPassword
	}

	return nil
}
