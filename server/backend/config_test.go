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
		AdminTokenDuration:  "24h",
		AuthWebhookCacheTTL: "10s",
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
}
