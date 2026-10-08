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

	"github.com/yorkie-team/yorkie/pkg/cache"
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

	// AuthWebhookCacheDisabled disables the authorization webhook response
	// cache server-wide. When true, every protected RPC calls the webhook.
	AuthWebhookCacheDisabled bool `yaml:"AuthWebhookCacheDisabled"`

	// AuthWebhookCacheTTL is the TTL value to set when caching the authorized
	// result. It must be at least cache.MinTTL.
	AuthWebhookCacheTTL string `yaml:"AuthWebhookCacheTTL"`

	// SnapshotCacheSize is the cache size of the snapshot.
	SnapshotCacheSize int `yaml:"SnapshotCacheSize"`

	// ChannelSessionTTL is the time-to-live duration for channel sessions.
	// If a channel session is not refreshed within this duration, it will be removed.
	// Default is "60s".
	ChannelSessionTTL string `yaml:"ChannelSessionTTL"`

	// ChannelSessionCleanupInterval is the interval for running cleanup of expired channel sessions.
	// Default is "10s".
	ChannelSessionCleanupInterval string `yaml:"ChannelSessionCleanupInterval"`

	// WatchHeartbeatInterval is the interval at which the server sends a
	// heartbeat on an idle Watch stream, so a client can tell a live stream
	// from a half-open one. The interval is advertised to the client in the
	// stream's initialization response. "0s" sends none, which keeps a client
	// from applying an idle timeout, and is the default: a client that
	// predates the heartbeat treats the response it cannot classify as
	// terminal, so heartbeats are opt-in rather than something a server
	// upgrade turns on under old clients.
	WatchHeartbeatInterval string `yaml:"WatchHeartbeatInterval"`

	// ChannelSessionCountCacheSize is the cache size of the session count.
	ChannelSessionCountCacheSize int `yaml:"ChannelSessionCountCacheSize"`

	// ChannelSessionCountCacheTTL is the TTL value for session count cache. It
	// must be at least cache.MinTTL.
	ChannelSessionCountCacheTTL string `yaml:"ChannelSessionCountCacheTTL"`

	// Hostname is yorkie server hostname. hostname is used by metrics.
	Hostname string `yaml:"Hostname"`

	// GatewayAddr is the address of the gateway server. It is used to unicast
	// messages to other nodes in consistent-hashing based routing.
	GatewayAddr string `yaml:"GatewayAddr"`

	// RPCAddr is the address of the RPC server. It is used to broadcast
	// messages to other nodes in the cluster.
	RPCAddr string `yaml:"RPCAddr"`

	// EnableWebhookValidation is whether to enable webhook URL validation
	// to prevent SSRF attacks. If false (default), webhook URL validation
	// is skipped.
	EnableWebhookValidation bool `yaml:"EnableWebhookValidation"`

	// ClusterRPCTimeout is the timeout for individual cluster RPC calls.
	// If a cluster peer does not respond within this duration, the call
	// will be cancelled. Default is "10s".
	ClusterRPCTimeout string `yaml:"ClusterRPCTimeout"`

	// ClusterClientTimeout is the hard limit for the entire HTTP request
	// lifecycle of cluster communication, including DNS, connection, TLS,
	// request and response. This acts as a safety net. Default is "30s".
	ClusterClientTimeout string `yaml:"ClusterClientTimeout"`

	// ClusterClientPoolSize is the number of connections per host in the cluster
	// client pool. Multiple connections reduce HTTP/2 mutex contention under
	// high concurrency. Default is 1.
	ClusterClientPoolSize int `yaml:"ClusterClientPoolSize"`

	// MaxConcurrentClusterRPCs is the maximum number of concurrent cluster
	// RPC calls across the entire server. This prevents goroutine explosion
	// when a cluster peer is slow or unresponsive. Default is 5000.
	MaxConcurrentClusterRPCs int `yaml:"MaxConcurrentClusterRPCs"`

	// ClusterSecret is the shared secret for authenticating inter-node
	// cluster RPCs. If empty, all requests are allowed.
	ClusterSecret string `yaml:"ClusterSecret"`
}

// validateCacheTTL checks that the given TTL parses and is at least
// cache.MinTTL, so that a bad value fails with the flag name at startup
// rather than inside cache construction.
func validateCacheTTL(flag, value string) error {
	if _, err := cache.ParseTTL(value); err != nil {
		return fmt.Errorf(`invalid argument "%s" for "%s" flag: %w`, value, flag, err)
	}
	return nil
}

// Validate validates this config.
func (c *Config) Validate() error {
	if err := validateCacheTTL(
		"--auth-webhook-cache-auth-ttl",
		c.AuthWebhookCacheTTL,
	); err != nil {
		return err
	}
	if c.ChannelSessionCountCacheTTL != "" {
		if err := validateCacheTTL(
			"--channel-session-count-cache-ttl",
			c.ChannelSessionCountCacheTTL,
		); err != nil {
			return err
		}
	}
	if c.ChannelSessionTTL != "" {
		if _, err := time.ParseDuration(c.ChannelSessionTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--channel-session-ttl" flag: %w`,
				c.ChannelSessionTTL,
				err,
			)
		}
	}
	if c.ChannelSessionCleanupInterval != "" {
		if _, err := time.ParseDuration(c.ChannelSessionCleanupInterval); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--channel-session-cleanup-interval" flag: %w`,
				c.ChannelSessionCleanupInterval,
				err,
			)
		}
	}
	if c.WatchHeartbeatInterval != "" {
		if _, err := time.ParseDuration(c.WatchHeartbeatInterval); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--backend-watch-heartbeat-interval" flag: %w`,
				c.WatchHeartbeatInterval,
				err,
			)
		}
	}
	if c.ClusterRPCTimeout != "" {
		if _, err := time.ParseDuration(c.ClusterRPCTimeout); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--cluster-rpc-timeout" flag: %w`,
				c.ClusterRPCTimeout,
				err,
			)
		}
	}
	if c.ClusterClientTimeout != "" {
		if _, err := time.ParseDuration(c.ClusterClientTimeout); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--cluster-client-timeout" flag: %w`,
				c.ClusterClientTimeout,
				err,
			)
		}
	}
	if c.ChannelSessionCountCacheSize <= 0 {
		return fmt.Errorf(
			`invalid argument "%d" for "--channel-session-count-cache-size"`,
			c.ChannelSessionCountCacheSize,
		)
	}
	if c.ClusterClientPoolSize <= 0 {
		return fmt.Errorf(
			`invalid argument "%d" for "--cluster-client-pool-size"`,
			c.ClusterClientPoolSize,
		)
	}
	if c.MaxConcurrentClusterRPCs <= 0 {
		return fmt.Errorf(
			`invalid argument "%d" for "--max-concurrent-cluster-rpcs"`,
			c.MaxConcurrentClusterRPCs,
		)
	}
	return nil
}

// ParseAdminTokenDuration returns admin token duration.
func (c *Config) ParseAdminTokenDuration() time.Duration {
	result, err := time.ParseDuration(c.AdminTokenDuration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse admin token duration: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseAuthWebhookCacheTTL returns TTL for authorized cache.
func (c *Config) ParseAuthWebhookCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.AuthWebhookCacheTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse auth webhook cache ttl: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseChannelSessionTTL returns TTL for channel session.
func (c *Config) ParseChannelSessionTTL() time.Duration {
	result, err := time.ParseDuration(c.ChannelSessionTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse channel session ttl: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseChannelSessionCleanupInterval returns the interval for channel session cleanup.
func (c *Config) ParseChannelSessionCleanupInterval() time.Duration {
	result, err := time.ParseDuration(c.ChannelSessionCleanupInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse channel session cleanup interval: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseWatchHeartbeatInterval returns the interval at which the server sends
// a heartbeat on an idle Watch stream. Zero disables the heartbeat.
func (c *Config) ParseWatchHeartbeatInterval() time.Duration {
	// An unset interval is no heartbeat, the same as "0s". A backend built
	// without going through ensureBackendDefaultValue -- an embedded server,
	// a test -- must not exit the process over an option it never set.
	if c.WatchHeartbeatInterval == "" {
		return 0
	}

	result, err := time.ParseDuration(c.WatchHeartbeatInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse watch heartbeat interval: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseClusterRPCTimeout returns the timeout for cluster RPC calls.
func (c *Config) ParseClusterRPCTimeout() time.Duration {
	result, err := time.ParseDuration(c.ClusterRPCTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse cluster rpc timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseClusterClientTimeout returns the HTTP client timeout for cluster communication.
func (c *Config) ParseClusterClientTimeout() time.Duration {
	result, err := time.ParseDuration(c.ClusterClientTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse cluster client timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseChannelSessionCountCacheTTL returns TTL for session count cache.
func (c *Config) ParseChannelSessionCountCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.ChannelSessionCountCacheTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse channel session count cache ttl: %v\n", err)
		os.Exit(1)
	}

	return result
}
