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

	"github.com/go-playground/validator/v10"
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

		switch err.(ValidError).Tag {
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
			Name string `validate:"required,slug,min=4,max=30"`
		}

		user := User{Name: "invalid-key-$-wrong-string-value"}

		err := ValidateStruct(user)

		fmt.Println(err.(ValidError).Field, err.(ValidError).Tag, err.(ValidError).Trans)

		assert.NotNil(t, err, "key should be invalid")

	})

	t.Run("add custom rule", func(t *testing.T) {

		// register custom rule tag and validation function
		RegisterValidation("custom", func(v validator.FieldLevel) bool {
			if v.Field().String() != "custom" {
				return false
			}

			return true
		})

		// custom error message for custom rule
		MyError := errors.New("custom error")
		RegisterTranslation("custom", MyError.Error())

		// validate value
		err := ValidateValue("custom-invalid-value", "required,custom")

		assert.NotNil(t, err, "value is must 'custom' string")
	})

	t.Run("simple custom rule", func(t *testing.T) {
		err := ValidateDynamically(
			"invalid custom rule",
			[]any{
				"required",
				CustomRule{
					Func: func(v validator.FieldLevel) bool {
						return v.Field().String() == "custom"
					},
				},
			},
		)

		assert.NotNil(t, err, "value is must 'custom' string")
	})

	t.Run("custom rule with error message", func(t *testing.T) {
		err := ValidateDynamically(
			"invalid custom rule",
			[]interface{}{
				"required",
				"min=3",
				"max=10",
			},
		)

		if err != nil {
			switch err.(ValidError).Tag {
			case "min":
				fmt.Println("min error")
			case "max":
				fmt.Println("max error")
			case "required":
				fmt.Println("required error")
			}
		}

		assert.NotNil(t, err, "value is must 'custom' string")
	})
}
