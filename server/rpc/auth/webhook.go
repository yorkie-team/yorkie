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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
)

var (
	// ErrUnauthenticated is returned when the authentication is failed.
	ErrUnauthenticated = errors.Unauthenticated("unauthenticated").WithCode("ErrUnauthenticated")

	// ErrPermissionDenied is returned when the given user is not allowed for the access.
	ErrPermissionDenied = errors.PermissionDenied("not allowed").WithCode("ErrPermissionDenied")
)

// verifyAccess verifies the given user is allowed to access the given method.
//
// A recheck asks the webhook directly and only once: it skips the cached
// decision, which may predate the change being checked, and the project's
// retries, since a failed recheck is retried as a whole by its caller. Its
// answer is still cached, as the freshest decision there is.
//
// With Config.AuthWebhookCacheDisabled, it neither reads nor writes the
// cache, so every call asks the webhook.
func verifyAccess(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	token string,
	accessInfo *types.AccessInfo,
	recheck bool,
) error {
	req := types.AuthWebhookRequest{
		Token:      token,
		Method:     accessInfo.Method,
		Attributes: accessInfo.Attributes,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("verify access: %w", err)
	}

	cacheDisabled := be.Config.AuthWebhookCacheDisabled
	var cacheKey string
	if !cacheDisabled {
		cacheKey = generateCacheKey(prj.PublicKey, body)
		if !recheck {
			if entry, ok := be.Cache.AuthWebhook.Get(cacheKey); ok {
				return handleWebhookResponse(entry.First, entry.Second)
			}
		}
	}

	options, err := prj.GetAuthWebhookOptions()
	if err != nil {
		return fmt.Errorf("verify access: %w", err)
	}
	if recheck {
		options.MaxRetries = 0
	}

	// Read before asking, so an answer that crosses a DropCachedDecisions is
	// seen as predating it and is not written back over the drop.
	var gen uint64
	if !cacheDisabled {
		gen = currentCacheGen()
	}

	res, status, err := be.AuthWebhookClient.Send(
		ctx,
		prj.AuthWebhookURL,
		"",
		body,
		options,
	)
	if err != nil {
		return fmt.Errorf("verify access: %w", err)
	}

	// TODO(hackerwins): We should consider caching the response of Unauthorized as well.
	if !cacheDisabled && status != http.StatusUnauthorized {
		cacheDecision(
			be,
			cacheKey,
			gen,
			pkgtypes.Pair[int, *types.AuthWebhookResponse]{First: status, Second: res},
		)
	}

	return handleWebhookResponse(status, res)
}

// generateCacheKey creates a unique key for caching webhook responses.
func generateCacheKey(publicKey string, body []byte) string {
	return cacheKeyPrefix(publicKey) + string(body)
}

// cacheKeyPrefix returns the prefix shared by every cached webhook response
// of the project with the given public key.
func cacheKeyPrefix(publicKey string) string {
	return publicKey + ":auth:"
}

// handleWebhookResponse processes the webhook response and returns an error if necessary.
func handleWebhookResponse(status int, res *types.AuthWebhookResponse) error {
	if res == nil {
		return fmt.Errorf("nil response for status %d: %w", status, webhook.ErrInvalidJSONResponse)
	}

	switch {
	case status == http.StatusOK && res.Allowed:
		return nil
	case status == http.StatusForbidden && !res.Allowed:
		return errors.WithMetadata(ErrPermissionDenied, map[string]string{"reason": res.Reason})
	case status == http.StatusUnauthorized && !res.Allowed:
		return errors.WithMetadata(ErrUnauthenticated, map[string]string{"reason": res.Reason})
	default:
		return fmt.Errorf("status=%d, allowed=%v, reason=%s: %w",
			status, res.Allowed, res.Reason, webhook.ErrInvalidJSONResponse)
	}
}
