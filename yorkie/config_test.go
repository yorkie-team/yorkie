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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/yorkie"
)

func TestNewConfigFromFile(t *testing.T) {
	_, err := yorkie.NewConfigFromFile("nowhere.json")
	assert.Error(t, err)

	filePath := "config.sample.json"
	conf, err := yorkie.NewConfigFromFile(filePath)
	assert.NoError(t, err)

	assert.Equal(t, conf.RPC.Port, yorkie.DefaultRPCPort)
	assert.Equal(t, conf.RPC.CertFile, "")
	assert.Equal(t, conf.RPC.KeyFile, "")
	assert.Equal(t, conf.Mongo.ConnectionTimeoutSec, time.Duration(yorkie.DefaultMongoConnectionTimeoutSec))
	assert.Equal(t, conf.Mongo.ConnectionURI, yorkie.DefaultMongoConnectionURI)
	assert.Equal(t, conf.Mongo.YorkieDatabase, yorkie.DefaultMongoYorkieDatabase)
	assert.Equal(t, conf.Mongo.PingTimeoutSec, time.Duration(yorkie.DefaultMongoPingTimeoutSec))
	assert.Equal(t, conf.Backend.SnapshotThreshold, uint64(yorkie.DefaultSnapshotThreshold))
}
