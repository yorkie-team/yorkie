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

	// EventWebhookURL is the URL of the event webhook.
	EventWebhookURL *string `bson:"event_webhook_url,omitempty" validate:"omitempty,url|emptystring"`

	// EventWebhookEvents is the events that trigger the webhook.
	EventWebhookEvents *[]string `bson:"event_webhook_events,omitempty" validate:"omitempty,invalid_webhook_event"`

	// ClientDeactivateThreshold is the time after which clients in specific project are considered deactivate.
	ClientDeactivateThreshold *string `bson:"client_deactivate_threshold,omitempty" validate:"omitempty,min=2,duration"`

	// MaxSubscribersPerDocument is the maximum number of subscribers per document.
	// If it is 0, there is no limit.
	MaxSubscribersPerDocument *int `bson:"max_subscribers_per_document,omitempty" validate:"omitempty,min=0"`

	// MaxAttachmentsPerDocument is the maximum number of attachments per document.
	// If it is 0, there is no limit.
	MaxAttachmentsPerDocument *int `bson:"max_attachments_per_document,omitempty" validate:"omitempty,min=0"`

	// MaxSizePerDocument is the maximum size of a document in bytes.
	MaxSizePerDocument *int `bson:"max_size_per_document,omitempty" validate:"omitempty,min=0"`

	// AllowedOrigins is the list of origins that are allowed to access the project.
	AllowedOrigins *[]string `bson:"allowed_origins,omitempty" validate:"omitempty,dive,valid_origin"`
}

// Validate validates the UpdatableProjectFields.
func (i *UpdatableProjectFields) Validate() error {
	if i.Name == nil &&
		i.AuthWebhookURL == nil &&
		i.AuthWebhookMethods == nil &&
		i.ClientDeactivateThreshold == nil &&
		i.EventWebhookURL == nil &&
		i.EventWebhookEvents == nil &&
		i.MaxSubscribersPerDocument == nil &&
		i.MaxAttachmentsPerDocument == nil &&
		i.MaxSizePerDocument == nil {
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

	if err := validation.RegisterValidation(
		"invalid_webhook_event",
		func(level validation.FieldLevel) bool {
			eventTypes := level.Field().Interface().([]string)
			for _, eventType := range eventTypes {
				if !IsValidEventType(eventType) {
					return false
				}
			}
			return true
		},
	); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}

	if err := validation.RegisterTranslation("invalid_webhook_event", "given {0} is invalid event type"); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}

	if err := validation.RegisterValidation(
		"valid_origin",
		func(level validation.FieldLevel) bool {
			origin := level.Field().String()
			if origin == "*" {
				return true
			}
			if err := validation.Validate(origin, []any{"url"}); err != nil {
				return false
			}
			return true
		},
	); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}

	if err := validation.RegisterTranslation("valid_origin", "given {0} must be a valid URL or '*'"); err != nil {
		fmt.Fprintln(os.Stderr, "updatable project fields: ", err)
		os.Exit(1)
	}
}
