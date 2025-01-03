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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"syscall"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/metaerrors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrUnauthenticated is returned when the authentication is failed.
	ErrUnauthenticated = errors.New("unauthenticated")

	// ErrPermissionDenied is returned when the given user is not allowed for the access.
	ErrPermissionDenied = errors.New("method is not allowed for this user")

	// ErrUnexpectedStatusCode is returned when the response code is not 200 from the webhook.
	ErrUnexpectedStatusCode = errors.New("unexpected status code from webhook")

	// ErrUnexpectedResponse is returned when the response from the webhook is not as expected.
	ErrUnexpectedResponse = errors.New("unexpected response from webhook")

	// ErrWebhookTimeout is returned when the webhook does not respond in time.
	ErrWebhookTimeout = errors.New("webhook timeout")
)

// verifyAccess verifies the given user is allowed to access the given method.
func verifyAccess(
	ctx context.Context,
	be *backend.Backend,
	authWebhookURL string,
	token string,
	accessInfo *types.AccessInfo,
) error {
	reqBody, err := json.Marshal(types.AuthWebhookRequest{
		Token:      token,
		Method:     accessInfo.Method,
		Attributes: accessInfo.Attributes,
	})
	if err != nil {
		return fmt.Errorf("marshal auth webhook request: %w", err)
	}

	cacheKey := string(reqBody)
	if entry, ok := be.AuthWebhookCache.Get(cacheKey); ok {
		resp := entry
		if !resp.Allowed {
			return fmt.Errorf("%s: %w", resp.Reason, ErrPermissionDenied)
		}
		return nil
	}

	var authResp *types.AuthWebhookResponse
	if err := withExponentialBackoff(ctx, be.Config, func() (int, error) {
		resp, err := http.Post(
			authWebhookURL,
			"application/json",
			bytes.NewBuffer(reqBody),
		)
		if err != nil {
			return 0, fmt.Errorf("post to webhook: %w", err)
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				logging.From(ctx).Error(err)
			}
		}()

		if resp.StatusCode != http.StatusOK &&
			resp.StatusCode != http.StatusUnauthorized &&
			resp.StatusCode != http.StatusForbidden {
			return resp.StatusCode, ErrUnexpectedStatusCode
		}

		authResp, err = types.NewAuthWebhookResponse(resp.Body)
		if err != nil {
			return resp.StatusCode, err
		}

		if resp.StatusCode == http.StatusOK && authResp.Allowed {
			return resp.StatusCode, nil
		}
		if resp.StatusCode == http.StatusForbidden && !authResp.Allowed {
			return resp.StatusCode, fmt.Errorf("%s: %w", authResp.Reason, ErrPermissionDenied)
		}
		if resp.StatusCode == http.StatusUnauthorized && !authResp.Allowed {
			return resp.StatusCode, metaerrors.New(
				ErrUnauthenticated,
				map[string]string{"reason": authResp.Reason},
			)
		}

		return resp.StatusCode, fmt.Errorf("%d: %w", resp.StatusCode, ErrUnexpectedResponse)
	}); err != nil {
		if errors.Is(err, ErrPermissionDenied) {
			be.AuthWebhookCache.Add(cacheKey, authResp, be.Config.ParseAuthWebhookCacheUnauthTTL())
		}

		return err
	}

	be.AuthWebhookCache.Add(cacheKey, authResp, be.Config.ParseAuthWebhookCacheAuthTTL())

	return nil
}

func withExponentialBackoff(ctx context.Context, cfg *backend.Config, webhookFn func() (int, error)) error {
	var retries uint64
	var statusCode int
	for retries <= cfg.AuthWebhookMaxRetries {
		statusCode, err := webhookFn()
		if !shouldRetry(statusCode, err) {
			if err == ErrUnexpectedStatusCode {
				return fmt.Errorf("%d: %w", statusCode, ErrUnexpectedStatusCode)
			}

			return err
		}

		waitBeforeRetry := waitInterval(retries, cfg.ParseAuthWebhookMaxWaitInterval())

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitBeforeRetry):
		}

		retries++
	}

	return fmt.Errorf("unexpected status code from webhook %d: %w", statusCode, ErrWebhookTimeout)
}

// waitInterval returns the interval of given retries. (2^retries * 100) milliseconds.
func waitInterval(retries uint64, maxWaitInterval time.Duration) time.Duration {
	interval := time.Duration(math.Pow(2, float64(retries))) * 100 * time.Millisecond
	if maxWaitInterval < interval {
		return maxWaitInterval
	}

	return interval
}

// shouldRetry returns true if the given error should be retried.
// Refer to https://github.com/kubernetes/kubernetes/search?q=DefaultShouldRetry
func shouldRetry(statusCode int, err error) bool {
	// If the connection is reset, we should retry.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ECONNRESET
	}

	return statusCode == http.StatusInternalServerError ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout ||
		statusCode == http.StatusTooManyRequests
}
