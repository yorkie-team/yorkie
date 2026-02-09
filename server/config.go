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
	"github.com/yorkie-team/yorkie/server/backend/membership"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/backend/warehouse"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

// Below are the values of the default values of Yorkie config.
const (
	DefaultRPCPort       = 8080
	DefaultProfilingPort = 8081

	DefaultMembershipLeaseDuration   = 15 * time.Second
	DefaultMembershipRenewalInterval = 5 * time.Second

	DefaultHousekeepingInterval             = 30 * time.Second
	DefaultHousekeepingCandidatesLimit      = 500
	DefaultHousekeepingCompactionMinChanges = 1000

	DefaultMongoConnectionURI                = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeout            = 5 * time.Second
	DefaultMongoPingTimeout                  = 5 * time.Second
	DefaultMongoYorkieDatabase               = "yorkie-meta"
	DefaultMongoMonitoringSlowQueryThreshold = 100 * time.Millisecond
	DefaultMongoCacheStatsInterval           = 30 * time.Second
	DefaultMongoClientCacheSize              = 10000
	DefaultMongoDocCacheSize                 = 10000
	DefaultMongoChangeCacheSize              = 10000
	DefaultMongoVectorCacheSize              = 10000

	DefaultKafkaUserEventsTopic     = "user-events"
	DefaultKafkaDocumentEventsTopic = "document-events"
	DefaultKafkaClientEventsTopic   = "client-events"
	DefaultKafkaChannelEventsTopic  = "channel-events"
	DefaultKafkaSessionEventsTopic  = "session-events"
	DefaultKafkaWriteTimeout        = 5 * time.Second

	DefaultChannelSessionTTL             = 60 * time.Second
	DefaultChannelSessionCleanupInterval = 10 * time.Second
	DefaultChannelSessionCountCacheTTL   = 30 * time.Second
	DefaultChannelSessionCountCacheSize  = 10000

	DefaultClusterRPCTimeout        = 10 * time.Second
	DefaultClusterClientTimeout     = 30 * time.Second
	DefaultMaxConcurrentClusterRPCs = 5000

	DefaultAdminUser     = "admin"
	DefaultAdminPassword = "admin"
	DefaultSecretKey     = "yorkie-secret"

	DefaultAdminTokenDuration  = 7 * 24 * time.Hour
	DefaultGitHubUserURL       = "https://api.github.com/user"
	DefaultGitHubAuthURL       = "https://github.com/login/oauth/authorize"
	DefaultGitHubTokenURL      = "https://github.com/login/oauth/access_token" // #nosec G101
	DefaultGitHubDeviceAuthURL = "https://github.com/login/device/code"

	DefaultUseDefaultProject = true
	DefaultSnapshotDisableGC = false
	DefaultSnapshotCacheSize = 1000

	DefaultAuthWebhookCacheSize = 5000
	DefaultAuthWebhookCacheTTL  = 10 * time.Second

	DefaultProjectCacheSize = 256
	DefaultProjectCacheTTL  = 10 * time.Minute

	DefaultHostname       = ""
	DefaultGatewayAddr    = "localhost:8080"
	DefaultBackendRPCAddr = "localhost:8080"
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC          *rpc.Config          `yaml:"RPC"`
	Profiling    *profiling.Config    `yaml:"Profiling"`
	Membership   *membership.Config   `yaml:"Membership"`
	Housekeeping *housekeeping.Config `yaml:"Housekeeping"`
	Backend      *backend.Config      `yaml:"Backend"`
	Mongo        *mongo.Config        `yaml:"Mongo"`
	Kafka        *messaging.Config    `yaml:"Kafka"`
	StarRocks    *warehouse.Config    `yaml:"StarRocks"`
}

// NewConfig returns a Config struct that contains reasonable defaults
// for most of the configurations.
func NewConfig() *Config {
	return newConfig(DefaultRPCPort, DefaultProfilingPort)
}

// NewConfigFromFile returns a Config struct for the given conf file.
func NewConfigFromFile(path string) (*Config, error) {
	// NOTE(hackerwins): Start from a config populated with default values so
	// that fields like boolean UseDefaultProject (which can't distinguish
	// zero-value false from "unset") get the intended default when omitted in
	// YAML. The unmarshal will override only the fields provided by the user.
	conf := newConfig(DefaultRPCPort, DefaultProfilingPort)
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

// ensureRPCDefaultValue set the default rpc.Config value
func (c *Config) ensureRPCDefaultValue() {
	if c.RPC == nil {
		c.RPC = &rpc.Config{}
	}
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
}

// ensureProfilingDefaultValue set the default profiling.Config value
func (c *Config) ensureProfilingDefaultValue() {
	if c.Profiling == nil {
		c.Profiling = &profiling.Config{}
	}
	if c.Profiling.Port == 0 {
		c.Profiling.Port = DefaultProfilingPort
	}
}

// ensureMembershipDefaultValue set the default membership.Config value (keeps YAML key Membership)
func (c *Config) ensureMembershipDefaultValue() {
	if c.Membership == nil {
		c.Membership = &membership.Config{}
	}

	if c.Membership.LeaseDuration == "" {
		c.Membership.LeaseDuration = DefaultMembershipLeaseDuration.String()
	}

	if c.Membership.RenewalInterval == "" {
		c.Membership.RenewalInterval = DefaultMembershipRenewalInterval.String()
	}
}

// ensureHouseKeepingDefaultValue set the default housekeeping.Config value
func (c *Config) ensureHouseKeepingDefaultValue() {
	if c.Housekeeping == nil {
		c.Housekeeping = &housekeeping.Config{}
	}
	if c.Housekeeping.Interval == "" {
		c.Housekeeping.Interval = DefaultHousekeepingInterval.String()
	}
	if c.Housekeeping.CandidatesLimit == 0 {
		c.Housekeeping.CandidatesLimit = DefaultHousekeepingCandidatesLimit
	}
	if c.Housekeeping.CompactionMinChanges == 0 {
		c.Housekeeping.CompactionMinChanges = DefaultHousekeepingCompactionMinChanges
	}
}

// ensureBackendDefaultValue set the default backend.Config value
func (c *Config) ensureBackendDefaultValue() {
	if c.Backend == nil {
		c.Backend = &backend.Config{}
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

	if c.Backend.SnapshotCacheSize == 0 {
		c.Backend.SnapshotCacheSize = DefaultSnapshotCacheSize
	}

	if c.Backend.AuthWebhookCacheSize == 0 {
		c.Backend.AuthWebhookCacheSize = DefaultAuthWebhookCacheSize
	}
	if c.Backend.AuthWebhookCacheTTL == "" {
		c.Backend.AuthWebhookCacheTTL = DefaultAuthWebhookCacheTTL.String()
	}

	if c.Backend.GatewayAddr == "" {
		c.Backend.GatewayAddr = DefaultGatewayAddr
	}
	if c.Backend.RPCAddr == "" {
		c.Backend.RPCAddr = DefaultBackendRPCAddr
	}
	if c.Backend.ChannelSessionTTL == "" {
		c.Backend.ChannelSessionTTL = DefaultChannelSessionTTL.String()
	}
	if c.Backend.ChannelSessionCleanupInterval == "" {
		c.Backend.ChannelSessionCleanupInterval = DefaultChannelSessionCleanupInterval.String()
	}
	if c.Backend.ChannelSessionCountCacheTTL == "" {
		c.Backend.ChannelSessionCountCacheTTL = DefaultChannelSessionCountCacheTTL.String()
	}
	if c.Backend.ChannelSessionCountCacheSize == 0 {
		c.Backend.ChannelSessionCountCacheSize = DefaultChannelSessionCountCacheSize
	}
	if c.Backend.ClusterRPCTimeout == "" {
		c.Backend.ClusterRPCTimeout = DefaultClusterRPCTimeout.String()
	}
	if c.Backend.ClusterClientTimeout == "" {
		c.Backend.ClusterClientTimeout = DefaultClusterClientTimeout.String()
	}
	if c.Backend.MaxConcurrentClusterRPCs == 0 {
		c.Backend.MaxConcurrentClusterRPCs = DefaultMaxConcurrentClusterRPCs
	}
}

// ensureMongoDefaultValue set the default mongo.Config value
func (c *Config) ensureMongoDefaultValue() {
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
	if c.Mongo.CacheStatsInterval == "" {
		c.Mongo.CacheStatsInterval = DefaultMongoCacheStatsInterval.String()
	}
	if c.Mongo.ClientCacheSize == 0 {
		c.Mongo.ClientCacheSize = DefaultMongoClientCacheSize
	}
	if c.Mongo.DocCacheSize == 0 {
		c.Mongo.DocCacheSize = DefaultMongoDocCacheSize
	}
	if c.Mongo.ChangeCacheSize == 0 {
		c.Mongo.ChangeCacheSize = DefaultMongoChangeCacheSize
	}
	if c.Mongo.VectorCacheSize == 0 {
		c.Mongo.VectorCacheSize = DefaultMongoVectorCacheSize
	}
}

// ensureKafkaDefaultValue set the default messagebroker.Config value
func (c *Config) ensureKafkaDefaultValue() {
	if c.Kafka == nil {
		c.Kafka = &messaging.Config{}
	}
	if c.Kafka.UserEventsTopic == "" {
		c.Kafka.UserEventsTopic = DefaultKafkaUserEventsTopic
	}
	if c.Kafka.DocumentEventsTopic == "" {
		c.Kafka.DocumentEventsTopic = DefaultKafkaDocumentEventsTopic
	}
	if c.Kafka.ChannelEventsTopic == "" {
		c.Kafka.ChannelEventsTopic = DefaultKafkaChannelEventsTopic
	}
	if c.Kafka.SessionEventsTopic == "" {
		c.Kafka.SessionEventsTopic = DefaultKafkaSessionEventsTopic
	}
	if c.Kafka.WriteTimeout == "" {
		c.Kafka.WriteTimeout = DefaultKafkaWriteTimeout.String()
	}
}

// ensureDefaultValue sets the value of the option to which the default value
// should be applied when the user does not input it.
func (c *Config) ensureDefaultValue() {
	c.ensureRPCDefaultValue()
	c.ensureProfilingDefaultValue()
	c.ensureMembershipDefaultValue()
	c.ensureHouseKeepingDefaultValue()
	c.ensureBackendDefaultValue()

	if c.Mongo != nil {
		c.ensureMongoDefaultValue()
	}
	if c.Kafka != nil && c.Kafka.Addresses != "" {
		c.ensureKafkaDefaultValue()
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
		Membership: &membership.Config{
			LeaseDuration:   DefaultMembershipLeaseDuration.String(),
			RenewalInterval: DefaultMembershipRenewalInterval.String(),
		},
		Housekeeping: &housekeeping.Config{
			Interval:             DefaultHousekeepingInterval.String(),
			CandidatesLimit:      DefaultHousekeepingCandidatesLimit,
			CompactionMinChanges: DefaultHousekeepingCompactionMinChanges,
		},
		Backend: &backend.Config{
			UseDefaultProject: DefaultUseDefaultProject,
		},
	}
}
