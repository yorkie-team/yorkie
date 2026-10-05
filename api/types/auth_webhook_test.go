/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

func TestMethodIsDeprecated(t *testing.T) {
	t.Run("watch aliases are deprecated", func(t *testing.T) {
		assert.True(t, types.WatchDocument.IsDeprecated())
		assert.True(t, types.WatchChannel.IsDeprecated())
	})

	t.Run("every deprecated method is covered by an enabled Watch", func(t *testing.T) {
		prj := &types.Project{
			AuthWebhookURL:     "http://localhost",
			AuthWebhookMethods: []string{string(types.Watch)},
		}
		for _, m := range types.AuthMethods() {
			if m.IsDeprecated() {
				assert.True(t, prj.RequireAuth(m), m)
			}
		}
	})

	t.Run("other auth methods are not deprecated", func(t *testing.T) {
		var active []types.Method
		for _, m := range types.AuthMethods() {
			if !m.IsDeprecated() {
				active = append(active, m)
			}
		}
		assert.Len(t, active, len(types.AuthMethods())-2)
		assert.Contains(t, active, types.Watch)
		assert.Contains(t, active, types.CreateRevision)
		assert.Contains(t, active, types.PeekChannel)
	})
}
