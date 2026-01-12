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
 *
 */

package types

import "time"

// Member is a member of a project.
type Member struct {
	// ID is the unique ID of the project member.
	ID ID `json:"id"`

	// ProjectID is the ID of the project.
	ProjectID ID `json:"project_id"`

	// UserID is the ID of the user.
	UserID ID `json:"user_id"`

	// Username is the username of the user.
	Username string `json:"username"`

	// Role is the role of the user in the project.
	Role string `json:"role"`

	// InvitedAt is the time when the member was invited.
	InvitedAt time.Time `json:"invited_at"`
}
