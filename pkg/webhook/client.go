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
	goerrors "errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"syscall"
	"time"

	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrUnexpectedStatusCode is returned when the response code is not 200 from the webhook.
	ErrUnexpectedStatusCode = errors.Internal("unexpected status code from webhook").WithCode("ErrUnexpectedStatusCode")

	// ErrUnexpectedResponse is returned when the response from the webhook is not as expected.
	ErrUnexpectedResponse = errors.Internal("unexpected response from webhook").WithCode("ErrUnexpectedResponse")

	// ErrWebhookTimeout is returned when the webhook does not respond in time.
	ErrWebhookTimeout = errors.Internal("webhook timeout").WithCode("ErrWebhookTimeout")
)

// Options are the options for the webhook httpClient.
type Options struct {
	MaxRetries      uint64
	MinWaitInterval time.Duration
	MaxWaitInterval time.Duration
	RequestTimeout  time.Duration
}

// Client is a httpClient for the webhook.
type Client[Req any, Res any] struct {
	httpClient *http.Client
}

// NewClient creates a new instance of Client. If you only want to get the status code,
// then set Res to int.
func NewClient[Req any, Res any]() *Client[Req, Res] {
	return &Client[Req, Res]{
		httpClient: &http.Client{},
	}
}

// Send sends the given request to the webhook.
func (c *Client[Req, Res]) Send(
	ctx context.Context,
	url, hmacKey string,
	body []byte,
	options Options,
) (*Res, int, error) {
	signature, err := createSignature(body, hmacKey)
	if err != nil {
		return nil, 0, fmt.Errorf("create signature: %w", err)
	}

	var res Res
	status, err := c.withExponentialBackoff(ctx, options, func() (int, error) {
		req, cancel, err := c.buildRequest(ctx, url, signature, options, body)
		if cancel != nil {
			defer cancel()
		}
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

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, fmt.Errorf("read response body: %w", err)
		}

		if err := json.Unmarshal(bodyBytes, &res); err != nil {
			return resp.StatusCode, fmt.Errorf("%w: body=%s", ErrUnexpectedResponse, string(bodyBytes))
		}

		return resp.StatusCode, nil
	})
	if err != nil {
		return nil, status, err
	}

	return &res, status, nil
}

// Close closes the httpClient.
func (c *Client[Req, Res]) Close() {
	c.httpClient.CloseIdleConnections()
}

// buildRequest creates a new HTTP POST request with the appropriate headers.
func (c *Client[Req, Res]) buildRequest(
	ctx context.Context,
	url, hmac string,
	options Options,
	body []byte,
) (*http.Request, context.CancelFunc, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, options.RequestTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("create POST request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if hmac != "" {
		req.Header.Set("X-Signature-256", hmac)
	}

	return req, cancel, nil
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

func (c *Client[Req, Res]) withExponentialBackoff(
	ctx context.Context, options Options,
	webhookFn func() (int, error)) (int, error) {
	var retries uint64
	var statusCode int
	var err error

	for retries <= options.MaxRetries {
		statusCode, err = webhookFn()
		if !shouldRetry(statusCode, err) {
			if goerrors.Is(err, ErrUnexpectedStatusCode) {
				return statusCode, fmt.Errorf("%d: %w", statusCode, ErrUnexpectedStatusCode)
			}

			return statusCode, err
		}

		waitBeforeRetry := waitInterval(retries, options.MinWaitInterval, options.MaxWaitInterval)

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

	return min(interval, maxWaitInterval)
}

// shouldRetry returns true if the given error should be retried.
// Refer to https://github.com/kubernetes/kubernetes/search?q=DefaultShouldRetry
func shouldRetry(statusCode int, err error) bool {
	// If the connection is reset, we should retry.
	var errno syscall.Errno
	if goerrors.As(err, &errno) {
		return goerrors.Is(errno, syscall.ECONNRESET)
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
