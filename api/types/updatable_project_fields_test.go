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

func TestUpdatableProjectFields(t *testing.T) {
	t.Run("validation test", func(t *testing.T) {
		newName := "changed-name"
		newAuthWebhookURL := "newWebhookURL"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		fields := &types.UpdatableProjectFields{
			Name:               &newName,
			AuthWebhookURL:     &newAuthWebhookURL,
			AuthWebhookMethods: &newAuthWebhookMethods,
		}
		assert.NoError(t, fields.Validate())

		fields = &types.UpdatableProjectFields{}
		assert.ErrorIs(t, fields.Validate(), types.ErrEmptyProjectFields)

		fields = &types.UpdatableProjectFields{
			Name: &newName,
		}
		assert.NoError(t, fields.Validate())

		fields = &types.UpdatableProjectFields{
			Name:           &newName,
			AuthWebhookURL: &newAuthWebhookURL,
		}
		assert.NoError(t, fields.Validate())

		// invalid AuthWebhookMethods
		newAuthWebhookMethods = []string{
			"InvalidMethods",
		}
		fields = &types.UpdatableProjectFields{
			Name:               &newName,
			AuthWebhookURL:     &newAuthWebhookURL,
			AuthWebhookMethods: &newAuthWebhookMethods,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrNotSupportedMethod)
	})

	t.Run("project name format test", func(t *testing.T) {
		validName := "valid-name"
		fields := &types.UpdatableProjectFields{
			Name: &validName,
		}
		assert.NoError(t, fields.Validate())

		invalidName := "has blank"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)

		reservedName := "new"
		fields = &types.UpdatableProjectFields{
			Name: &reservedName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)

		reservedName = "default"
		fields = &types.UpdatableProjectFields{
			Name: &reservedName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)

		invalidName = "1"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)

		invalidName = "over_30_chracaters_is_invalid_name"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)

		invalidName = "invalid/name"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorIs(t, fields.Validate(), types.ErrInvalidProjectName)
	})
}
