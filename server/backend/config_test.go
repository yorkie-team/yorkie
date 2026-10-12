/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package backend_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend"
)

func newValidBackendConf() backend.Config {
	return backend.Config{
		AdminTokenDuration:           "24h",
		AuthWebhookCacheTTL:          "10s",
		ChannelSessionCountCacheSize: 1,
		ClusterClientPoolSize:        1,
		MaxConcurrentClusterRPCs:     1,
	}
}
func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := newValidBackendConf()
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.AuthWebhookCacheTTL = "s"
		assert.Error(t, conf1.Validate())
	})

	t.Run("reject cache TTLs below cache.MinTTL", func(t *testing.T) {
		for _, tc := range []struct {
			flag string
			set  func(conf *backend.Config, ttl string)
		}{
			{"--auth-webhook-cache-auth-ttl", func(c *backend.Config, ttl string) {
				c.AuthWebhookCacheTTL = ttl
			}},
			{"--channel-session-count-cache-ttl", func(c *backend.Config, ttl string) {
				c.ChannelSessionCountCacheTTL = ttl
			}},
		} {
			for _, ttl := range []string{"0", "0s", "-1s", "1ns", "1ms", "99ms"} {
				conf := newValidBackendConf()
				tc.set(&conf, ttl)
				assert.ErrorContains(t, conf.Validate(), tc.flag, ttl)
			}

			conf := newValidBackendConf()
			tc.set(&conf, "100ms")
			assert.NoError(t, conf.Validate())
		}
	})

	t.Run("validate MaxConcurrentClusterRPCs test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.MaxConcurrentClusterRPCs = 0
		assert.Error(t, conf.Validate())

		conf.MaxConcurrentClusterRPCs = -1
		assert.Error(t, conf.Validate())

		conf.MaxConcurrentClusterRPCs = 1
		assert.NoError(t, conf.Validate())
	})

	t.Run("validate ChannelSessionCountCacheSize test", func(t *testing.T) {
		conf := newValidBackendConf()
		assert.Equal(t, 1, conf.ChannelSessionCountCacheSize)

		conf.ChannelSessionCountCacheSize = 0
		assert.Error(t, conf.Validate())

		conf.ChannelSessionCountCacheSize = -1
		assert.Error(t, conf.Validate())
	})

	t.Run("validate ClusterRPCTimeout test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ClusterRPCTimeout = "invalid"
		assert.Error(t, conf.Validate())

		conf.ClusterRPCTimeout = "5s"
		assert.NoError(t, conf.Validate())

		conf.ClusterRPCTimeout = ""
		assert.NoError(t, conf.Validate())
	})

	t.Run("validate ClusterClientTimeout test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ClusterClientTimeout = "invalid"
		assert.Error(t, conf.Validate())

		conf.ClusterClientTimeout = "30s"
		assert.NoError(t, conf.Validate())

		conf.ClusterClientTimeout = ""
		assert.NoError(t, conf.Validate())
	})

	t.Run("parse test", func(t *testing.T) {
		validConf := newValidBackendConf()

		assert.Equal(t, "24h0m0s", validConf.ParseAdminTokenDuration().String())
		assert.Equal(t, "10s", validConf.ParseAuthWebhookCacheTTL().String())
	})

	t.Run("parse ClusterRPCTimeout test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ClusterRPCTimeout = "5s"
		assert.Equal(t, "5s", conf.ParseClusterRPCTimeout().String())

		conf.ClusterRPCTimeout = "10s"
		assert.Equal(t, "10s", conf.ParseClusterRPCTimeout().String())
	})

	t.Run("parse ClusterClientTimeout test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ClusterClientTimeout = "30s"
		assert.Equal(t, "30s", conf.ParseClusterClientTimeout().String())

		conf.ClusterClientTimeout = "1m"
		assert.Equal(t, "1m0s", conf.ParseClusterClientTimeout().String())
	})

	t.Run("validate watch heartbeat bounds test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.WatchHeartbeatIntervalMin = "5s"
		conf.WatchHeartbeatIntervalMax = "45s"
		assert.NoError(t, conf.Validate())

		// A minimum above the maximum has no interval to clamp into, so it is
		// caught at startup rather than silently resolved at request time.
		inverted := conf
		inverted.WatchHeartbeatIntervalMin = "90s"
		assert.Error(t, inverted.Validate())

		// Except against the kill switch, where a zero maximum is "off", not
		// a bound the minimum could exceed.
		off := conf
		off.WatchHeartbeatIntervalMax = "0s"
		assert.NoError(t, off.Validate())

		negative := conf
		negative.WatchHeartbeatIntervalMin = "-1s"
		assert.Error(t, negative.Validate())

		malformed := conf
		malformed.WatchHeartbeatIntervalMax = "s"
		assert.Error(t, malformed.Validate())
	})

	t.Run("parse watch heartbeat bounds test", func(t *testing.T) {
		conf := newValidBackendConf()
		// An unset pair gets the documented defaults, not a zero that would
		// read as the kill switch.
		lower, upper := conf.ParseWatchHeartbeatBounds()
		assert.Equal(t, backend.DefaultWatchHeartbeatIntervalMin, lower)
		assert.Equal(t, backend.DefaultWatchHeartbeatIntervalMax, upper)

		conf.WatchHeartbeatIntervalMin = "10s"
		conf.WatchHeartbeatIntervalMax = "45s"
		lower, upper = conf.ParseWatchHeartbeatBounds()
		assert.Equal(t, 10*time.Second, lower)
		assert.Equal(t, 45*time.Second, upper)

		// The deprecated flag survives only as an off switch.
		conf.WatchHeartbeatInterval = "0s"
		lower, upper = conf.ParseWatchHeartbeatBounds()
		assert.Zero(t, lower)
		assert.Zero(t, upper)

		conf.WatchHeartbeatInterval = "25s"
		lower, upper = conf.ParseWatchHeartbeatBounds()
		assert.Equal(t, 10*time.Second, lower)
		assert.Equal(t, 45*time.Second, upper)
	})

	t.Run("ClusterClientPoolSize field test", func(t *testing.T) {
		conf := newValidBackendConf()
		assert.Equal(t, 1, conf.ClusterClientPoolSize)

		conf.ClusterClientPoolSize = 10
		assert.Equal(t, 10, conf.ClusterClientPoolSize)
		assert.NoError(t, conf.Validate())
	})
}
