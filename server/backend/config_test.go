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
		ProjectCacheTTL:     "10m",
	}
}
func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := newValidBackendConf()
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.AuthWebhookCacheTTL = "s"
		assert.Error(t, conf1.Validate())

		conf2 := validConf
		conf2.ProjectCacheTTL = "10 minutes"
		assert.Error(t, conf2.Validate())
	})

	t.Run("parse test", func(t *testing.T) {
		validConf := newValidBackendConf()

		assert.Equal(t, "24h0m0s", validConf.ParseAdminTokenDuration().String())
		assert.Equal(t, "10s", validConf.ParseAuthWebhookCacheTTL().String())
		assert.Equal(t, "10m0s", validConf.ParseProjectCacheTTL().String())
	})
}
