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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"syscall"
	"time"

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

// Options are the options for the webhook httpClient.
type Options struct {
	RequestTimeout  time.Duration
	MaxRetries      uint64
	MinWaitInterval time.Duration
	MaxWaitInterval time.Duration
}

// Client is a httpClient for the webhook.
type Client[Req any, Res any] struct {
	httpClient *http.Client
	options    Options
}

// NewClient creates a new instance of Client.
func NewClient[Req any, Res any](
	options Options,
) *Client[Req, Res] {
	return &Client[Req, Res]{
		httpClient: &http.Client{
			Timeout: options.RequestTimeout,
		},
		options: options,
	}
}

// Send sends the given request to the webhook.
func (c *Client[Req, Res]) Send(
	ctx context.Context,
	url, hmacKey string,
	body []byte,
) (*Res, int, error) {
	signature, err := createSignature(body, hmacKey)
	if err != nil {
		return nil, 0, fmt.Errorf("create signature: %w", err)
	}

	var res Res
	status, err := c.withExponentialBackoff(ctx, func() (int, error) {
		req, err := c.buildRequest(ctx, url, signature, body)
		if err != nil {
			return 0, fmt.Errorf("build request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("do request: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// TODO(hackerwins): Consider to remove the dependency of logging.
				logging.From(ctx).Error(err)
			}
		}()

		if !isExpectedStatus(resp.StatusCode) {
			return resp.StatusCode, ErrUnexpectedStatusCode
		}

		if _, ok := any(res).(int); ok {
			return resp.StatusCode, nil
		}

		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return resp.StatusCode, ErrUnexpectedResponse
		}

		return resp.StatusCode, nil
	})
	if err != nil {
		return nil, status, err
	}

	return &res, status, nil
}

// buildRequest creates a new HTTP POST request with the appropriate headers.
func (c *Client[Req, Res]) buildRequest(
	ctx context.Context,
	url, hmac string,
	body []byte,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create POST request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if hmac != "" {
		req.Header.Set("X-Signature-256", hmac)
	}

	return req, nil
}

// createSignature sets the HMAC signature header for the request.
func createSignature(data []byte, hmacKey string) (string, error) {
	if hmacKey == "" {
		return "", nil
	}
	mac := hmac.New(sha256.New, []byte(hmacKey))
	if _, err := mac.Write(data); err != nil {
		return "", fmt.Errorf("write HMAC body: %w", err)
	}
	signatureHex := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("sha256=%s", signatureHex), nil
}

func (c *Client[Req, Res]) withExponentialBackoff(ctx context.Context, webhookFn func() (int, error)) (int, error) {
	var retries uint64
	var statusCode int
	var err error

	for retries <= c.options.MaxRetries {
		statusCode, err = webhookFn()
		if !shouldRetry(statusCode, err) {
			if errors.Is(err, ErrUnexpectedStatusCode) {
				return statusCode, fmt.Errorf("%d: %w", statusCode, ErrUnexpectedStatusCode)
			}

			return statusCode, err
		}

		waitBeforeRetry := waitInterval(retries, c.options.MinWaitInterval, c.options.MaxWaitInterval)

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(waitBeforeRetry):
		}

		retries++
	}

	return statusCode, fmt.Errorf("unexpected status code from webhook %d: %w", statusCode, ErrWebhookTimeout)
}

// waitInterval returns the interval of given retries. (2^retries * minWaitInterval) .
func waitInterval(retries uint64, minWaitInterval, maxWaitInterval time.Duration) time.Duration {
	interval := time.Duration(math.Pow(2, float64(retries))) * minWaitInterval
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
		return errors.Is(errno, syscall.ECONNRESET)
	}

	return statusCode == http.StatusInternalServerError ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout ||
		statusCode == http.StatusTooManyRequests
}

func isExpectedStatus(statusCode int) bool {
	return statusCode == http.StatusOK ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden
}
