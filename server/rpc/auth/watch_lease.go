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

package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// VerifyWatchLease checks the current webhook decision without using the
// admission cache. A cached allow cannot extend an established Watch lease.
func VerifyWatchLease(ctx context.Context, be *backend.Backend, access *types.AccessInfo) error {
	project := projects.From(ctx)
	if !project.RequireAuth(access.Method) {
		return nil
	}

	request := types.AuthWebhookRequest{
		Token:      metadata.From(ctx).Authorization,
		Method:     access.Method,
		Attributes: access.Attributes,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("verify watch lease: %w", err)
	}
	options, err := project.GetAuthWebhookOptions()
	if err != nil {
		return fmt.Errorf("verify watch lease: %w", err)
	}
	response, status, err := be.AuthWebhookClient.Send(ctx, project.AuthWebhookURL, "", body, options)
	if err != nil {
		return fmt.Errorf("verify watch lease: %w", err)
	}
	return handleWebhookResponse(status, response)
}
