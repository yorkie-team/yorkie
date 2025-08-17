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
	"strings"

	"github.com/yorkie-team/yorkie/internal/validation"
)

// UserFields is a set of fields that use to sign up or change password to yorkie server.
type UserFields struct {
	// Username is the name of user.
	Username *string `bson:"username" validate:"required,min=2,max=30,slug"`

	// Password is the password of user.
	Password *string `bson:"password" validate:"required,min=8,max=30,alpha_num_special"`
}

// Validate validates the UserFields.
func (i *UserFields) Validate() error {
	err := validation.ValidateStruct(i)
	if err != nil {
		return i.getDetailedError(err)
	}
	return nil
}

// getDetailedError creates user-friendly error messages from validation errors
func (i *UserFields) getDetailedError(err error) error {
	var messages []string

	var formErr *validation.FormError
	if errors.As(err, &formErr) {
		for _, violation := range formErr.Violations {
			msg := i.getFieldErrorMessage(violation.Field, violation.Tag)
			if msg != "" {
				messages = append(messages, msg)
			}
		}
	}

	if len(messages) > 0 {
		return fmt.Errorf(strings.Join(messages, "; "))
	}

	return err
}

// getFieldErrorMessage returns specific field error messages
func (i *UserFields) getFieldErrorMessage(field, tag string) string {
	switch field {
	case "Username":
		return i.getUsernameErrorMessage(tag)
	case "Password":
		return i.getPasswordErrorMessage(tag)
	default:
		return fmt.Sprintf("%s validation failed", field)
	}
}

// getUsernameErrorMessage returns specific error messages for username validation
func (i *UserFields) getUsernameErrorMessage(tag string) string {
	switch tag {
	case "required":
		return "username is required"
	case "min":
		return "username must be at least 2 characters long"
	case "max":
		return "username must be at most 30 characters long"
	case "slug":
		return "username must only contain letters, numbers, hyphen, period, underscore, and tilde"
	default:
		return fmt.Sprintf("username validation failed: %s", tag)
	}
}

// getPasswordErrorMessage returns specific error messages for password validation
func (i *UserFields) getPasswordErrorMessage(tag string) string {
	switch tag {
	case "required":
		return "password is required"
	case "min":
		return "password must be at least 8 characters long"
	case "max":
		return "password must be at most 30 characters long"
	case "alpha_num_special":
		return "password must include letters, numbers, and special characters"
	default:
		return fmt.Sprintf("password validation failed: %s", tag)
	}
}
