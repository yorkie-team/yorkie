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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend"
)

func newValidBackendConf() backend.Config {
	return backend.Config{
		SecretKey:                     "secret-key",
		AdminTokenDuration:            "24h",
		AuthWebhookCacheTTL:           "10s",
		ChannelSessionTTL:             "60s",
		ChannelSessionCleanupInterval: "10s",
		ChannelSessionCountCacheTTL:   "30s",
		ChannelSessionCountCacheSize:  1,
		ClusterRPCTimeout:             "10s",
		ClusterClientTimeout:          "30s",
		ClusterClientPoolSize:         1,
		MaxConcurrentClusterRPCs:      1,
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

	t.Run("reject a TTL the expirable cache cannot honor", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.AuthWebhookCacheTTL = "1ns"
		assert.ErrorContains(t, conf.Validate(), "auth-webhook-cache-auth-ttl")

		// Zero and negative are read as "never expire" by the expirable LRU,
		// which would keep a revoked authorization cached until restart.
		conf.AuthWebhookCacheTTL = "0s"
		assert.ErrorContains(t, conf.Validate(), "auth-webhook-cache-auth-ttl")

		conf.AuthWebhookCacheTTL = "-1s"
		assert.ErrorContains(t, conf.Validate(), "auth-webhook-cache-auth-ttl")

		conf.AuthWebhookCacheTTL = "1ms"
		assert.NoError(t, conf.Validate())
	})

	t.Run("reject a session count cache TTL the expirable cache cannot honor", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ChannelSessionCountCacheTTL = "1ns"
		assert.ErrorContains(t, conf.Validate(), "channel-session-count-cache-ttl")

		conf.ChannelSessionCountCacheTTL = "0s"
		assert.ErrorContains(t, conf.Validate(), "channel-session-count-cache-ttl")

		conf.ChannelSessionCountCacheTTL = "1s"
		assert.NoError(t, conf.Validate())
	})

	t.Run("reject empty durations rather than read them as unset", func(t *testing.T) {
		// A duration left empty passes through to a Parse* helper that exits
		// the process, so every one of them has to fail Validate instead.
		for _, tc := range []struct {
			flag string
			set  func(conf *backend.Config)
		}{
			{"admin-token-duration", func(c *backend.Config) { c.AdminTokenDuration = "" }},
			{"auth-webhook-cache-auth-ttl", func(c *backend.Config) { c.AuthWebhookCacheTTL = "" }},
			{"channel-session-ttl", func(c *backend.Config) { c.ChannelSessionTTL = "" }},
			{"channel-session-cleanup-interval", func(c *backend.Config) {
				c.ChannelSessionCleanupInterval = ""
			}},
			{"channel-session-count-cache-ttl", func(c *backend.Config) {
				c.ChannelSessionCountCacheTTL = ""
			}},
			{"cluster-rpc-timeout", func(c *backend.Config) { c.ClusterRPCTimeout = "" }},
			{"cluster-client-timeout", func(c *backend.Config) { c.ClusterClientTimeout = "" }},
		} {
			conf := newValidBackendConf()
			tc.set(&conf)
			assert.ErrorContains(t, conf.Validate(), tc.flag)
		}
	})

	t.Run("reject an empty effective cluster secret", func(t *testing.T) {
		// The cluster interceptor fails closed, so an empty secret would reject
		// every inter-node RPC at runtime instead of at startup.
		conf := newValidBackendConf()
		conf.SecretKey = ""
		assert.ErrorContains(t, conf.Validate(), "cluster-secret")

		conf.ClusterSecret = "cluster-secret"
		assert.NoError(t, conf.Validate())
	})

	t.Run("report the published default secret as unprotected", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.SecretKey = backend.DefaultSecretKey
		assert.True(t, conf.UsesDefaultClusterSecret())

		conf.ClusterSecret = "cluster-secret"
		assert.False(t, conf.UsesDefaultClusterSecret())
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

		// Empty is not an "unset" reading: ParseClusterRPCTimeout would
		// os.Exit(1) on it, so Validate has to refuse it first.
		conf.ClusterRPCTimeout = ""
		assert.ErrorContains(t, conf.Validate(), "cluster-rpc-timeout")
	})

	t.Run("validate ClusterClientTimeout test", func(t *testing.T) {
		conf := newValidBackendConf()
		conf.ClusterClientTimeout = "invalid"
		assert.Error(t, conf.Validate())

		conf.ClusterClientTimeout = "30s"
		assert.NoError(t, conf.Validate())

		conf.ClusterClientTimeout = ""
		assert.ErrorContains(t, conf.Validate(), "cluster-client-timeout")
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

	t.Run("ClusterClientPoolSize field test", func(t *testing.T) {
		conf := newValidBackendConf()
		assert.Equal(t, 1, conf.ClusterClientPoolSize)

		conf.ClusterClientPoolSize = 10
		assert.Equal(t, 10, conf.ClusterClientPoolSize)
		assert.NoError(t, conf.Validate())
	})
}
