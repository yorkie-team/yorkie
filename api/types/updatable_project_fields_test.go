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
	"github.com/yorkie-team/yorkie/internal/validation"
)

func TestUpdatableProjectFields(t *testing.T) {
	var formErr *validation.FormError
	t.Run("validation test", func(t *testing.T) {
		newName := "changed-name"
		newAuthWebhookURL := "http://localhost:3000"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		newEventWebhookURL := "http://localhost:4000"
		newEventWebhookTypes := []string{
			string(types.DocRootChanged),
		}
		newClientDeactivateThreshold := "1h"
		fields := &types.UpdatableProjectFields{
			Name:                      &newName,
			AuthWebhookURL:            &newAuthWebhookURL,
			AuthWebhookMethods:        &newAuthWebhookMethods,
			EventWebhookURL:           &newEventWebhookURL,
			EventWebhookTypes:         &newEventWebhookTypes,
			ClientDeactivateThreshold: &newClientDeactivateThreshold,
		}
		assert.NoError(t, fields.Validate())

		fields = &types.UpdatableProjectFields{}
		assert.ErrorIs(t, fields.Validate(), types.ErrEmptyProjectFields)

		fields = &types.UpdatableProjectFields{
			Name: &newName,
		}
		assert.NoError(t, fields.Validate())

		fields = &types.UpdatableProjectFields{
			Name:                      &newName,
			AuthWebhookURL:            &newAuthWebhookURL,
			EventWebhookURL:           &newEventWebhookURL,
			ClientDeactivateThreshold: &newClientDeactivateThreshold,
		}
		assert.NoError(t, fields.Validate())

		// invalid AuthWebhookMethods
		newAuthWebhookMethods = []string{
			"InvalidMethods",
		}
		// invalid EventWebhookTypes
		newEventWebhookTypes = []string{
			"DocChanged",
		}
		fields = &types.UpdatableProjectFields{
			Name:                      &newName,
			AuthWebhookURL:            &newAuthWebhookURL,
			AuthWebhookMethods:        &newAuthWebhookMethods,
			EventWebhookTypes:         &newEventWebhookTypes,
			ClientDeactivateThreshold: &newClientDeactivateThreshold,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)
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
		assert.ErrorAs(t, fields.Validate(), &formErr)

		reservedName := "new"
		fields = &types.UpdatableProjectFields{
			Name: &reservedName,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)

		reservedName = "default"
		fields = &types.UpdatableProjectFields{
			Name: &reservedName,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)

		invalidName = "1"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)

		invalidName = "over_30_chracaters_is_invalid_name"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)

		invalidName = "invalid/name"
		fields = &types.UpdatableProjectFields{
			Name: &invalidName,
		}
		assert.ErrorAs(t, fields.Validate(), &formErr)
	})
}
