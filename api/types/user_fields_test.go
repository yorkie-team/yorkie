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

func TestSignupFields(t *testing.T) {
	var structError *validation.StructError

	t.Run("password validation test", func(t *testing.T) {
		validUsername := "test"
		validPassword := "pass123!"
		fields := &types.UserFields{
			Username: &validUsername,
			Password: &validPassword,
		}
		assert.NoError(t, fields.Validate())

		invalidPassword := "1234"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "abcd"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "!@#$"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "abcd1234"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "abcd!@#$"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "1234!@#$"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)

		invalidPassword = "abcd1234!@abcd1234!@abcd1234!@1"
		fields = &types.UserFields{
			Username: &validUsername,
			Password: &invalidPassword,
		}
		assert.ErrorAs(t, fields.Validate(), &structError)
	})
}
