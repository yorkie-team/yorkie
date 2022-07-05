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
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

var (
	// ErrEmptyProjectFields is returned when all the fields are empty.
	ErrEmptyProjectFields = errors.New("UpdatableProjectFields is empty")

	// ErrNotSupportedMethod is returned when the method is not supported.
	ErrNotSupportedMethod = errors.New("not supported method for authorization webhook")

	// ErrInvalidProjectField is returned when the field is invalid.
	ErrInvalidProjectField = errors.New("invalid project field")
)

var (
	// reservedNames is a map of reserved names. It is used to check if the
	// given project name is reserved or not.
	reservedNames = map[string]bool{"new": true, "default": true}

	// NOTE(DongjinS): regular expression is referenced unreserved characters
	// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
	// and copied from https://gist.github.com/dpk/4757681
	nameRegex = regexp.MustCompile("^[a-z0-9\\-._~]+$")
)

// ErrorWithDetails is error for deliver details with error
type ErrorWithDetails struct {
	err     error
	details interface{}
}

// Error returns Error() of ErrorWithDetails' err
func (e *ErrorWithDetails) Error() string {
	return e.err.Error()
}

// GetDetails returns details of ErrorWithDetails
func (e *ErrorWithDetails) GetDetails() interface{} {
	return e.details
}

// GetError returns err of ErrorWithDetails
func (e *ErrorWithDetails) GetError() error {
	return e.err
}

func isReservedName(name string) bool {
	if _, ok := reservedNames[name]; ok {
		return true
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

	if err := defaultValidator.Struct(i); err != nil {
		for _, err := range err.(validator.ValidationErrors) {
			desc := "The Project name must only contain url available characters"
			v := &errdetails.BadRequest_FieldViolation{
				Field:       "project-name",
				Description: desc,
			}
			br := &errdetails.BadRequest{}
			br.FieldViolations = append(br.FieldViolations, v)

			return &ErrorWithDetails{
				err:     fmt.Errorf("%s: %w", err, ErrInvalidProjectField),
				details: br,
			}
		}
		return err
	}

	return nil
}

func init() {
	registerValidation("name", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return !isReservedName(name) && nameRegex.MatchString(name)
	})
}
