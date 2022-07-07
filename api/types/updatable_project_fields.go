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
 *
 */

package types

import (
	"errors"
	"regexp"

	"github.com/go-playground/validator/v10"
)

// ErrEmptyProjectFields is returned when all the fields are empty.
var ErrEmptyProjectFields = errors.New("UpdatableProjectFields is empty")

var (
	// reservedNames is a map of reserved names. It is used to check if the
	// given project name is reserved or not.
	reservedNames = map[string]bool{"new": true, "default": true}

	// NOTE(DongjinS): regular expression is referenced unreserved characters
	// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
	// and copied from https://gist.github.com/dpk/4757681
	nameRegex = regexp.MustCompile("^[a-z0-9\\-._~]+$")
)

// FieldViolation is used to describe a single bad request field
type FieldViolation struct {
	// A Field of which field of the reques is bad.
	Field string
	// A description of why the request element is bad.
	Description string
}

// InvalidFieldsError is used to describe invalid fields.
type InvalidFieldsError struct {
	Violations []*FieldViolation
}

// Error returns the error message.
func (e *InvalidFieldsError) Error() string { return "invalid project fields" }

func isReservedName(name string) bool {
	if _, ok := reservedNames[name]; ok {
		return true
	}
	return false
}

// UpdatableProjectFields is a set of fields that use to update a project.
type UpdatableProjectFields struct {
	// Name is the name of this project.
	Name *string `bson:"name,omitempty" validate:"omitempty,min=2,max=30,slug,reservedname"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL *string `bson:"auth_webhook_url,omitempty"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods *[]string `bson:"auth_webhook_methods,omitempty" validate:"omitempty,invalidmethod"`
}

// Validate validates the UpdatableProjectFields.
func (i *UpdatableProjectFields) Validate() error {
	if i.Name == nil && i.AuthWebhookURL == nil && i.AuthWebhookMethods == nil {
		return ErrEmptyProjectFields
	}

	if err := defaultValidator.Struct(i); err != nil {
		invalidFieldsError := &InvalidFieldsError{}
		for _, err := range err.(validator.ValidationErrors) {
			v := &FieldViolation{
				Field:       err.StructField(),
				Description: err.Translate(trans),
			}
			invalidFieldsError.Violations = append(invalidFieldsError.Violations, v)
		}
		return invalidFieldsError
	}

	return nil
}

func init() {
	registerValidation("slug", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return nameRegex.MatchString(name)
	})
	registerTranslation("slug", "{0} must only contain slug available characters")

	registerValidation("reservedname", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return !isReservedName(name)
	})
	registerTranslation("reservedname", "given {0} is reserved name")

	registerValidation("invalidmethod", func(level validator.FieldLevel) bool {
		methods := level.Field().Interface().([]string)
		for _, method := range methods {
			if !IsAuthMethod(method) {
				return false
			}
		}
		return true
	})
	registerTranslation("invalidmethod", "given {0} is invalid method")
}
