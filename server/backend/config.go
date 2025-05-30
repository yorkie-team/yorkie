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
	"os"
	"time"
)

// Config is the configuration for creating a Backend instance.
type Config struct {
	// AdminUser is the name of the default admin user who has full permissions.
	// Set once on first-run. Default is "admin".
	AdminUser string `yaml:"AdminUser"`

	// AdminPassword is the password of the default admin. Default is "admin".
	AdminPassword string `yaml:"AdminPassword"`

	// SecretKey is the secret key for signing authentication tokens.
	SecretKey string `yaml:"SecretKey"`

	// AdminTokenDuration is the duration of the admin token. Default is "7d".
	AdminTokenDuration string `yaml:"AdminTokenDuration"`

	// UseDefaultProject is whether to use the default project. Even if public
	// key is not provided from the client, the default project will be used. If
	// we are using server as single-tenant mode, this should be set to true.
	UseDefaultProject bool `yaml:"UseDefaultProject"`

	// ClientDeactivateThreshold is deactivate threshold of clients in specific project for housekeeping.
	ClientDeactivateThreshold string `yaml:"ClientDeactivateThreshold"`

	// SnapshotThreshold is the threshold that determines if changes should be
	// sent with snapshot when the number of changes is greater than this value.
	SnapshotThreshold int64 `yaml:"SnapshotThreshold"`

	// SnapshotInterval is the interval of changes to create a snapshot.
	SnapshotInterval int64 `yaml:"SnapshotInterval"`

	// SnapshotDisableGC is whether to disable garbage collection of snapshots.
	SnapshotDisableGC bool

	// AuthWebhookMaxRetries is the max count that retries the authorization webhook.
	AuthWebhookMaxRetries uint64 `yaml:"AuthWebhookMaxRetries"`

	// AuthWebhookMaxWaitInterval is the max interval that waits before retrying the authorization webhook.
	AuthWebhookMaxWaitInterval string `yaml:"AuthWebhookMaxWaitInterval"`

	// AuthWebhookMinWaitInterval is the min interval that waits before retrying the authorization webhook.
	AuthWebhookMinWaitInterval string `yaml:"AuthWebhookMinWaitInterval"`

	// AuthWebhookRequestTimeout is the max waiting time per auth webhook request.
	AuthWebhookRequestTimeout string `yaml:"AuthWebhookRequestTimeout"`

	// AuthWebhookCacheSize is the cache size of the authorization webhook.
	AuthWebhookCacheSize int `yaml:"AuthWebhookCacheSize"`

	// AuthWebhookCacheTTL is the TTL value to set when caching the authorized result.
	AuthWebhookCacheTTL string `yaml:"AuthWebhookCacheTTL"`

	// SnapshotCacheSize is the cache size of the snapshot.
	SnapshotCacheSize int `yaml:"SnapshotCacheSize"`

	// EventWebhookMaxRetries is the max count that retries the event webhook.
	EventWebhookMaxRetries uint64 `yaml:"EventWebhookMaxRetries"`

	// EventWebhookMaxWaitInterval is the max interval that waits before retrying the event webhook.
	EventWebhookMaxWaitInterval string `yaml:"EventWebhookMaxWaitInterval"`

	// EventWebhookMinWaitInterval is the min interval that waits before retrying the event webhook.
	EventWebhookMinWaitInterval string `yaml:"EventWebhookMinWaitInterval"`

	// EventWebhookRequestTimeout is the max waiting time per event webhook request.
	EventWebhookRequestTimeout string `yaml:"EventWebhookRequestTimeout"`

	// ProjectCacheSize is the cache size of the project metadata.
	ProjectCacheSize int `yaml:"ProjectCacheSize"`

	// ProjectCacheTTL is the TTL value to set when caching the project metadata.
	ProjectCacheTTL string `yaml:"ProjectCacheTTL"`

	// Hostname is yorkie server hostname. hostname is used by metrics.
	Hostname string `yaml:"Hostname"`

	// GatewayAddr is the address of the gateway server.
	GatewayAddr string `yaml:"GatewayAddr"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.ClientDeactivateThreshold); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--client-deactivate-threshold" flag: %w`,
			c.ClientDeactivateThreshold,
			err,
		)
	}

	if _, err := time.ParseDuration(c.AuthWebhookMaxWaitInterval); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--auth-webhook-max-wait-interval" flag: %w`,
			c.AuthWebhookMaxWaitInterval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.AuthWebhookMinWaitInterval); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--auth-webhook-min-wait-interval" flag: %w`,
			c.AuthWebhookMinWaitInterval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.AuthWebhookRequestTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--auth-webhook-request-timeout" flag: %w`,
			c.AuthWebhookRequestTimeout,
			err,
		)
	}

	if _, err := time.ParseDuration(c.AuthWebhookCacheTTL); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--auth-webhook-cache-ttl" flag: %w`,
			c.AuthWebhookCacheTTL,
			err,
		)
	}

	if _, err := time.ParseDuration(c.EventWebhookMaxWaitInterval); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--event-webhook-max-wait-interval" flag: %w`,
			c.EventWebhookMaxWaitInterval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.EventWebhookMinWaitInterval); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--event-webhook-min-wait-interval" flag: %w`,
			c.EventWebhookMinWaitInterval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.EventWebhookRequestTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--event-webhook-request-timeout" flag: %w`,
			c.EventWebhookRequestTimeout,
			err,
		)
	}
	if _, err := time.ParseDuration(c.ProjectCacheTTL); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--project-info-cache-ttl" flag: %w`,
			c.ProjectCacheTTL,
			err,
		)
	}

	return nil
}

// ParseAdminTokenDuration returns admin token duration.
func (c *Config) ParseAdminTokenDuration() time.Duration {
	result, err := time.ParseDuration(c.AdminTokenDuration)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse admin token duration: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseAuthWebhookMaxWaitInterval returns max wait interval.
func (c *Config) ParseAuthWebhookMaxWaitInterval() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookMaxWaitInterval)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse auth webhook max wait interval: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseAuthWebhookMinWaitInterval returns min wait interval.
func (c *Config) ParseAuthWebhookMinWaitInterval() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookMinWaitInterval)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse auth webhook min wait interval: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseAuthWebhookRequestTimeout returns request timeout.
func (c *Config) ParseAuthWebhookRequestTimeout() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookRequestTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse auth webhook request timeout: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseAuthWebhookCacheTTL returns TTL for authorized cache.
func (c *Config) ParseAuthWebhookCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookCacheTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse auth webhook cache ttl: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseEventWebhookMaxWaitInterval returns max wait interval.
func (c *Config) ParseEventWebhookMaxWaitInterval() time.Duration {
	result, err := time.ParseDuration(c.EventWebhookMaxWaitInterval)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse event webhook max wait interval: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseEventWebhookMinWaitInterval returns min wait interval.
func (c *Config) ParseEventWebhookMinWaitInterval() time.Duration {
	result, err := time.ParseDuration(c.EventWebhookMinWaitInterval)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse event webhook min wait interval: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseEventWebhookRequestTimeout returns request timeout.
func (c *Config) ParseEventWebhookRequestTimeout() time.Duration {
	result, err := time.ParseDuration(c.EventWebhookRequestTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse event webhook request timeout: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseProjectCacheTTL returns TTL for project metadata cache.
func (c *Config) ParseProjectCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.ProjectCacheTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse project metadata cache ttl: %w", err)
		os.Exit(1)
	}

	return result
}
