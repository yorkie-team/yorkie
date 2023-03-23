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
	"os"

	"github.com/yorkie-team/yorkie/internal/validation"
)

// ErrEmptyProjectFields is returned when all the fields are empty.
var ErrEmptyProjectFields = errors.New("updatable project fields are empty")

// UpdatableProjectFields is a set of fields that use to update a project.
type UpdatableProjectFields struct {
	// Name is the name of this project.
	Name *string `bson:"name,omitempty" validate:"omitempty,min=2,max=30,slug,reserved_project_name"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL *string `bson:"auth_webhook_url,omitempty" validate:"omitempty,url|emptystring"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods *[]string `bson:"auth_webhook_methods,omitempty" validate:"omitempty,invalid_webhook_method"`

	// ClientDeactivateThreshold is the time after which clients in specific project are considered deactivate.
	ClientDeactivateThreshold *string `bson:"client_deactivate_threshold,omitempty" validate:"omitempty,min=2,duration"`
}

// Validate validates the UpdatableProjectFields.
func (i *UpdatableProjectFields) Validate() error {
	if i.Name == nil && i.AuthWebhookURL == nil && i.AuthWebhookMethods == nil && i.ClientDeactivateThreshold == nil {
		return ErrEmptyProjectFields
	}

	return validation.ValidateStruct(i)
}

func init() {
	if err := validation.RegisterValidation(
		"invalid_webhook_method",
		func(level validation.FieldLevel) bool {
			methods := level.Field().Interface().([]string)
			for _, method := range methods {
				if !IsAuthMethod(method) {
					return false
				}
			}
			return true
		},
	); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}
	if err := validation.RegisterTranslation("invalid_webhook_method", "given {0} is invalid method"); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}
}
