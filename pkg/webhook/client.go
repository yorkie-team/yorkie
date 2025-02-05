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
	"io"
	"net/http"
	"syscall"
	"time"

	"github.com/hashicorp/go-retryablehttp"

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
	MaxRetries      uint64
	MinWaitInterval time.Duration
	MaxWaitInterval time.Duration
	RequestTimeout  time.Duration
}

// Client is a client for the webhook.
type Client[Req any, Res any] struct {
	retryClient *retryablehttp.Client
	options     Options
}

// NewClient creates a new instance of Client.
func NewClient[Req any, Res any](
	options Options,
) *Client[Req, Res] {
	retryClient := &retryablehttp.Client{
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
	}

	return &Client[Req, Res]{
		retryClient: retryClient,
		options:     options,
	}
}

// Send sends the given request to the webhook.
func (c *Client[Req, Res]) Send(
	ctx context.Context,
	url, hmacKey string,
	body []byte,
) (*Res, int, error) {

	req, err := c.buildRequest(ctx, url, hmacKey, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.retryClient.Do(req)
	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}

		return nil, statusCode, fmt.Errorf("post webhook request: %w", err)
	}

	if !isExpectedCode(resp.StatusCode) {
		return nil, resp.StatusCode, fmt.Errorf("%d: %w", resp.StatusCode, ErrUnexpectedStatusCode)
	}

	var res Res
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, resp.StatusCode, ErrUnexpectedResponse
	}

	return &res, resp.StatusCode, nil
}

// buildRequest creates a new HTTP POST request with the appropriate headers.
func (c *Client[Req, Res]) buildRequest(ctx context.Context, url, hmacKey string, body []byte) (*retryablehttp.Request, error) {
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("create POST request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if hmacKey != "" {
		if err := setSignature(req, body, hmacKey); err != nil {
			return nil, fmt.Errorf("set HMAC signature: %w", err)
		}
	}

	return req, nil
}

// setSignature sets the HMAC signature header for the request.
func setSignature(req *retryablehttp.Request, data []byte, hmacKey string) error {
	mac := hmac.New(sha256.New, []byte(hmacKey))
	if _, err := mac.Write(data); err != nil {
		return fmt.Errorf("write HMAC body: %w", err)
	}
	signatureHex := hex.EncodeToString(mac.Sum(nil))
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

// isExpectedCode checks if the status code is acceptable.
func isExpectedCode(code int) bool {
	return code == http.StatusOK ||
		code == http.StatusUnauthorized ||
		code == http.StatusForbidden
}

// isRetryCode checks if the status code is one that should trigger a retry.
func isRetryCode(code int) bool {
	return code == http.StatusInternalServerError ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout ||
		code == http.StatusTooManyRequests
}
