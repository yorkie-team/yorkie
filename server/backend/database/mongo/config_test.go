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

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		// 1. success
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
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

	t.Run("reject project cache TTL below cache.MinTTL", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}
		for _, ttl := range []string{"0s", "-1s", "1ns", "1ms", "99ms"} {
			config.ProjectCacheTTL = ttl
			assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-ttl", ttl)
		}

		config.ProjectCacheTTL = "100ms"
		assert.NoError(t, config.Validate())
	})

	t.Run("reject negative project cache size", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
			ProjectCacheSize:  -1,
		}
		assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-size")

		// A non-positive size must never reach the LRU, which reads it as
		// "unbounded" instead of rejecting it.
		assert.Equal(t, mongo.DefaultProjectCacheSize, config.ParseProjectCacheSize())
	})

	t.Run("default caches for a config built in code", func(t *testing.T) {
		// A Config built in code may leave the cache fields empty, and an unset
		// value must resolve to the default instead of exiting.
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}
		assert.NoError(t, config.Validate())

		ttl, err := config.ParseProjectCacheTTL()
		assert.NoError(t, err)
		assert.Equal(t, mongo.DefaultProjectCacheTTL, ttl)
		assert.Equal(t, mongo.DefaultProjectCacheSize, config.ParseProjectCacheSize())
		assert.Equal(t, mongo.DefaultCacheStatsInterval, config.ParseCacheStatsInterval())
	})

	t.Run("report an invalid project cache TTL without exiting", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
			ProjectCacheTTL:   "1ms",
		}
		_, err := config.ParseProjectCacheTTL()
		assert.ErrorIs(t, err, cache.ErrInvalidTTL)
	})

	t.Run("client cache TTL test", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}

		// Unset falls back to the default instead of "never expire".
		ttl, err := config.ParseClientCacheTTL()
		assert.NoError(t, err)
		assert.Equal(t, mongo.DefaultClientCacheTTL, ttl)

		for _, value := range []string{"0s", "-1s", "1ms", "99ms"} {
			config.ClientCacheTTL = value
			assert.ErrorContains(t, config.Validate(), "--mongo-client-cache-ttl", value)
			_, err := config.ParseClientCacheTTL()
			assert.ErrorIs(t, err, cache.ErrInvalidTTL, value)
		}

		config.ClientCacheTTL = "2s"
		assert.NoError(t, config.Validate())
		ttl, err = config.ParseClientCacheTTL()
		assert.NoError(t, err)
		assert.Equal(t, "2s", ttl.String())
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
