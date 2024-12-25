/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"syscall"
	gotime "time"
)

var (
	// ErrUnexpectedStatusCode is returned when the webhook returns an unexpected status code.
	ErrUnexpectedStatusCode = errors.New("unexpected status code from webhook")

	// ErrWebhookTimeout is returned when the webhook times out.
	ErrWebhookTimeout = errors.New("webhook timeout")
)

// WithExponentialBackoff retries the given webhookFn with exponential backoff.
func WithExponentialBackoff(ctx context.Context, maxRetries uint64, baseInterval, maxInterval gotime.Duration,
	webhookFn func() (int, error)) error {
	var retries uint64
	var statusCode int
	for retries <= maxRetries {
		statusCode, err := webhookFn()
		if !shouldRetry(statusCode, err) {
			if errors.Is(err, ErrUnexpectedStatusCode) {
				return fmt.Errorf("%d: %w", statusCode, ErrUnexpectedStatusCode)
			}

			return err
		}

		waitBeforeRetry := waitInterval(retries, baseInterval, maxInterval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gotime.After(waitBeforeRetry):
		}

		retries++
	}

	return fmt.Errorf("unexpected status code from webhook %d: %w", statusCode, ErrWebhookTimeout)
}

// waitInterval returns the interval of given retries. it returns maxWaitInterval
func waitInterval(retries uint64, baseInterval, maxWaitInterval gotime.Duration) gotime.Duration {
	interval := gotime.Duration(math.Pow(2, float64(retries))) * baseInterval
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
