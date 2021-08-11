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

package backend

import (
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/types"
)

// Config is the configuration for creating a Backend instance.
type Config struct {
	// SnapshotThreshold is the threshold that determines if changes should be
	// sent with snapshot when the number of changes is greater than this value.
	SnapshotThreshold uint64 `json:"SnapshotThreshold"`

	// SnapshotInterval is the interval of changes to create a snapshot.
	SnapshotInterval uint64 `json:"SnapshotInterval"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL string `json:"AuthWebhookURL"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods []string `json:"AuthWebhookMethods"`

	// AuthorizationWebhookMaxRetries is the max count
	// that retries the authorization webhook.
	AuthorizationWebhookMaxRetries uint64 `json:"AuthorizationWebhookMaxRetries"`

	// AuthorizationWebhookMaxWaitIntervalMillis is the max interval
	// that waits before retrying the authorization webhook.
	AuthorizationWebhookMaxWaitIntervalMillis uint64 `json:"AuthorizationWebhookMaxWaitIntervalMillis"`

	// AuthorizationWebhookCacheAuthorizedTTLSec is the TTL value to set when caching the authorized result.
	AuthorizationWebhookCacheAuthorizedTTLSec uint64 `json:"AuthorizationWebhookCacheAuthorizedTTLSec"`

	// AuthorizationWebhookCacheAuthorizedTTLSec is the TTL value to set when caching the unauthorized result.
	AuthorizationWebhookCacheUnauthorizedTTLSec uint64 `json:"AuthorizationWebhookCacheUnauthorizedTTLSec"`
}

// RequireAuth returns whether the given method require authorization.
func (c *Config) RequireAuth(method types.Method) bool {
	if len(c.AuthWebhookURL) == 0 {
		return false
	}

	if len(c.AuthWebhookMethods) == 0 {
		return true
	}

	for _, m := range c.AuthWebhookMethods {
		if types.Method(m) == method {
			return true
		}
	}

	return false
}

// Validate validates this config.
func (c *Config) Validate() error {
	for _, method := range c.AuthWebhookMethods {
		if !types.IsAuthMethod(method) {
			return fmt.Errorf("not supported method for authorization webhook: %s", method)
		}
	}

	return nil
}
