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

package backend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

func TestConfig(t *testing.T) {
	t.Run("authorization webhook config test", func(t *testing.T) {
		conf := backend.Config{
			AuthWebhookURL:              "ValidWebhookURL",
			AuthorizationWebhookMethods: []string{"InvalidMethod"},
		}
		assert.Error(t, conf.Validate())

		conf2 := backend.Config{
			AuthWebhookURL:              "ValidWebhookURL",
			AuthorizationWebhookMethods: []string{string(types.ActivateClient)},
		}
		assert.NoError(t, conf2.Validate())
		assert.True(t, conf2.RequireAuth(types.ActivateClient))
		assert.False(t, conf2.RequireAuth(types.DetachDocument))

		conf3 := backend.Config{
			AuthWebhookURL:              "ValidWebhookURL",
			AuthorizationWebhookMethods: []string{},
		}
		assert.NoError(t, conf3.Validate())
		assert.True(t, conf3.RequireAuth(types.ActivateClient))
		assert.True(t, conf3.RequireAuth(types.DetachDocument))
	})
}
