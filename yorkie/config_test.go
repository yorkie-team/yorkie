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

package yorkie_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/yorkie"
)

func TestNewConfigFromFile(t *testing.T) {
	t.Run("fail read config file test", func(t *testing.T) {
		conf := yorkie.NewConfig()
		assert.Equal(t, conf.RPCAddr(), "localhost:"+strconv.Itoa(yorkie.DefaultRPCPort))
		_, err := yorkie.NewConfigFromFile("nowhere.json")
		assert.Error(t, err)
		assert.Equal(t, conf.RPC.Port, yorkie.DefaultRPCPort)
		assert.Equal(t, conf.RPC.CertFile, "")
		assert.Equal(t, conf.RPC.KeyFile, "")
	
		connTimeout, err := time.ParseDuration(conf.Mongo.ConnectionTimeout)
		assert.NoError(t, err)
		assert.Equal(t, connTimeout, yorkie.DefaultMongoConnectionTimeout)
		assert.Equal(t, conf.Mongo.ConnectionURI, yorkie.DefaultMongoConnectionURI)
		assert.Equal(t, conf.Mongo.YorkieDatabase, yorkie.DefaultMongoYorkieDatabase)
	
		pingTimeout, err := time.ParseDuration(conf.Mongo.PingTimeout)
		assert.NoError(t, err)
		assert.Equal(t, pingTimeout, yorkie.DefaultMongoPingTimeout)
		assert.Equal(t, conf.Backend.SnapshotThreshold, uint64(yorkie.DefaultSnapshotThreshold))
		assert.Equal(t, conf.Backend.SnapshotInterval, uint64(yorkie.DefaultSnapshotInterval))
		assert.Equal(t, conf.Backend.AuthWebhookURL, "")
	})

	t.Run("read config file test", func(t *testing.T) {
		filePath := "config.sample.yml"
		conf, err := yorkie.NewConfigFromFile(filePath)
		assert.NoError(t, err)

		assert.Equal(t, conf.RPC.Port, yorkie.DefaultRPCPort)
		assert.Equal(t, conf.RPC.CertFile, "")
		assert.Equal(t, conf.RPC.KeyFile, "")

		connTimeout, err := time.ParseDuration(conf.Mongo.ConnectionTimeout)
		assert.NoError(t, err)
		assert.Equal(t, connTimeout, yorkie.DefaultMongoConnectionTimeout)
		assert.Equal(t, conf.Mongo.ConnectionURI, yorkie.DefaultMongoConnectionURI)
		assert.Equal(t, conf.Mongo.YorkieDatabase, yorkie.DefaultMongoYorkieDatabase)

		pingTimeout, err := time.ParseDuration(conf.Mongo.PingTimeout)
		assert.NoError(t, err)
		assert.Equal(t, pingTimeout, yorkie.DefaultMongoPingTimeout)
		assert.Equal(t, conf.Backend.SnapshotThreshold, uint64(yorkie.DefaultSnapshotThreshold))
		assert.Equal(t, conf.Backend.SnapshotInterval, uint64(yorkie.DefaultSnapshotInterval))
		assert.Equal(t, conf.Backend.AuthWebhookURL, "")
		assert.Equal(t, conf.Backend.AuthWebhookMaxRetries, uint64(yorkie.DefaultAuthWebhookMaxRetries))

		authWebhookMaxWaitInterval, err := time.ParseDuration(conf.Backend.AuthWebhookMaxWaitInterval)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookMaxWaitInterval, yorkie.DefaultAuthWebhookMaxWaitInterval)

		authWebhookCacheAuthTTL, err := time.ParseDuration(conf.Backend.AuthWebhookCacheAuthTTL)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookCacheAuthTTL, yorkie.DefaultAuthWebhookCacheAuthTTL)

		authWebhookCacheUnauthTTL, err := time.ParseDuration(conf.Backend.AuthWebhookCacheUnauthTTL)
		assert.NoError(t, err)
		assert.Equal(t, authWebhookCacheUnauthTTL, yorkie.DefaultAuthWebhookCacheUnauthTTL)
	})
}
