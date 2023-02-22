/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
package validation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationManager_Validate(t *testing.T) {
	t.Run("use ValidationManager to validate value", func(t *testing.T) {

		err := ValidateValue("valid-key", "required,slug,min=4,max=30")

		assert.Nil(t, err, "key should be valid")
	})

	t.Run("use ValidationManager to validate value", func(t *testing.T) {

		err := ValidateValue("invalid key", "required,slug,min=4,max=30")

		assert.NotNil(t, err, "key should be invalid")
	})

	t.Run("use ValidationManager to validate value", func(t *testing.T) {

		err := ValidateValue("invalid-key-~$a", "required,slug,min=4,max=30")

		assert.NotNil(t, err, "key should be invalid")
	})

	t.Run("use ValidationManager to validate value", func(t *testing.T) {

		err := ValidateValue("invalid-key-$-wrong-string-value", "required,slug,min=4,max=30")

		assert.NotNil(t, err, "key should be invalid")
	})

	t.Run("invalid value", func(t *testing.T) {

		err := ValidateValue("invalid-key-$-wrong-string-value", "required,slug,min=4,max=30")

		switch err.(Violation).Tag {
		case "slug":
			fmt.Println("slug error")
		case "max":
			fmt.Println("max error")
		case "min":
			fmt.Println("min error")
		}

		assert.NotNil(t, err, "key should be invalid")
	})

	t.Run("invalid struct value", func(t *testing.T) {
		type User struct {
			Name    string `validate:"required,slug,min=4,max=30"`
			Country string `validate:"required,min=2,max=2"`
		}

		user := User{Name: "invalid-key-$-wrong-string-value", Country: "korea"}

		err := ValidateStruct(user)
		structError := err.(*StructError)
		assert.Len(t, structError.Violations, 2, "user should be invalid")
	})

	t.Run("add custom rule", func(t *testing.T) {
		// register custom rule tag and validation function
		RegisterValidation("custom", func(v FieldLevel) bool {
			return v.Field().String() == "custom"
		})

		// custom error message for custom rule
		myError := errors.New("custom error")
		RegisterTranslation("custom", myError.Error())

		// validate value
		err := ValidateValue("custom-invalid-value", "required,custom")
		assert.NotNil(t, err, "value is must 'custom' string")
	})

	t.Run("simple custom rule", func(t *testing.T) {
		err := Validate(
			"invalid custom rule",
			[]any{
				"required",
				CustomRule{
					Tag: "custom",
					Func: func(v FieldLevel) bool {
						return v.Field().String() == "custom"
					},
				},
			},
		)

		assert.Equal(t, "custom", err.(Violation).Tag, "value is must 'custom' string")
	})

	t.Run("custom rule with error message", func(t *testing.T) {
		err := Validate(
			"invalid custom rule",
			[]interface{}{
				"required",
				"min=3",
				"max=10",
			},
		)
		if err != nil {
			switch err.(Violation).Tag {
			case "min":
				fmt.Println("min error")
			case "max":
				fmt.Println("max error")
			case "required":
				fmt.Println("required error")
			}
		}

		assert.Equal(t, "max", err.(Violation).Tag, "value is must 'custom' string")
	})
}
