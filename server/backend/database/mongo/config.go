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

package mongo

import (
	"fmt"
	"os"
	"time"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

// Below are the default values applied when the matching fields are left
// empty, so that a Config built in code (not only one read from a config
// file) resolves to a usable value.
const (
	// DefaultProjectCacheSize is the default size of the project metadata cache.
	DefaultProjectCacheSize = 256

	// DefaultProjectCacheTTL is the default TTL of the project metadata cache.
	DefaultProjectCacheTTL = 10 * time.Minute

	// DefaultClientCacheTTL is the default TTL of the client cache. The cache
	// is filled by the node that performed the write and has no cross-node
	// invalidation, so the TTL is the only thing that refreshes activation and
	// attachment state on a node that did not perform the write. It is what
	// bounds how long the RPC gates can read a client row the database no
	// longer holds.
	DefaultClientCacheTTL = time.Minute

	// DefaultCacheStatsInterval is the default interval for logging cache statistics.
	DefaultCacheStatsInterval = 30 * time.Second
)

// Config is the configuration for creating a Client instance.
type Config struct {
	ConnectionTimeout string `yaml:"ConnectionTimeout"`
	ConnectionURI     string `yaml:"ConnectionURI"`
	YorkieDatabase    string `yaml:"YorkieDatabase"`
	PingTimeout       string `yaml:"PingTimeout"`

	// MonitoringEnabled determines whether query monitoring is enabled.
	MonitoringEnabled bool `yaml:"MonitoringEnabled"`

	// MonitoringSlowQueryThreshold is the threshold in milliseconds to log slow queries.
	// If a query takes longer than this threshold, it will be logged as a slow query.
	MonitoringSlowQueryThreshold string `yaml:"MonitoringSlowQueryThreshold"`

	// CacheStatsEnabled determines whether cache statistics logging is enabled.
	CacheStatsEnabled bool `yaml:"CacheStatsEnabled"`

	// CacheStatsInterval is the interval for logging cache statistics.
	CacheStatsInterval string `yaml:"CacheStatsInterval"`

	// ClientCacheSize is the size of the client cache. It works as LRU cache.
	ClientCacheSize int `yaml:"ClientCacheSize"`

	// ClientCacheTTL is the TTL value for the client cache.
	ClientCacheTTL string `yaml:"ClientCacheTTL"`

	// DocCacheSize is the size of the document cache. It works as LRU cache.
	DocCacheSize int `yaml:"DocCacheSize"`

	// ChangeCacheSize is the size of the change cache. It works as LRU cache.
	ChangeCacheSize int `yaml:"ChangeCacheSize"`

	// ActorCacheSize is the size of the actor cache. It works as LRU cache.
	VectorCacheSize int `yaml:"VectorCacheSize"`

	// ProjectCacheSize is the size of the project metadata cache.
	ProjectCacheSize int `yaml:"ProjectCacheSize"`

	// ProjectCacheTTL is the TTL value for the project metadata cache.
	ProjectCacheTTL string `yaml:"ProjectCacheTTL"`
}

// Validate returns an error if the provided Config is invalidated.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.ConnectionTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-connection-timeout" flag: %w`,
			c.ConnectionTimeout,
			err,
		)
	}

	if _, err := time.ParseDuration(c.PingTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-ping-timeout" flag: %w`,
			c.PingTimeout,
			err,
		)
	}

	if c.CacheStatsInterval != "" {
		if _, err := time.ParseDuration(c.CacheStatsInterval); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for cache stats interval: %w`,
				c.CacheStatsInterval,
				err,
			)
		}
	}

	if c.ProjectCacheSize < 0 {
		return fmt.Errorf(
			`invalid argument "%d" for "--mongo-project-cache-size" flag: size must not be negative`,
			c.ProjectCacheSize,
		)
	}

	// An empty value is not an error here, because ParseProjectCacheTTL falls
	// back to DefaultProjectCacheTTL instead of failing.
	if c.ProjectCacheTTL != "" {
		if _, err := cache.ParseTTL(c.ProjectCacheTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--mongo-project-cache-ttl" flag: %w`,
				c.ProjectCacheTTL,
				err,
			)
		}
	}

	// As above: empty falls back to DefaultClientCacheTTL.
	if c.ClientCacheTTL != "" {
		if _, err := cache.ParseTTL(c.ClientCacheTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--mongo-client-cache-ttl" flag: %w`,
				c.ClientCacheTTL,
				err,
			)
		}
	}

	return nil
}

// ParseConnectionTimeout returns connection timeout duration.
func (c *Config) ParseConnectionTimeout() time.Duration {
	result, err := time.ParseDuration(c.ConnectionTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse connection timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParsePingTimeout returns ping timeout duration.
func (c *Config) ParsePingTimeout() time.Duration {
	result, err := time.ParseDuration(c.PingTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse ping timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseCacheStatsInterval returns cache stats interval duration, falling back
// to DefaultCacheStatsInterval when the value is unset. A Config built in code
// may leave it empty, and an unset value must not terminate the process.
func (c *Config) ParseCacheStatsInterval() time.Duration {
	if c.CacheStatsInterval == "" {
		return DefaultCacheStatsInterval
	}

	result, err := time.ParseDuration(c.CacheStatsInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse cache stats interval: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseProjectCacheSize returns the size of the project cache, falling back to
// DefaultProjectCacheSize when the value is unset. A non-positive size is also
// defaulted, because the underlying LRU reads it as "unbounded" rather than as
// the misconfiguration it is; Validate rejects a negative value outright.
func (c *Config) ParseProjectCacheSize() int {
	if c.ProjectCacheSize <= 0 {
		return DefaultProjectCacheSize
	}

	return c.ProjectCacheSize
}

// ParseProjectCacheTTL returns the TTL duration for the project cache, falling
// back to DefaultProjectCacheTTL when the value is unset. Configs built in code
// may leave it empty, and neither an unset nor an invalid value terminates the
// process: an invalid one is returned as an error for the caller to surface.
func (c *Config) ParseProjectCacheTTL() (time.Duration, error) {
	if c.ProjectCacheTTL == "" {
		return DefaultProjectCacheTTL, nil
	}

	result, err := cache.ParseTTL(c.ProjectCacheTTL)
	if err != nil {
		return 0, fmt.Errorf("parse project cache TTL: %w", err)
	}

	return result, nil
}

// ParseClientCacheTTL returns the TTL duration for the client cache, falling
// back to DefaultClientCacheTTL when the value is unset, on the same terms as
// ParseProjectCacheTTL.
func (c *Config) ParseClientCacheTTL() (time.Duration, error) {
	if c.ClientCacheTTL == "" {
		return DefaultClientCacheTTL, nil
	}

	result, err := cache.ParseTTL(c.ClientCacheTTL)
	if err != nil {
		return 0, fmt.Errorf("parse client cache TTL: %w", err)
	}

	return result, nil
}

// ParseMonitoringConfig returns the monitoring configuration for MongoDB query monitoring.
func (c *Config) ParseMonitoringConfig() *MonitorConfig {
	conf := &MonitorConfig{
		Enabled: c.MonitoringEnabled,
	}

	if c.MonitoringSlowQueryThreshold != "" {
		duration, err := time.ParseDuration(c.MonitoringSlowQueryThreshold)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse slow query threshold: %v\n", err)
			os.Exit(1)
		}
		conf.SlowQueryThreshold = duration
	}

	return conf
}
