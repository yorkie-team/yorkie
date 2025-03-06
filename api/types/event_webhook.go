/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package types

import (
	"encoding/json"
	"fmt"
	"time"
)

// DateFormat defines the standard format used for timestamps.
const DateFormat = "2006-01-02T15:04:05.000Z"

// EventWebhookType represents event webhook type
type EventWebhookType string

const (
	// DocRootChanged is an event that indicates the document's content was modified.
	DocRootChanged EventWebhookType = "DocumentRootChanged"
)

// IsValidEventType checks whether the given event type is valid.
func IsValidEventType(eventType string) bool {
	return eventType == string(DocRootChanged)
}

// EventWebhookAttribute represents metadata associated with a webhook event.
type EventWebhookAttribute struct {
	Key      string `json:"key"`
	IssuedAt string `json:"issuedAt"`
}

// EventWebhookRequest represents a webhook event request payload.
type EventWebhookRequest struct {
	Type       EventWebhookType      `json:"type"`
	Attributes EventWebhookAttribute `json:"attributes"`
}

// NewRequestBody builds the JSON request body for a webhook event.
func NewRequestBody(docKey string, event EventWebhookType) ([]byte, error) {
	req := EventWebhookRequest{
		Type: event,
		Attributes: EventWebhookAttribute{
			Key:      docKey,
			IssuedAt: time.Now().UTC().Format(DateFormat),
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal event webhook request: %w", err)
	}
	return body, nil
}

// EventWebhookInfo holds the webhook EventRefKey and its associated Attribute.
type EventWebhookInfo struct {
	EventRefKey EventRefKey
	Attribute   WebhookAttribute
}

// WebhookAttribute defines attributes necessary for webhook handling.
type WebhookAttribute struct {
	SigningKey string
	URL        string
	DocKey     string
}
