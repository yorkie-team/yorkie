/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/metaerrors"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
)

var (
	// ErrUnauthenticated is returned when the authentication is failed.
	ErrUnauthenticated = errors.New("unauthenticated")

	// ErrPermissionDenied is returned when the given user is not allowed for the access.
	ErrPermissionDenied = errors.New("method is not allowed for this user")
)

// verifyAccess verifies the given user is allowed to access the given method.
func verifyAccess(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	token string,
	accessInfo *types.AccessInfo,
) error {
	res, status, err := be.AuthWebhookClient.Send(
		ctx,
		prj.PublicKey+":auth",
		prj.AuthWebhookURL,
		"",
		types.AuthWebhookRequest{
			Token:      token,
			Method:     accessInfo.Method,
			Attributes: accessInfo.Attributes,
		},
	)
	if err != nil {
		return fmt.Errorf("send to webhook: %w", err)
	}

	if status == http.StatusOK && res.Allowed {
		return nil
	}
	if status == http.StatusForbidden && !res.Allowed {
		return metaerrors.New(
			ErrPermissionDenied,
			map[string]string{"reason": res.Reason},
		)
	}
	if status == http.StatusUnauthorized && !res.Allowed {
		return metaerrors.New(
			ErrUnauthenticated,
			map[string]string{"reason": res.Reason},
		)
	}

	return fmt.Errorf("%d: %w", status, webhook.ErrUnexpectedResponse)
}
