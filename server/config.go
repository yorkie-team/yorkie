/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

// Below are the values of the default values of Yorkie config.
const (
	DefaultRPCPort                  = 11101
	DefaultRPCMaxRequestsBytes      = 4 * 1024 * 1024 // 4MiB
	DefaultRPCMaxConnectionAge      = 0 * time.Second
	DefaultRPCMaxConnectionAgeGrace = 0 * time.Second

	DefaultProfilingPort = 11102

	DefaultHousekeepingInterval                  = 30 * time.Second
	DefaultHousekeepingCandidatesLimitPerProject = 500
	DefaultHousekeepingProjectFetchSize          = 100

	DefaultMongoConnectionURI     = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeout = 5 * time.Second
	DefaultMongoPingTimeout       = 5 * time.Second
	DefaultMongoYorkieDatabase    = "yorkie-meta"

	DefaultAdminUser                  = "admin"
	DefaultAdminPassword              = "admin"
	DefaultSecretKey                  = "yorkie-secret"
	DefaultAdminTokenDuration         = 7 * 24 * time.Hour
	DefaultUseDefaultProject          = true
	DefaultClientDeactivateThreshold  = "24h"
	DefaultSnapshotThreshold          = 500
	DefaultSnapshotInterval           = 1000
	DefaultSnapshotWithPurgingChanges = false

	DefaultAuthWebhookMaxRetries      = 10
	DefaultAuthWebhookMaxWaitInterval = 3000 * time.Millisecond
	DefaultAuthWebhookCacheSize       = 5000
	DefaultAuthWebhookCacheAuthTTL    = 10 * time.Second
	DefaultAuthWebhookCacheUnauthTTL  = 10 * time.Second
	DefaultProjectInfoCacheSize       = 256
	DefaultProjectInfoCacheTTL        = 10 * time.Minute

	DefaultHostname = ""
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC          *rpc.Config          `yaml:"RPC"`
	Profiling    *profiling.Config    `yaml:"Profiling"`
	Housekeeping *housekeeping.Config `yaml:"Housekeeping"`
	Backend      *backend.Config      `yaml:"Backend"`
	Mongo        *mongo.Config        `yaml:"Mongo"`
}

// NewConfig returns a Config struct that contains reasonable defaults
// for most of the configurations.
func NewConfig() *Config {
	return newConfig(DefaultRPCPort, DefaultProfilingPort)
}

// NewConfigFromFile returns a Config struct for the given conf file.
func NewConfigFromFile(path string) (*Config, error) {
	conf := &Config{}
	bytes, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err = yaml.Unmarshal(bytes, conf); err != nil {
		return nil, fmt.Errorf("unmarshal config file: %w", err)
	}

	conf.ensureDefaultValue()
	return conf, nil
}

// RPCAddr returns the RPC address.
func (c *Config) RPCAddr() string {
	return fmt.Sprintf("localhost:%d", c.RPC.Port)
}

// Validate returns an error if the provided Config is invalidated.
func (c *Config) Validate() error {
	if err := c.RPC.Validate(); err != nil {
		return err
	}

	if err := c.Profiling.Validate(); err != nil {
		return err
	}

	if err := c.Housekeeping.Validate(); err != nil {
		return err
	}

	if err := c.Backend.Validate(); err != nil {
		return err
	}

	if c.Mongo != nil {
		if err := c.Mongo.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// ensureDefaultValue sets the value of the option to which the default value
// should be applied when the user does not input it.
func (c *Config) ensureDefaultValue() {
	if c.RPC.Port == 0 {
		c.RPC.Port = DefaultRPCPort
	}

	if c.RPC.MaxRequestBytes == 0 {
		c.RPC.MaxRequestBytes = DefaultRPCMaxRequestsBytes
	}

	if c.RPC.MaxConnectionAge == "" {
		c.RPC.MaxConnectionAge = DefaultRPCMaxConnectionAge.String()
	}

	if c.RPC.MaxConnectionAgeGrace == "" {
		c.RPC.MaxConnectionAgeGrace = DefaultRPCMaxConnectionAgeGrace.String()
	}

	if c.Profiling.Port == 0 {
		c.Profiling.Port = DefaultProfilingPort
	}

	if c.Backend.AdminUser == "" {
		c.Backend.AdminUser = DefaultAdminUser
	}

	if c.Backend.AdminPassword == "" {
		c.Backend.AdminPassword = DefaultAdminPassword
	}

	if c.Backend.SecretKey == "" {
		c.Backend.SecretKey = DefaultSecretKey
	}

	if c.Backend.AdminTokenDuration == "" {
		c.Backend.AdminTokenDuration = DefaultAdminTokenDuration.String()
	}

	if c.Backend.ClientDeactivateThreshold == "" {
		c.Backend.ClientDeactivateThreshold = DefaultClientDeactivateThreshold
	}

	if c.Backend.SnapshotThreshold == 0 {
		c.Backend.SnapshotThreshold = DefaultSnapshotThreshold
	}

	if c.Backend.SnapshotInterval == 0 {
		c.Backend.SnapshotInterval = DefaultSnapshotInterval
	}

	if c.Backend.AuthWebhookCacheSize == 0 {
		c.Backend.AuthWebhookCacheSize = DefaultAuthWebhookCacheSize
	}

	if c.Backend.AuthWebhookMaxRetries == 0 {
		c.Backend.AuthWebhookMaxRetries = DefaultAuthWebhookMaxRetries
	}

	if c.Backend.AuthWebhookMaxWaitInterval == "" {
		c.Backend.AuthWebhookMaxWaitInterval = DefaultAuthWebhookMaxWaitInterval.String()
	}

	if c.Backend.AuthWebhookCacheAuthTTL == "" {
		c.Backend.AuthWebhookCacheAuthTTL = DefaultAuthWebhookCacheAuthTTL.String()
	}

	if c.Backend.AuthWebhookCacheUnauthTTL == "" {
		c.Backend.AuthWebhookCacheUnauthTTL = DefaultAuthWebhookCacheUnauthTTL.String()
	}

	if c.Backend.ProjectInfoCacheSize == 0 {
		c.Backend.ProjectInfoCacheSize = DefaultProjectInfoCacheSize
	}

	if c.Backend.ProjectInfoCacheTTL == "" {
		c.Backend.ProjectInfoCacheTTL = DefaultProjectInfoCacheTTL.String()
	}

	if c.Mongo != nil {
		if c.Mongo.ConnectionURI == "" {
			c.Mongo.ConnectionURI = DefaultMongoConnectionURI
		}

		if c.Mongo.ConnectionTimeout == "" {
			c.Mongo.ConnectionTimeout = DefaultMongoConnectionTimeout.String()
		}

		if c.Mongo.YorkieDatabase == "" {
			c.Mongo.YorkieDatabase = DefaultMongoYorkieDatabase
		}

		if c.Mongo.PingTimeout == "" {
			c.Mongo.PingTimeout = DefaultMongoPingTimeout.String()
		}
	}
}

func newConfig(port int, profilingPort int) *Config {
	return &Config{
		RPC: &rpc.Config{
			Port: port,
		},
		Profiling: &profiling.Config{
			Port: profilingPort,
		},
		Housekeeping: &housekeeping.Config{
			Interval:                  DefaultHousekeepingInterval.String(),
			CandidatesLimitPerProject: DefaultHousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          DefaultHousekeepingProjectFetchSize,
		},
		Backend: &backend.Config{
			ClientDeactivateThreshold:  DefaultClientDeactivateThreshold,
			SnapshotThreshold:          DefaultSnapshotThreshold,
			SnapshotInterval:           DefaultSnapshotInterval,
			SnapshotWithPurgingChanges: DefaultSnapshotWithPurgingChanges,
		},
	}
}
