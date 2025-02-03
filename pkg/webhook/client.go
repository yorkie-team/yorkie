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

// Package webhook provides a client for the webhook.
package webhook

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

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrUnexpectedStatusCode is returned when the response code is not 200 from the webhook.
	ErrUnexpectedStatusCode = errors.New("unexpected status code from webhook")

	// ErrUnexpectedResponse is returned when the response from the webhook is not as expected.
	ErrUnexpectedResponse = errors.New("unexpected response from webhook")

	// ErrWebhookTimeout is returned when the webhook does not respond in time.
	ErrWebhookTimeout = errors.New("webhook timeout")
)

// Options are the options for the webhook client.
type Options struct {
	CacheTTL time.Duration

	MaxRetries      uint64
	MaxWaitInterval time.Duration
}

// Client is a client for the webhook.
type Client[Req any, Res any] struct {
	url     string
	cache   *cache.LRUExpireCache[string, types.Pair[int, *Res]]
	options Options
}

// NewClient creates a new instance of Client.
func NewClient[Req any, Res any](
	url string,
	Cache *cache.LRUExpireCache[string, types.Pair[int, *Res]],
	options Options,
) *Client[Req, Res] {
	return &Client[Req, Res]{
		url:     url,
		cache:   Cache,
		options: options,
	}
}

// Send sends the given request to the webhook.
func (c *Client[Req, Res]) Send(ctx context.Context, req Req) (*Res, int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal webhook request: %w", err)
	}

	cacheKey := string(body)
	if entry, ok := c.cache.Get(cacheKey); ok {
		return entry.Second, entry.First, nil
	}

	var res Res
	status, err := c.withExponentialBackoff(ctx, func() (int, error) {
		// TODO(hackerwins, window9u): We should consider using HMAC to sign the request.
		resp, err := http.Post(
			c.url,
			"application/json",
			bytes.NewBuffer(body),
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

		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return resp.StatusCode, ErrUnexpectedResponse
		}

		return resp.StatusCode, nil
	})
	if err != nil {
		return nil, status, err
	}

	// TODO(hackerwins): We should consider caching the response of Unauthorized as well.
	if status != http.StatusUnauthorized {
		c.cache.Add(cacheKey, types.Pair[int, *Res]{First: status, Second: &res}, c.options.CacheTTL)
	}

	return &res, status, nil
}

func (c *Client[Req, Res]) withExponentialBackoff(ctx context.Context, webhookFn func() (int, error)) (int, error) {
	var retries uint64
	var statusCode int
	for retries <= c.options.MaxRetries {
		statusCode, err := webhookFn()
		if !shouldRetry(statusCode, err) {
			if err == ErrUnexpectedStatusCode {
				return statusCode, fmt.Errorf("%d: %w", statusCode, ErrUnexpectedStatusCode)
			}

			return statusCode, err
		}

		waitBeforeRetry := waitInterval(retries, c.options.MaxWaitInterval)

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(waitBeforeRetry):
		}

		retries++
	}

	return statusCode, fmt.Errorf("unexpected status code from webhook %d: %w", statusCode, ErrWebhookTimeout)
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
