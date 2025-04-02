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
	"time"
)

var (
	// ErrTooManyAttachments is the error that the attachment limit is exceeded.
	ErrTooManyAttachments = errors.New("attachment limit exceeded")
)

// Project is a project that consists of multiple documents and clients.
type Project struct {
	// ID is the unique ID of the project.
	ID ID `json:"id"`

	// Name is the name of this project.
	Name string `json:"name"`

	// Owner is the owner of this project.
	Owner ID `json:"owner"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL string `json:"auth_webhook_url"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods []string `json:"auth_webhook_methods"`

	// EventWebhookURL is the url of the event webhook.
	EventWebhookURL string `json:"event_webhook_url"`

	// EventWebhookEvents are the events that event webhook will be triggered.
	EventWebhookEvents []string `json:"event_webhook_events"`

	// ClientDeactivateThreshold is the time after which clients in
	// specific project are considered deactivate for housekeeping.
	ClientDeactivateThreshold string `bson:"client_deactivate_threshold"`

	// MaxSubscribersPerDocument is the maximum number of subscribers per document.
	// If it is 0, there is no limit.
	MaxSubscribersPerDocument int `bson:"max_subscribers_per_document"`

	// MaxAttachmentsPerDocument is the maximum number of attachments per document.
	// If it is 0, there is no limit.
	MaxAttachmentsPerDocument int `bson:"max_attachments_per_document"`

	// AllowedOrigins is the list of allowed origins.
	AllowedOrigins []string `json:"allowed_origins"`

	// PublicKey is the API key of this project.
	PublicKey string `json:"public_key"`

	// SecretKey is the secret key of this project.
	SecretKey string `json:"secret_key"`

	// CreatedAt is the time when the project was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the time when the project was updated.
	UpdatedAt time.Time `json:"updated_at"`
}

// RequireAuth returns whether the given method requires authorization.
func (p *Project) RequireAuth(method Method) bool {
	if len(p.AuthWebhookURL) == 0 {
		return false
	}

	if len(p.AuthWebhookMethods) == 0 {
		return false
	}

	for _, m := range p.AuthWebhookMethods {
		if Method(m) == method {
			return true
		}
	}

	return false
}

// RequireEventWebhook returns whether the given type requires to send event webhook.
func (p *Project) RequireEventWebhook(eventType EventWebhookType) bool {
	if len(p.EventWebhookURL) == 0 {
		return false
	}

	if len(p.EventWebhookEvents) == 0 {
		return false
	}

	for _, t := range p.EventWebhookEvents {
		if EventWebhookType(t) == eventType {
			return true
		}
	}

	return false
}

// HasSubscriberLimit returns whether the document has a limit on the number of subscribers.
func (p *Project) HasSubscriberLimit() bool {
	return p.MaxSubscribersPerDocument > 0
}

// HasAttachmentLimit returns whether the document has a limit on the number of attachments.
func (p *Project) HasAttachmentLimit() bool {
	return p.MaxAttachmentsPerDocument > 0
}

// CheckAttachmentLimit checks whether the attachment limit is exceeded.
func (p *Project) CheckAttachmentLimit(count int) error {
	if p.MaxAttachmentsPerDocument > 0 && count >= p.MaxAttachmentsPerDocument {
		return fmt.Errorf("%d attachments allowed per document: %w",
			p.MaxAttachmentsPerDocument,
			ErrTooManyAttachments,
		)
	}

	return nil
}
