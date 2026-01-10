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

package database

import (
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// InviteInfo is a struct for project invite link information.
// This invite is reusable: multiple users can join with the same token.
type InviteInfo struct {
	// ID is the unique ID of the invite.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project.
	ProjectID types.ID `bson:"project_id"`

	// Token is the opaque, random invite token.
	Token string `bson:"token"`

	// Role is the role granted to the invited user.
	Role MemberRole `bson:"role"`

	// CreatedBy is the user who created this invite.
	CreatedBy types.ID `bson:"created_by"`

	// CreatedAt is the time when the invite was created.
	CreatedAt time.Time `bson:"created_at"`

	// ExpiresAt is the time when the invite expires. If nil, it never expires.
	ExpiresAt *time.Time `bson:"expires_at,omitempty"`
}

// NewInviteInfo creates a new InviteInfo.
func NewInviteInfo(projectID types.ID,
	token string,
	role MemberRole,
	createdBy types.ID,
	expiresAt *time.Time,
) (*InviteInfo, error) {
	if token == "" {
		return nil, ErrInvalidInviteToken
	}
	if err := role.Validate(); err != nil {
		return nil, err
	}

	var copiedExpiresAt *time.Time
	if expiresAt != nil {
		t := *expiresAt
		copiedExpiresAt = &t
	}
	return &InviteInfo{
		ProjectID: projectID,
		Token:     token,
		Role:      role,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
		ExpiresAt: copiedExpiresAt,
	}, nil
}

// DeepCopy returns a deep copy of the InviteInfo.
func (i *InviteInfo) DeepCopy() *InviteInfo {
	if i == nil {
		return nil
	}

	var expiresAt *time.Time
	if i.ExpiresAt != nil {
		t := *i.ExpiresAt
		expiresAt = &t
	}

	return &InviteInfo{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		Token:     i.Token,
		Role:      i.Role,
		CreatedBy: i.CreatedBy,
		CreatedAt: i.CreatedAt,
		ExpiresAt: expiresAt,
	}
}
