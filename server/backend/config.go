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

	// SnapshotDisableGC is whether to disable garbage collection of snapshots.
	SnapshotDisableGC bool `yaml:"SnapshotDisableGC"`

	// AuthWebhookCacheSize is the cache size of the authorization webhook.
	AuthWebhookCacheSize int `yaml:"AuthWebhookCacheSize"`

	// AuthWebhookCacheTTL is the TTL value to set when caching the authorized result.
	AuthWebhookCacheTTL string `yaml:"AuthWebhookCacheTTL"`

	// SnapshotCacheSize is the cache size of the snapshot.
	SnapshotCacheSize int `yaml:"SnapshotCacheSize"`

	// ChannelSessionCountCacheSize is the cache size of the session count.
	ChannelSessionCountCacheSize int `yaml:"ChannelSessionCountCacheSize"`

	// ChannelSessionCountCacheTTL is the TTL value for session count cache.
	ChannelSessionCountCacheTTL string `yaml:"ChannelSessionCountCacheTTL"`

	// PresenceTTL is the time-to-live duration for presence sessions.
	// If a presence is not refreshed within this duration, it will be removed.
	// Default is "60s".
	PresenceTTL string `yaml:"PresenceTTL"`

	// PresenceCleanupInterval is the interval for running cleanup of expired presences.
	// Default is "10s".
	PresenceCleanupInterval string `yaml:"PresenceCleanupInterval"`

	// Hostname is yorkie server hostname. hostname is used by metrics.
	Hostname string `yaml:"Hostname"`

	// GatewayAddr is the address of the gateway server. It is used to unicast
	// messages to other nodes in consistent-hashing based routing.
	GatewayAddr string `yaml:"GatewayAddr"`

	// RPCAddr is the address of the RPC server. It is used to broadcast
	// messages to other nodes in the cluster.
	RPCAddr string `yaml:"RPCAddr"`

	// DisableWebhookValidation is whether to disable webhook validation.
	DisableWebhookValidation bool `yaml:"DisableWebhookValidation"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.AuthWebhookCacheTTL); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--auth-webhook-cache-ttl" flag: %w`,
			c.AuthWebhookCacheTTL,
			err,
		)
	}
	if c.ChannelSessionCountCacheTTL != "" {
		if _, err := time.ParseDuration(c.ChannelSessionCountCacheTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--channel-session-count-cache-ttl" flag: %w`,
				c.ChannelSessionCountCacheTTL,
				err,
			)
		}
	}
	if c.PresenceTTL != "" {
		if _, err := time.ParseDuration(c.PresenceTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--presence-ttl" flag: %w`,
				c.PresenceTTL,
				err,
			)
		}
	}
	if c.PresenceCleanupInterval != "" {
		if _, err := time.ParseDuration(c.PresenceCleanupInterval); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--presence-cleanup-interval" flag: %w`,
				c.PresenceCleanupInterval,
				err,
			)
		}
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

// ParseAuthWebhookCacheTTL returns TTL for authorized cache.
func (c *Config) ParseAuthWebhookCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookCacheTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse auth webhook cache ttl: %w", err)
		os.Exit(1)
	}

	return result
}

// ParseChannelSessionCountCacheTTL returns TTL for session count cache.
func (c *Config) ParseChannelSessionCountCacheTTL() time.Duration {
	if c.ChannelSessionCountCacheTTL == "" {
		return 30 * time.Second // Default: 30 seconds
	}

	result, err := time.ParseDuration(c.ChannelSessionCountCacheTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse session count cache ttl: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParsePresenceTTL returns TTL for presence sessions.
func (c *Config) ParsePresenceTTL() time.Duration {
	if c.PresenceTTL == "" {
		return 60 * time.Second // Default: 60 seconds
	}

	result, err := time.ParseDuration(c.PresenceTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse presence ttl: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParsePresenceCleanupInterval returns the interval for presence cleanup.
func (c *Config) ParsePresenceCleanupInterval() time.Duration {
	if c.PresenceCleanupInterval == "" {
		return 10 * time.Second // Default: 10 seconds
	}

	result, err := time.ParseDuration(c.PresenceCleanupInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse presence cleanup interval: %v\n", err)
		os.Exit(1)
	}

	return result
}
