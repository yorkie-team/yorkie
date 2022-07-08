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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// UserInfo is a structure representing information of a user.
type UserInfo struct {
	ID        types.ID  `bson:"_id"`
	Email     string    `bson:"email"`
	CreatedAt time.Time `bson:"created_at"`
}

// DeepCopy returns a deep copy of the UserInfo
func (i *UserInfo) DeepCopy() *UserInfo {
	if i == nil {
		return nil
	}

	return &UserInfo{
		ID:        i.ID,
		Email:     i.Email,
		CreatedAt: i.CreatedAt,
	}
}

// NewUserInfo creates a new UserInfo of the given email.
func NewUserInfo(email string) *UserInfo {
	return &UserInfo{
		Email:     email,
		CreatedAt: time.Now(),
	}
}
