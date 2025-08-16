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

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := backend.Config{
			AdminTokenDuration:          "24h",
			ClientDeactivateThreshold:   "1h",
			AuthWebhookMaxWaitInterval:  "0ms",
			AuthWebhookMinWaitInterval:  "0ms",
			AuthWebhookRequestTimeout:   "0ms",
			AuthWebhookCacheTTL:         "10s",
			ProjectCacheTTL:             "10m",
			EventWebhookMaxWaitInterval: "0ms",
			EventWebhookMinWaitInterval: "0ms",
			EventWebhookRequestTimeout:  "0ms",
		}
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.ClientDeactivateThreshold = "1 hour"
		assert.Error(t, conf1.Validate())

		conf2 := validConf
		conf2.AuthWebhookMaxWaitInterval = "5"
		assert.Error(t, conf2.Validate())

		conf3 := validConf
		conf3.AuthWebhookMinWaitInterval = "3"
		assert.Error(t, conf3.Validate())

		conf4 := validConf
		conf4.AuthWebhookRequestTimeout = "1"
		assert.Error(t, conf4.Validate())

		conf5 := validConf
		conf5.AuthWebhookCacheTTL = "s"
		assert.Error(t, conf5.Validate())

		conf6 := validConf
		conf6.ProjectCacheTTL = "10 minutes"
		assert.Error(t, conf6.Validate())

		conf7 := validConf
		conf7.EventWebhookMaxWaitInterval = "5"
		assert.Error(t, conf7.Validate())

		conf8 := validConf
		conf8.EventWebhookMinWaitInterval = "3"
		assert.Error(t, conf8.Validate())

		conf9 := validConf
		conf9.EventWebhookRequestTimeout = "1"
		assert.Error(t, conf9.Validate())
	})
	t.Run("parse test", func(t *testing.T) {
		validConf := backend.Config{
			AdminTokenDuration:          "24h",
			ClientDeactivateThreshold:   "1h",
			AuthWebhookMaxWaitInterval:  "0ms",
			AuthWebhookMinWaitInterval:  "0ms",
			AuthWebhookRequestTimeout:   "0ms",
			AuthWebhookCacheTTL:         "10s",
			ProjectCacheTTL:             "10m",
			EventWebhookMaxWaitInterval: "0ms",
			EventWebhookMinWaitInterval: "0ms",
			EventWebhookRequestTimeout:  "0ms",
		}

		assert.Equal(t, "24h0m0s", validConf.ParseAdminTokenDuration().String())
		assert.Equal(t, "0s", validConf.ParseAuthWebhookMaxWaitInterval().String())
		assert.Equal(t, "0s", validConf.ParseAuthWebhookMinWaitInterval().String())
		assert.Equal(t, "0s", validConf.ParseAuthWebhookRequestTimeout().String())
		assert.Equal(t, "10s", validConf.ParseAuthWebhookCacheTTL().String())
		assert.Equal(t, "10m0s", validConf.ParseProjectCacheTTL().String())
		assert.Equal(t, "0s", validConf.ParseEventWebhookMaxWaitInterval().String())
		assert.Equal(t, "0s", validConf.ParseEventWebhookMinWaitInterval().String())
		assert.Equal(t, "0s", validConf.ParseEventWebhookRequestTimeout().String())
	})
}
