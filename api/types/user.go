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
 *
 */

package types

import "time"

// AccessScope is the scope of access for a user.
type AccessScope string

const (
	// AccessScopeAdmin is the full administrative access scope for a user.
	AccessScopeAdmin AccessScope = "admin"

	// AccessScopeProject is the project-specific access scope for a user.
	AccessScopeProject AccessScope = "project"
)

// User is a user that can access the project.
type User struct {
	// ID is the unique ID of the user.
	ID ID `json:"id"`

	// AuthProvider is the authentication provider of the user. It can be
	// "github" or empty string for local authentication.
	AuthProvider string `json:"auth_provider"`

	// Username is the username of the user.
	Username string `json:"username"`

	// CreatedAt is the time when the user was created.
	CreatedAt time.Time `json:"created_at"`

	// AccessScope is the scope of access for the user.
	AccessScope AccessScope `json:"access_scope"`
}
