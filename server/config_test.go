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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server"
)

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

	t.Run("read config file test", func(t *testing.T) {
		filePath := "config.sample.yml"
		conf, err := server.NewConfigFromFile(filePath)
		assert.NoError(t, err)

		assert.Equal(t, conf.RPC.Port, server.DefaultRPCPort)
		assert.Equal(t, conf.RPC.CertFile, "")
		assert.Equal(t, conf.RPC.KeyFile, "")

		connTimeout, err := time.ParseDuration(conf.Mongo.ConnectionTimeout)
		assert.NoError(t, err)
		assert.Equal(t, connTimeout, server.DefaultMongoConnectionTimeout)
		assert.Equal(t, conf.Mongo.ConnectionURI, server.DefaultMongoConnectionURI)
		assert.Equal(t, conf.Mongo.YorkieDatabase, server.DefaultMongoYorkieDatabase)

		pingTimeout, err := time.ParseDuration(conf.Mongo.PingTimeout)
		assert.NoError(t, err)
		assert.Equal(t, pingTimeout, server.DefaultMongoPingTimeout)
		assert.Equal(t, conf.Backend.SnapshotThreshold, int64(server.DefaultSnapshotThreshold))
		assert.Equal(t, conf.Backend.SnapshotInterval, int64(server.DefaultSnapshotInterval))
		assert.Equal(t, conf.Backend.AuthWebhookMaxRetries, uint64(server.DefaultAuthWebhookMaxRetries))

		ClientDeactivateThreshold := conf.Backend.ClientDeactivateThreshold
		assert.NoError(t, err)
		assert.Equal(t, ClientDeactivateThreshold, server.DefaultClientDeactivateThreshold)

		authWebhookMaxWaitInterval, err := time.ParseDuration(conf.Backend.AuthWebhookMaxWaitInterval)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookMaxWaitInterval, server.DefaultAuthWebhookMaxWaitInterval)

		authWebhookCacheAuthTTL, err := time.ParseDuration(conf.Backend.AuthWebhookCacheAuthTTL)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookCacheAuthTTL, server.DefaultAuthWebhookCacheAuthTTL)

		authWebhookCacheUnauthTTL, err := time.ParseDuration(conf.Backend.AuthWebhookCacheUnauthTTL)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookCacheUnauthTTL, server.DefaultAuthWebhookCacheUnauthTTL)

		projectInfoCacheTTL, err := time.ParseDuration(conf.Backend.ProjectInfoCacheTTL)
		assert.NoError(t, err)
		assert.Equal(t, projectInfoCacheTTL, server.DefaultProjectInfoCacheTTL)
	})
}
