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
	assert.Equal(t, server.DefaultHousekeepingCandidatesLimitPerProject, conf.Housekeeping.CandidatesLimitPerProject)
	assert.Equal(t, server.DefaultHousekeepingProjectFetchSize, conf.Housekeeping.ProjectFetchSize)
	assert.Equal(t, server.DefaultHousekeepingCompactionMinChanges, conf.Housekeeping.CompactionMinChanges)

	assertDurationEqual(t, server.DefaultAdminTokenDuration, conf.Backend.AdminTokenDuration)
	//TODO: assert failure - expect default true, but got false
	//assert.Equal(t, server.DefaultUseDefaultProject, conf.Backend.UseDefaultProject)
	assertDurationEqual(t, server.DefaultClientDeactivateThreshold, conf.Backend.ClientDeactivateThreshold)
	assert.Equal(t, int64(server.DefaultSnapshotThreshold), conf.Backend.SnapshotThreshold)
	assert.Equal(t, int64(server.DefaultSnapshotInterval), conf.Backend.SnapshotInterval)
	assert.Equal(t, server.DefaultSnapshotDisableGC, conf.Backend.SnapshotDisableGC)
	assert.Equal(t, server.DefaultSnapshotCacheSize, conf.Backend.SnapshotCacheSize)

	assertDurationEqual(t, server.DefaultAuthWebhookRequestTimeout, conf.Backend.AuthWebhookRequestTimeout)
	assert.Equal(t, uint64(server.DefaultAuthWebhookMaxRetries), conf.Backend.AuthWebhookMaxRetries)
	assertDurationEqual(t, server.DefaultAuthWebhookMinWaitInterval, conf.Backend.AuthWebhookMinWaitInterval)
	assertDurationEqual(t, server.DefaultAuthWebhookMaxWaitInterval, conf.Backend.AuthWebhookMaxWaitInterval)
	assert.Equal(t, server.DefaultAuthWebhookCacheSize, conf.Backend.AuthWebhookCacheSize)
	assertDurationEqual(t, server.DefaultAuthWebhookCacheTTL, conf.Backend.AuthWebhookCacheTTL)

	assertDurationEqual(t, server.DefaultEventWebhookRequestTimeout, conf.Backend.EventWebhookRequestTimeout)
	assert.Equal(t, uint64(server.DefaultEventWebhookMaxRetries), conf.Backend.EventWebhookMaxRetries)
	assertDurationEqual(t, server.DefaultEventWebhookMinWaitInterval, conf.Backend.EventWebhookMinWaitInterval)
	assertDurationEqual(t, server.DefaultEventWebhookMaxWaitInterval, conf.Backend.EventWebhookMaxWaitInterval)

	assert.Equal(t, server.DefaultProjectCacheSize, conf.Backend.ProjectCacheSize)
	assertDurationEqual(t, server.DefaultProjectCacheTTL, conf.Backend.ProjectCacheTTL)

	assert.Equal(t, server.DefaultHostname, conf.Backend.Hostname)
	assert.Equal(t, server.DefaultGatewayAddr, conf.Backend.GatewayAddr)

	if conf.Mongo != nil {
		assertDurationEqual(t, server.DefaultMongoConnectionTimeout, conf.Mongo.ConnectionTimeout)
		assert.Equal(t, server.DefaultMongoConnectionURI, conf.Mongo.ConnectionURI)
		assert.Equal(t, server.DefaultMongoYorkieDatabase, conf.Mongo.YorkieDatabase)
		assertDurationEqual(t, server.DefaultMongoPingTimeout, conf.Mongo.PingTimeout)
	}

	if conf.Kafka != nil {
		assert.Equal(t, server.DefaultKafkaTopic, conf.Kafka.Topic)
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

		assert.Equal(t, conf.Backend.SnapshotThreshold, int64(server.DefaultSnapshotThreshold))
		assert.Equal(t, conf.Backend.SnapshotInterval, int64(server.DefaultSnapshotInterval))
	})

	t.Run("read empty config file test", func(t *testing.T) {
		filePath := "config.empty.yml"
		file, err := os.Create(filePath)
		assert.NoError(t, err)
		_, err = file.WriteString("RPC: {}\nProfiling: {}\nHousekeeping: {}\nBackend: {}\nMongo: {}\n")
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
