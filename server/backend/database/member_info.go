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

package database

import (
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

const (
	// RoleOwner is the owner role of the project.
	RoleOwner = "owner"
	// RoleAdmin is the admin role of the project.
	RoleAdmin = "admin"
	// RoleMember is the member role of the project.
	RoleMember = "member"
)

// MemberInfo is a struct for project member information.
type MemberInfo struct {
	// ID is the unique ID of the project member.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project.
	ProjectID types.ID `bson:"project_id"`

	// UserID is the ID of the user.
	UserID types.ID `bson:"user_id"`

	// Role is the role of the user in the project.
	Role string `bson:"role"`

	// InvitedBy is the ID of the user who invited this member.
	InvitedBy types.ID `bson:"invited_by"`

	// InvitedAt is the time when the member was invited.
	InvitedAt time.Time `bson:"invited_at"`
}

// NewMemberInfo creates a new MemberInfo.
func NewMemberInfo(projectID, userID, invitedBy types.ID, role string) *MemberInfo {
	return &MemberInfo{
		ProjectID: projectID,
		UserID:    userID,
		Role:      role,
		InvitedBy: invitedBy,
		InvitedAt: time.Now(),
	}
}

// DeepCopy returns a deep copy of the MemberInfo.
func (i *MemberInfo) DeepCopy() *MemberInfo {
	if i == nil {
		return nil
	}

	return &MemberInfo{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		UserID:    i.UserID,
		Role:      i.Role,
		InvitedBy: i.InvitedBy,
		InvitedAt: i.InvitedAt,
	}
}

// ToMember converts the MemberInfo to a Member.
func (i *MemberInfo) ToMember() *types.Member {
	return &types.Member{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		UserID:    i.UserID,
		Role:      i.Role,
		InvitedAt: i.InvitedAt,
	}
}
