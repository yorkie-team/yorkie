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

	"github.com/yorkie-team/yorkie/pkg/webhook"
)

var (
	// ErrTooManyAttachments is the error that the attachment limit is exceeded.
	ErrTooManyAttachments = errors.New("attachment limit exceeded")

	// ErrInvalidTimeDurationString is returned when the given time duration string is not in valid format.
	ErrInvalidTimeDurationString = errors.New("invalid time duration string format")
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

	// AuthWebhookMaxRetries is the max count that retries the authorization webhook.
	AuthWebhookMaxRetries uint64 `bson:"auth_webhook_max_retries"`

	// AuthWebhookMinWaitInterval is the min interval that waits before retrying the authorization webhook.
	AuthWebhookMinWaitInterval string `bson:"auth_webhook_min_wait_interval"`

	// AuthWebhookMaxWaitInterval is the max interval that waits before retrying the authorization webhook.
	AuthWebhookMaxWaitInterval string `bson:"auth_webhook_max_wait_interval"`

	// AuthWebhookRequestTimeout is the max waiting time per auth webhook request.
	AuthWebhookRequestTimeout string `bson:"auth_webhook_request_timeout"`

	// EventWebhookURL is the url of the event webhook.
	EventWebhookURL string `json:"event_webhook_url"`

	// EventWebhookEvents are the events that event webhook will be triggered.
	EventWebhookEvents []string `json:"event_webhook_events"`

	// EventWebhookMaxRetries is the max count that retries the event webhook.
	EventWebhookMaxRetries uint64 `bson:"event_webhook_max_retries"`

	// EventWebhookMinWaitInterval is the min interval that waits before retrying the event webhook.
	EventWebhookMinWaitInterval string `bson:"event_webhook_min_wait_interval"`

	// EventWebhookMaxWaitInterval is the max interval that waits before retrying the event webhook.
	EventWebhookMaxWaitInterval string `bson:"event_webhook_max_wait_interval"`

	// EventWebhookRequestTimeout is the max waiting time per event webhook request.
	EventWebhookRequestTimeout string `bson:"event_webhook_request_timeout"`

	// ClientDeactivateThreshold is the time after which clients in
	// specific project are considered deactivate for housekeeping.
	ClientDeactivateThreshold string `bson:"client_deactivate_threshold"`

	// MaxSubscribersPerDocument is the maximum number of subscribers per document.
	// If it is 0, there is no limit.
	MaxSubscribersPerDocument int `bson:"max_subscribers_per_document"`

	// MaxAttachmentsPerDocument is the maximum number of attachments per document.
	// If it is 0, there is no limit.
	MaxAttachmentsPerDocument int `bson:"max_attachments_per_document"`

	// MaxSizePerDocument is the maximum size of a document in bytes.
	MaxSizePerDocument int `bson:"max_size_per_document"`

	// RemoveOnDetach is the flag to remove the document on detach.
	RemoveOnDetach bool `json:"remove_on_detach"`

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

// IsAttachmentLimitExceeded checks whether the attachment limit is exceeded.
func (p *Project) IsAttachmentLimitExceeded(count int) error {
	if p.MaxAttachmentsPerDocument > 0 && count >= p.MaxAttachmentsPerDocument {
		return fmt.Errorf("%d attachments allowed per document: %w",
			p.MaxAttachmentsPerDocument,
			ErrTooManyAttachments,
		)
	}

	return nil
}

// ClientDeactivateThresholdAsTimeDuration converts ClientDeactivateThreshold string to time.Duration.
func (p *Project) ClientDeactivateThresholdAsTimeDuration() (time.Duration, error) {
	clientDeactivateThreshold, err := time.ParseDuration(p.ClientDeactivateThreshold)
	if err != nil {
		return 0, ErrInvalidTimeDurationString
	}
	return clientDeactivateThreshold, nil
}

// GetAuthWebhookOptions returns the webhook options for the auth webhook.
func (p *Project) GetAuthWebhookOptions() (webhook.Options, error) {
	authWebhookMinWaitInterval, err := time.ParseDuration(p.AuthWebhookMinWaitInterval)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	authWebhookMaxWaitInterval, err := time.ParseDuration(p.AuthWebhookMaxWaitInterval)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	authWebhookRequestTimeout, err := time.ParseDuration(p.AuthWebhookRequestTimeout)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	return webhook.Options{
		MaxRetries:      p.AuthWebhookMaxRetries,
		MinWaitInterval: authWebhookMinWaitInterval,
		MaxWaitInterval: authWebhookMaxWaitInterval,
		RequestTimeout:  authWebhookRequestTimeout,
	}, nil
}

// GetEventWebhookOptions returns the webhook options for the event webhook.
func (p *Project) GetEventWebhookOptions() (webhook.Options, error) {
	eventWebhookMinWaitInterval, err := time.ParseDuration(p.EventWebhookMinWaitInterval)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	eventWebhookMaxWaitInterval, err := time.ParseDuration(p.EventWebhookMaxWaitInterval)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	eventWebhookRequestTimeout, err := time.ParseDuration(p.EventWebhookRequestTimeout)
	if err != nil {
		return webhook.Options{}, ErrInvalidTimeDurationString
	}
	return webhook.Options{
		MaxRetries:      p.EventWebhookMaxRetries,
		MinWaitInterval: eventWebhookMinWaitInterval,
		MaxWaitInterval: eventWebhookMaxWaitInterval,
		RequestTimeout:  eventWebhookRequestTimeout,
	}, nil
}
