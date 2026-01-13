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

// Package invites provides business logic for managing reusable invite links.
package invites

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	goerrors "errors"
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ExpireOption represents invite expiration options.
type ExpireOption int

const (
	ExpireUnspecified ExpireOption = iota
	ExpireOneHour
	ExpireTwentyFourHours
	ExpireSevenDays
)

// Create creates a reusable invite link and returns the invite token and expiresAt.
func Create(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	role database.MemberRole,
	createdBy types.ID,
	expireOption ExpireOption,
) (token string, expiresAt *gotime.Time, err error) {
	var exp *gotime.Time
	switch expireOption {
	case ExpireOneHour:
		t := gotime.Now().Add(1 * gotime.Hour)
		exp = &t
	case ExpireTwentyFourHours:
		t := gotime.Now().Add(24 * gotime.Hour)
		exp = &t
	case ExpireSevenDays:
		t := gotime.Now().Add(7 * 24 * gotime.Hour)
		exp = &t
	default:
		return "", nil, database.ErrInvalidInviteExpireOpt
	}

	// Retry on token collision.
	for i := 0; i < 3; i++ {
		tok, err := newToken()
		if err != nil {
			return "", nil, err
		}

		_, err = be.DB.CreateInviteInfo(ctx, projectID, tok, role, createdBy, exp)
		if err == nil {
			return tok, exp, nil
		}
		if goerrors.Is(err, database.ErrInviteAlreadyExists) {
			continue
		}
		return "", nil, err
	}

	return "", nil, fmt.Errorf("create invite: %w", database.ErrInviteAlreadyExists)
}

// Accept accepts an invite token and ensures the user becomes a member of the project.
func Accept(
	ctx context.Context,
	be *backend.Backend,
	token string,
	userID types.ID,
) (*types.Member, error) {
	invite, err := be.DB.FindInviteInfoByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	if invite.ExpiresAt != nil && !invite.ExpiresAt.After(gotime.Now()) {
		return nil, database.ErrInviteExpired
	}

	_, err = be.DB.CreateMemberInfo(ctx, invite.ProjectID, userID, invite.CreatedBy, invite.Role)
	if err != nil {
		if goerrors.Is(err, database.ErrMemberAlreadyExists) {
			memberInfo, err := be.DB.FindMemberInfo(ctx, invite.ProjectID, userID)
			if err != nil {
				return nil, err
			}
			return memberInfo.ToMember(), nil
		}
		return nil, err
	}

	memberInfo, err := be.DB.FindMemberInfo(ctx, invite.ProjectID, userID)
	if err != nil {
		return nil, err
	}
	return memberInfo.ToMember(), nil
}

func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
