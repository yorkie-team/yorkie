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

// cachePolicy decides which cached authorization decisions a check may be
// answered from.
type cachePolicy int

const (
	// cacheAnyDecision answers from whatever decision is cached, which is the
	// amortization the configured AuthWebhookCacheTTL exists to provide.
	cacheAnyDecision cachePolicy = iota

	// cacheDenialsOnly answers a denial from the cache but asks the webhook
	// again before allowing. Watch admissions and lease renewals use it: a
	// revoked client must not get the stream back from a stale allow, while a
	// client retrying a denied request is still answered from the cache
	// instead of being amplified into the project's webhook.
	cacheDenialsOnly
)

// verifyAccessWithCache checks authorization against the cache and, when the
// cache cannot answer under the given policy, against the webhook. Every
// decision obtained from the webhook is written back to the cache, so a
// revocation one Watch stream observes also denies the other RPCs that read it.
func verifyAccessWithCache(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	token string,
	accessInfo *types.AccessInfo,
	policy cachePolicy,
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

	cacheKey := generateCacheKey(prj.PublicKey, body)
	if entry, ok := be.Cache.AuthWebhook.Get(cacheKey); ok {
		decision := handleWebhookResponse(entry.First, entry.Second)
		if policy == cacheAnyDecision || isDenial(decision) {
			return decision
		}
	}

	options, err := prj.GetAuthWebhookOptions()
	if err != nil {
		return fmt.Errorf("verify access: %w", err)
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

	// A decision obtained past a cached allow is still written back, so a
	// denial observed by a Watch lease replaces the stale allow the other RPCs
	// would otherwise keep reading until the TTL elapsed.
	// TODO(hackerwins): We should consider caching the response of Unauthorized as well.
	if status != http.StatusUnauthorized {
		be.Cache.AuthWebhook.Add(
			cacheKey,
			pkgtypes.Pair[int, *types.AuthWebhookResponse]{First: status, Second: res},
		)
	}

	return handleWebhookResponse(status, res)
}

// isDenial reports whether the given decision refused the access, as opposed
// to leaving it unanswered.
func isDenial(err error) bool {
	return errors.IsStatus(err, errors.ErrCodePermissionDenied) ||
		errors.IsStatus(err, errors.ErrCodeUnauthenticated)
}

// generateCacheKey creates a unique key for caching webhook responses.
func generateCacheKey(publicKey string, body []byte) string {
	return fmt.Sprintf("%s:auth:%s", publicKey, body)
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
