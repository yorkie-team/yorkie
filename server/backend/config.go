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

const (
	// DefaultWatchHeartbeatIntervalMin is the shortest heartbeat interval a
	// client can obtain. It is well under the idle timeout of a typical proxy
	// or load balancer, so a client sitting behind one can still ask for an
	// interval that keeps its stream from being reaped, while a client asking
	// for a far shorter one is held here.
	DefaultWatchHeartbeatIntervalMin = 5 * time.Second

	// DefaultWatchHeartbeatIntervalMax is the longest heartbeat interval a
	// client can obtain. Beyond a minute a heartbeat tells a client little it
	// would not learn as quickly from its own traffic.
	DefaultWatchHeartbeatIntervalMax = 60 * time.Second
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
	//
	// It applies only to the client public key on YorkieService. It never
	// waives the project secret key on AdminService, where a request without
	// one is rejected whatever this says.
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

	// PresenceBaseCacheSize is the number of (document, client) presences the
	// server keeps to fold presence patches. An evicted entry costs the client
	// one refused push and a resend of its full presence.
	PresenceBaseCacheSize int `yaml:"PresenceBaseCacheSize"`

	// ChannelSessionTTL is the time-to-live duration for channel sessions.
	// If a channel session is not refreshed within this duration, it will be removed.
	// Default is "60s".
	ChannelSessionTTL string `yaml:"ChannelSessionTTL"`

	// ChannelSessionCleanupInterval is the interval for running cleanup of expired channel sessions.
	// Default is "10s".
	ChannelSessionCleanupInterval string `yaml:"ChannelSessionCleanupInterval"`

	// WatchHeartbeatIntervalMin is the shortest heartbeat interval the server
	// will serve on an idle Watch stream. A client asking for less is clamped
	// up to it, which is what stops one client from asking for a 1s heartbeat
	// and multiplying the load a hosted server carries. Default is "5s".
	WatchHeartbeatIntervalMin string `yaml:"WatchHeartbeatIntervalMin"`

	// WatchHeartbeatIntervalMax is the longest heartbeat interval the server
	// will serve, and doubles as the server-wide kill switch: "0s" means the
	// server serves no heartbeat however short an interval a client asks for.
	// Default is "60s".
	//
	// Heartbeats are safe to leave on by default because they are opt-in per
	// stream: only a WatchRequest carrying a non-zero heartbeat_interval_ms
	// gets them, and a client old enough to mistake a WatchHeartbeat for a
	// terminal response is also old enough not to ask for one.
	WatchHeartbeatIntervalMax string `yaml:"WatchHeartbeatIntervalMax"`

	// WatchHeartbeatInterval is deprecated and ignored. It was the single
	// server-wide interval from before heartbeats were opt-in per stream. Any
	// value it held, "0s" included, would now either keep the heartbeat from
	// clients that asked for it or force it on clients that did not -- and a
	// client older than the heartbeat ends its watch on the first one -- so the
	// server reads neither. Use WatchHeartbeatIntervalMin/Max instead, with a
	// zero maximum to turn heartbeats off. Setting it only logs a warning.
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
	var heartbeatMin, heartbeatMax time.Duration
	if c.WatchHeartbeatIntervalMin != "" {
		parsed, err := time.ParseDuration(c.WatchHeartbeatIntervalMin)
		if err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--backend-watch-heartbeat-interval-min" flag: %w`,
				c.WatchHeartbeatIntervalMin,
				err,
			)
		}
		if parsed < 0 {
			return fmt.Errorf(
				`invalid argument "%s" for "--backend-watch-heartbeat-interval-min" flag: must not be negative`,
				c.WatchHeartbeatIntervalMin,
			)
		}
		heartbeatMin = parsed
	}
	if c.WatchHeartbeatIntervalMax != "" {
		parsed, err := time.ParseDuration(c.WatchHeartbeatIntervalMax)
		if err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--backend-watch-heartbeat-interval-max" flag: %w`,
				c.WatchHeartbeatIntervalMax,
				err,
			)
		}
		if parsed < 0 {
			return fmt.Errorf(
				`invalid argument "%s" for "--backend-watch-heartbeat-interval-max" flag: must not be negative`,
				c.WatchHeartbeatIntervalMax,
			)
		}
		heartbeatMax = parsed
	}
	// A maximum of zero is the kill switch, not a bound, so it is the one case
	// where a maximum below the minimum is meaningful.
	if heartbeatMax > 0 && heartbeatMin > heartbeatMax {
		return fmt.Errorf(
			`invalid argument "%s" for "--backend-watch-heartbeat-interval-min" flag: must not exceed the maximum "%s"`,
			c.WatchHeartbeatIntervalMin,
			c.WatchHeartbeatIntervalMax,
		)
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

// WatchHeartbeatPolicy bounds the heartbeat interval a Watch stream may ask
// for. A zero Max disables the heartbeat server-wide.
type WatchHeartbeatPolicy struct {
	Min time.Duration
	Max time.Duration
}

// ParseWatchHeartbeatPolicy returns the heartbeat policy this server applies
// to an idle Watch stream: a client names the interval it wants on its
// WatchRequest and the server clamps it into [Min, Max]. The deprecated
// WatchHeartbeatInterval is not read.
func (c *Config) ParseWatchHeartbeatPolicy() WatchHeartbeatPolicy {
	return WatchHeartbeatPolicy{
		Min: c.parseHeartbeatBound(
			c.WatchHeartbeatIntervalMin,
			DefaultWatchHeartbeatIntervalMin,
			"min",
		),
		Max: c.parseHeartbeatBound(
			c.WatchHeartbeatIntervalMax,
			DefaultWatchHeartbeatIntervalMax,
			"max",
		),
	}
}

// parseHeartbeatBound parses one heartbeat bound, falling back to the given
// default when it is unset. A backend built without going through
// ensureBackendDefaultValue -- an embedded server, a test -- must still get
// the documented bounds rather than a zero that would read as the kill
// switch.
func (c *Config) parseHeartbeatBound(value string, fallback time.Duration, name string) time.Duration {
	if value == "" {
		return fallback
	}

	result, err := time.ParseDuration(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse watch heartbeat interval %s: %v\n", name, err)
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
