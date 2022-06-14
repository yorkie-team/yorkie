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
)

var (
	// ErrProjectFieldEmpty is returned when the ProjectField is empty.
	ErrProjectFieldEmpty = errors.New("project field is empty")

	// ErrNotSupportedMethod is returned when the method is not supported.
	ErrNotSupportedMethod = errors.New("not supported method for authorization webhook")
)

// ProjectField is a set of fields that use to update a project.
type ProjectField struct {
	// Name is the name of this project.
	Name string `json:"name"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL string `json:"auth_webhook_url"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods []string `json:"auth_webhook_methods"`
}

// Validate validates the ProjectField.
func (i *ProjectField) Validate() error {
	// Check empty ProjectField
	if i.Name == "" && i.AuthWebhookURL == "" && len(i.AuthWebhookMethods) == 0 {
		return fmt.Errorf("%s: %w", i, ErrProjectFieldEmpty)
	}
	// Check wrong AuthWebhookMethods
	for _, method := range i.AuthWebhookMethods {
		if !IsAuthMethod(method) {
			return fmt.Errorf("%s: %w", method, ErrNotSupportedMethod)
		}
	}

	return nil
}
