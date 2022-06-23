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
	"fmt"
	"regexp"

	"github.com/go-playground/validator/v10"
)

var (
	// ErrEmptyProjectFields is returned when all the fields are empty.
	ErrEmptyProjectFields = errors.New("UpdatableProjectFields is empty")

	// ErrNotSupportedMethod is returned when the method is not supported.
	ErrNotSupportedMethod = errors.New("not supported method for authorization webhook")

	// ErrInvalidProjectName is returned when the name is invalid.
	ErrInvalidProjectName = errors.New("invalid project name")
)

// reservedNames gives reserved words for project name.
func reservedNames() []string {
	return []string{"new", "default"}
}

// isReservedNames returns whether the given name is one of reserved names
func isReservedNames(name string) bool {
	for _, v := range reservedNames() {
		if v == name {
			return true
		}
	}
	return false
}

// UpdatableProjectFields is a set of fields that use to update a project.
type UpdatableProjectFields struct {
	// Name is the name of this project.
	Name *string `bson:"name,omitempty" validate:"omitempty,min=2,max=30,name"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL *string `bson:"auth_webhook_url,omitempty"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods *[]string `bson:"auth_webhook_methods,omitempty"`
}

// Validate validates the UpdatableProjectFields.
func (i *UpdatableProjectFields) Validate() error {
	if i.Name == nil && i.AuthWebhookURL == nil && i.AuthWebhookMethods == nil {
		return ErrEmptyProjectFields
	}
	if i.AuthWebhookMethods != nil {
		for _, method := range *i.AuthWebhookMethods {
			if !IsAuthMethod(method) {
				return fmt.Errorf("%s: %w", method, ErrNotSupportedMethod)
			}
		}
	}

	v := validator.New()
	if err := v.RegisterValidation("name", func(fl validator.FieldLevel) bool {
		// NOTE(DongjinS): regular expression is referenced unreserved characters
		// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
		// and copied from https://gist.github.com/dpk/4757681
		nameRegexString := "^[a-z0-9\\-._~]+$"
		nameRegex := regexp.MustCompile(nameRegexString)
		name := fl.Field().String()
		return nameRegex.MatchString(name) && !isReservedNames(name)
	}); err != nil {
		return err
	}

	err := v.Struct(i)
	if err != nil {
		for _, err := range err.(validator.ValidationErrors) {
			if fieldName := err.Field(); fieldName == "Name" {
				return fmt.Errorf("%s: %w", *i.Name, ErrInvalidProjectName)
			}
		}
		return err
	}

	return nil
}
