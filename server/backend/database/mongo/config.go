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

	if c.ProjectCacheTTL != "" {
		if _, err := time.ParseDuration(c.ProjectCacheTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for project cache TTL: %w`,
				c.ProjectCacheTTL,
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

// ParseCacheStatsInterval returns cache stats interval duration.
func (c *Config) ParseCacheStatsInterval() time.Duration {
	result, err := time.ParseDuration(c.CacheStatsInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse cache stats interval: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseProjectCacheTTL returns the TTL duration for the project cache.
func (c *Config) ParseProjectCacheTTL() time.Duration {
	result, err := time.ParseDuration(c.ProjectCacheTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse project cache TTL: %v\n", err)
		os.Exit(1)
	}

	return result
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
