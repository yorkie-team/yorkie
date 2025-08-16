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

	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
	"github.com/yorkie-team/yorkie/server/backend/warehouse"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

// Below are the values of the default values of Yorkie config.
const (
	DefaultRPCPort       = 8080
	DefaultProfilingPort = 8081

	DefaultHousekeepingInterval                  = 30 * time.Second
	DefaultHousekeepingCandidatesLimitPerProject = 500
	DefaultHousekeepingProjectFetchSize          = 100
	DefaultHousekeepingCompactionMinChanges      = 1000

	DefaultMongoConnectionURI                = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeout            = 5 * time.Second
	DefaultMongoPingTimeout                  = 5 * time.Second
	DefaultMongoYorkieDatabase               = "yorkie-meta"
	DefaultMongoMonitoringSlowQueryThreshold = 100 * time.Millisecond

	DefaultKafkaTopic        = "user-events"
	DefaultKafkaWriteTimeout = 5 * time.Second

	DefaultAdminUser     = "admin"
	DefaultAdminPassword = "admin"
	DefaultSecretKey     = "yorkie-secret"

	DefaultAdminTokenDuration  = 7 * 24 * time.Hour
	DefaultGitHubUserURL       = "https://api.github.com/user"
	DefaultGitHubAuthURL       = "https://github.com/login/oauth/authorize"
	DefaultGitHubTokenURL      = "https://github.com/login/oauth/access_token" // #nosec G101
	DefaultGitHubDeviceAuthURL = "https://github.com/login/device/code"

	DefaultUseDefaultProject         = true
	DefaultClientDeactivateThreshold = 24 * time.Hour
	DefaultSnapshotThreshold         = 500
	DefaultSnapshotInterval          = 500
	DefaultSnapshotDisableGC         = false
	DefaultSnapshotCacheSize         = 1000

	DefaultAuthWebhookRequestTimeout  = 3 * time.Second
	DefaultAuthWebhookMaxRetries      = 10
	DefaultAuthWebhookMaxWaitInterval = 3 * time.Second
	DefaultAuthWebhookMinWaitInterval = 100 * time.Millisecond
	DefaultAuthWebhookCacheSize       = 5000
	DefaultAuthWebhookCacheTTL        = 10 * time.Second

	DefaultEventWebhookRequestTimeout  = 3 * time.Second
	DefaultEventWebhookMaxRetries      = 10
	DefaultEventWebhookMaxWaitInterval = 3 * time.Second
	DefaultEventWebhookMinWaitInterval = 100 * time.Millisecond

	DefaultProjectCacheSize = 256
	DefaultProjectCacheTTL  = 10 * time.Minute

	DefaultHostname    = ""
	DefaultGatewayAddr = "localhost:8080"
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC          *rpc.Config           `yaml:"RPC"`
	Profiling    *profiling.Config     `yaml:"Profiling"`
	Housekeeping *housekeeping.Config  `yaml:"Housekeeping"`
	Backend      *backend.Config       `yaml:"Backend"`
	Mongo        *mongo.Config         `yaml:"Mongo"`
	Kafka        *messagebroker.Config `yaml:"Kafka"`
	StarRocks    *warehouse.Config     `yaml:"StarRocks"`
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

	if c.Kafka != nil {
		if err := c.Kafka.Validate(); err != nil {
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
	if c.RPC.Auth.GitHubAuthURL == "" {
		c.RPC.Auth.GitHubAuthURL = DefaultGitHubAuthURL
	}
	if c.RPC.Auth.GitHubTokenURL == "" {
		c.RPC.Auth.GitHubTokenURL = DefaultGitHubTokenURL
	}
	if c.RPC.Auth.GitHubDeviceAuthURL == "" {
		c.RPC.Auth.GitHubDeviceAuthURL = DefaultGitHubDeviceAuthURL
	}
	if c.RPC.Auth.GitHubUserURL == "" {
		c.RPC.Auth.GitHubUserURL = DefaultGitHubUserURL
	}

	if c.Profiling.Port == 0 {
		c.Profiling.Port = DefaultProfilingPort
	}

	if c.Housekeeping.Interval == "" {
		c.Housekeeping.Interval = DefaultHousekeepingInterval.String()
	}
	if c.Housekeeping.CandidatesLimitPerProject == 0 {
		c.Housekeeping.CandidatesLimitPerProject = DefaultHousekeepingCandidatesLimitPerProject
	}
	if c.Housekeeping.ProjectFetchSize == 0 {
		c.Housekeeping.ProjectFetchSize = DefaultHousekeepingProjectFetchSize
	}
	if c.Housekeeping.CompactionMinChanges == 0 {
		c.Housekeeping.CompactionMinChanges = DefaultHousekeepingCompactionMinChanges
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
		c.Backend.ClientDeactivateThreshold = DefaultClientDeactivateThreshold.String()
	}

	if c.Backend.SnapshotThreshold == 0 {
		c.Backend.SnapshotThreshold = DefaultSnapshotThreshold
	}
	if c.Backend.SnapshotInterval == 0 {
		c.Backend.SnapshotInterval = DefaultSnapshotInterval
	}
	if c.Backend.SnapshotCacheSize == 0 {
		c.Backend.SnapshotCacheSize = DefaultSnapshotCacheSize
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
	if c.Backend.AuthWebhookMinWaitInterval == "" {
		c.Backend.AuthWebhookMinWaitInterval = DefaultAuthWebhookMinWaitInterval.String()
	}
	if c.Backend.AuthWebhookRequestTimeout == "" {
		c.Backend.AuthWebhookRequestTimeout = DefaultAuthWebhookRequestTimeout.String()
	}
	if c.Backend.AuthWebhookCacheTTL == "" {
		c.Backend.AuthWebhookCacheTTL = DefaultAuthWebhookCacheTTL.String()
	}

	if c.Backend.EventWebhookMaxRetries == 0 {
		c.Backend.EventWebhookMaxRetries = DefaultEventWebhookMaxRetries
	}
	if c.Backend.EventWebhookMaxWaitInterval == "" {
		c.Backend.EventWebhookMaxWaitInterval = DefaultEventWebhookMaxWaitInterval.String()
	}
	if c.Backend.EventWebhookMinWaitInterval == "" {
		c.Backend.EventWebhookMinWaitInterval = DefaultEventWebhookMinWaitInterval.String()
	}
	if c.Backend.EventWebhookRequestTimeout == "" {
		c.Backend.EventWebhookRequestTimeout = DefaultEventWebhookRequestTimeout.String()
	}

	if c.Backend.ProjectCacheSize == 0 {
		c.Backend.ProjectCacheSize = DefaultProjectCacheSize
	}
	if c.Backend.ProjectCacheTTL == "" {
		c.Backend.ProjectCacheTTL = DefaultProjectCacheTTL.String()
	}
	if c.Backend.GatewayAddr == "" {
		c.Backend.GatewayAddr = DefaultGatewayAddr
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

		if c.Mongo.MonitoringEnabled {
			if c.Mongo.MonitoringSlowQueryThreshold == "" {
				c.Mongo.MonitoringSlowQueryThreshold = DefaultMongoMonitoringSlowQueryThreshold.String()
			}
		}
	}
	if c.Kafka != nil && c.Kafka.Addresses != "" {
		if c.Kafka.Topic == "" {
			c.Kafka.Topic = DefaultKafkaTopic
		}
		if c.Kafka.WriteTimeout == "" {
			c.Kafka.WriteTimeout = DefaultKafkaWriteTimeout.String()
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
			CompactionMinChanges:      DefaultHousekeepingCompactionMinChanges,
		},
		Backend: &backend.Config{
			ClientDeactivateThreshold: DefaultClientDeactivateThreshold.String(),
			SnapshotThreshold:         DefaultSnapshotThreshold,
			SnapshotInterval:          DefaultSnapshotInterval,
		},
	}
}
