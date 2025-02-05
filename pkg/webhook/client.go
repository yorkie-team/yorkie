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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/server/logging"
	"io"
	"log"
	"net/http"
	"syscall"
	"time"
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
	MinWaitInterval time.Duration
	MaxWaitInterval time.Duration
	RequestTimeout  time.Duration
}

// Client is a client for the webhook.
type Client[Req any, Res any] struct {
	cache       *cache.LRUExpireCache[string, types.Pair[int, *Res]]
	retryClient *retryablehttp.Client
	options     Options
}

// NewClient creates a new instance of Client.
func NewClient[Req any, Res any](
	Cache *cache.LRUExpireCache[string, types.Pair[int, *Res]],
	options Options,
) *Client[Req, Res] {
	return &Client[Req, Res]{
		cache: Cache,
		retryClient: &retryablehttp.Client{
			HTTPClient: &http.Client{
				Timeout: options.RequestTimeout,
			},
			RetryMax:     int(options.MaxRetries),
			RetryWaitMin: options.MinWaitInterval,
			RetryWaitMax: options.MaxWaitInterval,
			CheckRetry:   shouldRetry,
			Logger:       nil,
			Backoff:      retryablehttp.DefaultBackoff,
			ErrorHandler: func(resp *http.Response, err error, numTries int) (*http.Response, error) {
				if err == nil && numTries == int(options.MaxRetries)+1 {
					return nil, ErrWebhookTimeout
				}
				return resp, fmt.Errorf("after %d attempts, errors were: %w", numTries, err)
			},
		},
		options: options,
	}
}

// Send sends the given request to the webhook.
func (c *Client[Req, Res]) Send(
	ctx context.Context,
	CacheKeyPrefix, url, HMACKey string,
	reqData Req,
) (*Res, int, error) {
	body, err := json.Marshal(reqData)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal webhook request: %w", err)
	}

	cacheKey := CacheKeyPrefix + ":" + string(body)
	if entry, ok := c.cache.Get(cacheKey); ok {
		return entry.Second, entry.First, nil
	}

	req, err := c.buildRequest(ctx, url, HMACKey, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.retryClient.Do(req)
	if err != nil {
		var statusCode int
		if resp != nil {
			statusCode = resp.StatusCode
		}
		log.Println(err)

		return nil, statusCode, fmt.Errorf("post to webhook: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			// TODO(hackerwins): Consider to remove the dependency of logging.
			logging.From(ctx).Error(err)
		}
	}()

	if !isExpectedCode(resp.StatusCode) {
		return nil, resp.StatusCode, fmt.Errorf("%d: %w", resp.StatusCode, ErrUnexpectedStatusCode)
	}

	var res Res
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, resp.StatusCode, ErrUnexpectedResponse
	}

	// TODO(hackerwins): We should consider caching the response of Unauthorized as well.
	if resp.StatusCode != http.StatusUnauthorized {
		c.cache.Add(cacheKey, types.Pair[int, *Res]{First: resp.StatusCode, Second: &res}, c.options.CacheTTL)
	}

	return &res, resp.StatusCode, nil
}

func (c *Client[Req, Res]) buildRequest(ctx context.Context, url, HMACKey string, body []byte) (*retryablehttp.Request, error) {
	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("create POST request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if HMACKey != "" {
		if err := setSignature(req, body, HMACKey); err != nil {
			return req, fmt.Errorf("set HMAC signature: %w", err)
		}
	}

	return req, nil
}

func setSignature(req *retryablehttp.Request, Data []byte, HMACKey string) error {
	mac := hmac.New(sha256.New, []byte(HMACKey))
	if _, err := mac.Write(Data); err != nil {
		return fmt.Errorf("write HMAC body: %w", err)
	}
	signature := mac.Sum(nil)
	signatureHex := hex.EncodeToString(signature)
	req.Header.Set("X-Signature-256", fmt.Sprintf("sha256=%s", signatureHex))

	return nil
}

// shouldRetry returns true if the given error should be retried.
// Refer to https://github.com/kubernetes/kubernetes/search?q=DefaultShouldRetry
func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// If the connection is reset, we should retry.
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && errors.Is(errno, syscall.ECONNRESET) {
			return true, nil
		}

		return false, err
	}

	if resp != nil {
		code := resp.StatusCode
		if isExpectedCode(code) {
			return false, nil
		}
		if isRetryCode(code) {
			return true, nil
		}
	}

	return false, nil
}

func isExpectedCode(code int) bool {
	return code == http.StatusOK || code == http.StatusUnauthorized || code == http.StatusForbidden
}

func isRetryCode(code int) bool {
	return code == http.StatusInternalServerError ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout ||
		code == http.StatusTooManyRequests
}
