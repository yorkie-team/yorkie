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

func TestProjectField(t *testing.T) {
	// test ProjectField.validate() works correctly
	t.Run("validation test", func(t *testing.T) {
		// happy case test
		newName := "changed-name"
		newAuthWebhookURL := "newWebhookURL"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		field := &types.ProjectField{
			Name:               newName,
			AuthWebhookURL:     newAuthWebhookURL,
			AuthWebhookMethods: newAuthWebhookMethods,
		}
		assert.NoError(t, field.Validate())

		// Empty project field test
		field = &types.ProjectField{}
		assert.ErrorIs(t, field.Validate(), types.ErrProjectFieldEmpty)

		// Partial empty test
		field = &types.ProjectField{
			Name: newName,
		}
		assert.NoError(t, field.Validate())

		field = &types.ProjectField{
			Name:           newName,
			AuthWebhookURL: newAuthWebhookURL,
		}
		assert.NoError(t, field.Validate())

		// Wrong AuthWebhookMethods test
		newAuthWebhookMethods = []string{
			"wrong methods",
		}
		field = &types.ProjectField{
			Name:               newName,
			AuthWebhookURL:     newAuthWebhookURL,
			AuthWebhookMethods: newAuthWebhookMethods,
		}
		assert.ErrorIs(t, field.Validate(), types.ErrNotSupportedMethod)
	})
}
