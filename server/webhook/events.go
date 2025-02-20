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

// Package webhook provides publishing events to project endpoint.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/backend"
)

var (
	// ErrUnexpectedStatusCode is returned when the webhook returns an unexpected status code.
	ErrUnexpectedStatusCode = errors.New("unexpected status code from webhook")
)

// SendEvent sends an event to the project's event webhook endpoint.
func SendEvent(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	docKey string,
	eventType events.DocEventType,
) error {
	webhookType := eventType.WebhookType()
	if webhookType == "" {
		return fmt.Errorf("invalid event webhook type: %s", eventType)
	}

	if !prj.RequireEventWebhook(webhookType) {
		return nil
	}

	body, err := buildRequestBody(docKey, webhookType)
	if err != nil {
		return fmt.Errorf("marshal event webhook request: %w", err)
	}

	_, status, err := be.EventWebhookClient.Send(
		ctx,
		prj.EventWebhookURL,
		prj.SecretKey,
		body,
	)
	if err != nil {
		return fmt.Errorf("send event webhook: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("send event webhook %d: %w", status, ErrUnexpectedStatusCode)
	}

	return nil
}

// buildRequestBody builds the request body for the event webhook.
func buildRequestBody(docKey string, webhookType types.EventWebhookType) ([]byte, error) {
	req := types.EventWebhookRequest{
		Type: webhookType,
		Attributes: types.EventWebhookAttribute{
			Key:      docKey,
			IssuedAt: gotime.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal event webhook request: %w", err)
	}

	return body, nil
}
