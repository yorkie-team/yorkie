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

package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestProjectInfo(t *testing.T) {
	t.Run("require auth test", func(t *testing.T) {
		// 1. Specify which methods to allow
		validWebhookURL := "ValidWebhookURL"
		info := &types.Project{
			AuthWebhookURL:     validWebhookURL,
			AuthWebhookMethods: []string{string(types.ActivateClient)},
		}
		assert.True(t, info.RequireAuth(types.ActivateClient))
		assert.False(t, info.RequireAuth(types.DetachDocument))

		// 2. No method specified
		info2 := &types.Project{
			AuthWebhookURL:     validWebhookURL,
			AuthWebhookMethods: []string{},
		}
		assert.False(t, info2.RequireAuth(types.ActivateClient))
		assert.False(t, info2.RequireAuth(types.DetachDocument))

		// 3. Empty webhook URL
		info3 := &types.Project{
			AuthWebhookURL: "",
		}
		assert.False(t, info3.RequireAuth(types.ActivateClient))
	})

	t.Run("require event webhook test", func(t *testing.T) {
		// 1. Specify which event types to allow
		validWebhookURL := "ValidWebhookURL"
		info := &types.Project{
			EventWebhookURL:    validWebhookURL,
			EventWebhookEvents: []string{string(types.DocRootChanged)},
		}
		assert.True(t, info.RequireEventWebhook(types.DocRootChanged))

		// 2. No event types specified
		info2 := &types.Project{
			EventWebhookURL:    validWebhookURL,
			EventWebhookEvents: []string{},
		}
		assert.False(t, info2.RequireEventWebhook(types.DocRootChanged))

		// 3. Empty webhook URL
		info3 := &types.Project{
			EventWebhookURL: "",
		}
		assert.False(t, info3.RequireEventWebhook(types.DocRootChanged))
	})

	t.Run("require IsSubscriptionLimitEnabled test", func(t *testing.T) {
		info := &types.Project{
			SubscriptionLimitPerDocument: 1,
		}
		assert.True(t, info.IsSubscriptionLimitEnabled())

		info2 := &types.Project{
			SubscriptionLimitPerDocument: 0,
		}
		assert.False(t, info2.IsSubscriptionLimitEnabled())
	})
}
