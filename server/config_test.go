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

package server_test

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server"
)

func assertDurationEqual(t *testing.T, expected time.Duration, actual string) {
	actualDuration, err := time.ParseDuration(actual)
	assert.NoError(t, err)
	assert.Equal(t, expected, actualDuration)
}

func assertDefaultConfig(t *testing.T, conf *server.Config) {
	err := conf.Validate()
	assert.NoError(t, err)

	assert.Equal(t, server.DefaultRPCPort, conf.RPC.Port)
	assert.Equal(t, "", conf.RPC.CertFile)
	assert.Equal(t, "", conf.RPC.KeyFile)

	assert.Equal(t, server.DefaultGitHubUserURL, conf.RPC.Auth.GitHubUserURL)
	assert.Equal(t, server.DefaultGitHubDeviceAuthURL, conf.RPC.Auth.GitHubDeviceAuthURL)
	assert.Equal(t, server.DefaultGitHubTokenURL, conf.RPC.Auth.GitHubTokenURL)
	assert.Equal(t, server.DefaultGitHubAuthURL, conf.RPC.Auth.GitHubAuthURL)

	assert.Equal(t, server.DefaultProfilingPort, conf.Profiling.Port)

	assertDurationEqual(t, server.DefaultHousekeepingInterval, conf.Housekeeping.Interval)
	assert.Equal(t, server.DefaultHousekeepingCandidatesLimit, conf.Housekeeping.CandidatesLimit)
	assert.Equal(t, server.DefaultHousekeepingCompactionMinChanges, conf.Housekeeping.CompactionMinChanges)

	assertDurationEqual(t, server.DefaultAdminTokenDuration, conf.Backend.AdminTokenDuration)
	assert.Equal(t, server.DefaultUseDefaultProject, conf.Backend.UseDefaultProject)
	assert.Equal(t, server.DefaultSnapshotDisableGC, conf.Backend.SnapshotDisableGC)
	assert.Equal(t, server.DefaultSnapshotCacheSize, conf.Backend.SnapshotCacheSize)

	assert.Equal(t, server.DefaultAuthWebhookCacheSize, conf.Backend.AuthWebhookCacheSize)
	assertDurationEqual(t, server.DefaultAuthWebhookCacheTTL, conf.Backend.AuthWebhookCacheTTL)

	assert.Equal(t, server.DefaultHostname, conf.Backend.Hostname)
	assert.Equal(t, server.DefaultGatewayAddr, conf.Backend.GatewayAddr)

	assertDurationEqual(t, server.DefaultChannelSessionTTL, conf.Backend.ChannelSessionTTL)
	assertDurationEqual(t, server.DefaultChannelSessionCleanupInterval, conf.Backend.ChannelSessionCleanupInterval)
	assertDurationEqual(t, server.DefaultChannelSessionCountCacheTTL, conf.Backend.ChannelSessionCountCacheTTL)
	assert.Equal(t, server.DefaultChannelSessionCountCacheSize, conf.Backend.ChannelSessionCountCacheSize)

	assertDurationEqual(t, server.DefaultClusterRPCTimeout, conf.Backend.ClusterRPCTimeout)
	assertDurationEqual(t, server.DefaultClusterClientTimeout, conf.Backend.ClusterClientTimeout)
	assert.Equal(t, server.DefaultClusterClientPoolSize, conf.Backend.ClusterClientPoolSize)
	assert.Equal(t, server.DefaultMaxConcurrentClusterRPCs, conf.Backend.MaxConcurrentClusterRPCs)

	if conf.Mongo != nil {
		assertDurationEqual(t, server.DefaultMongoConnectionTimeout, conf.Mongo.ConnectionTimeout)
		assert.Equal(t, server.DefaultMongoConnectionURI, conf.Mongo.ConnectionURI)
		assert.Equal(t, server.DefaultMongoYorkieDatabase, conf.Mongo.YorkieDatabase)
		assertDurationEqual(t, server.DefaultMongoPingTimeout, conf.Mongo.PingTimeout)
	}

	if conf.Kafka != nil {
		assert.Equal(t, server.DefaultKafkaUserEventsTopic, conf.Kafka.UserEventsTopic)
		assert.Equal(t, server.DefaultKafkaChannelEventsTopic, conf.Kafka.ChannelEventsTopic)
		assert.Equal(t, server.DefaultKafkaSessionEventsTopic, conf.Kafka.SessionEventsTopic)
		assertDurationEqual(t, server.DefaultKafkaWriteTimeout, conf.Kafka.WriteTimeout)
	}
}

func TestNewConfigFromFile(t *testing.T) {
	t.Run("fail read config file test", func(t *testing.T) {
		conf := server.NewConfig()
		assert.Equal(t, conf.RPCAddr(), "localhost:"+strconv.Itoa(server.DefaultRPCPort))
		_, err := server.NewConfigFromFile("nowhere.yml")
		assert.Error(t, err)
		assert.Equal(t, conf.RPC.Port, server.DefaultRPCPort)
		assert.Equal(t, conf.RPC.CertFile, "")
		assert.Equal(t, conf.RPC.KeyFile, "")
	})

	t.Run("read empty config file test", func(t *testing.T) {
		filePath := "config.empty.yml"
		file, err := os.Create(filePath)
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, file.Close())
			assert.NoError(t, os.Remove(filePath))
		}()
		conf, err := server.NewConfigFromFile(filePath)
		assert.NoError(t, err)
		assertDefaultConfig(t, conf)
	})

	t.Run("read default config file test", func(t *testing.T) {
		filePath := "config.sample.yml"
		conf, err := server.NewConfigFromFile(filePath)
		assert.NoError(t, err)
		assertDefaultConfig(t, conf)
	})
}
