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

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		// 1. success
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
			ProjectCacheTTL:   "10m",
		}
		assert.NoError(t, config.Validate())

		// 2. invalid connection timeout
		config.ConnectionTimeout = "5"
		assert.Error(t, config.Validate())

		// 3. invalid ping timeout
		config.ConnectionTimeout = "5s"
		config.PingTimeout = "5"
		assert.Error(t, config.Validate())
	})

	// ParseProjectCacheTTL exits the process on an unparseable value, and a
	// non-positive one is reinterpreted as "never expire" by the underlying
	// expirable LRU. Both have to be caught here, before startup.
	t.Run("reject unusable project cache TTL", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}

		// An omitted TTL is filled by server.ensureMongoDefaultValue; if it
		// still reaches Validate, it is an error rather than a silent default.
		assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-ttl")

		config.ProjectCacheTTL = "0s"
		assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-ttl")

		config.ProjectCacheTTL = "1ns"
		assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-ttl")

		config.ProjectCacheTTL = "1ms"
		assert.NoError(t, config.Validate())
	})

	t.Run("parse monitoring test", func(t *testing.T) {
		config := &mongo.Config{
			MonitoringEnabled:            true,
			MonitoringSlowQueryThreshold: "100ms",
		}
		monitorConfig := config.ParseMonitoringConfig()
		assert.Equal(t, true, monitorConfig.Enabled)
		assert.Equal(t, "100ms", monitorConfig.SlowQueryThreshold.String())
	})
}
