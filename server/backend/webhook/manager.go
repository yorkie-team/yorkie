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
	"fmt"
	"net/http"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/limit"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	// TODO(window9u): Consider making this parameter configurable via CLI.
	expireInterval  = 100 * time.Millisecond
	throttleWindow  = 1 * time.Second
	debouncingTime  = 1 * time.Second
	expireBatchSize = 100
)

// Manager manages sending webhook events with rate limiting.
type Manager struct {
	limiter       *limit.Limiter[types.EventRefKey]
	webhookClient *webhook.Client[types.EventWebhookRequest, int]
	db            database.Database
}

// NewManager creates a new instance of Manager with the provided webhook client.
func NewManager(cli *webhook.Client[types.EventWebhookRequest, int], db database.Database) *Manager {
	return &Manager{
		limiter:       limit.NewLimiter[types.EventRefKey](expireBatchSize, expireInterval, throttleWindow, debouncingTime),
		webhookClient: cli,
		db:            db,
	}
}

// Send dispatches a webhook event for the specified document and event reference key.
// It uses rate limiting to debounce multiple events within a short period.
func (m *Manager) Send(ctx context.Context, info types.EventWebhookInfo) error {
	callback := func() {
		if err := SendWebhook(
			ctx,
			m.webhookClient,
			info.EventRefKey.EventWebhookType,
			info.Attribute,
			info.EventRefKey.ProjectID,
			m.db,
			info.Options,
		); err != nil {
			logging.From(ctx).Error(err)
		}
	}

	// If allowed immediately, invoke the callback.
	if allowed := m.limiter.Allow(info.EventRefKey, callback); allowed {
		return SendWebhook(
			ctx,
			m.webhookClient,
			info.EventRefKey.EventWebhookType,
			info.Attribute,
			info.EventRefKey.ProjectID,
			m.db,
			info.Options,
		)
	}
	return nil
}

// Close closes the event webhook manager. This will wait for flushing remain debouncing events
func (m *Manager) Close() {
	m.limiter.Close()

	// NOTE(hackerwins): It is not good to close the webhook client here
	// because it is created from outside.
	m.webhookClient.Close()
}

// SendWebhook sends the webhook event using the provided client.
// It builds the request body and checks for a successful HTTP response.
func SendWebhook(
	ctx context.Context,
	cli *webhook.Client[types.EventWebhookRequest, int],
	event types.EventWebhookType,
	attr types.WebhookAttribute,
	projectID types.ID,
	db database.Database,
	options webhook.Options,
) error {
	body, err := types.NewRequestBody(attr.DocKey, event)
	if err != nil {
		return fmt.Errorf("create webhook request body: %w", err)
	}

	_, status, responseBody, err := cli.Send(ctx, attr.URL, attr.SigningKey, body, options)
	if (err != nil || status != http.StatusOK) && db != nil {
		errorMessage := ""
		if err != nil {
			errorMessage = err.Error()
		}

		webhookLog := &types.WebhookLogInfo{
			ProjectID:    projectID,
			WebhookType:  "event",
			WebhookURL:   attr.URL,
			RequestBody:  body,
			StatusCode:   status,
			ResponseBody: responseBody,
			ErrorMessage: errorMessage,
			CreatedAt:    time.Now().UTC(),
		}
		if logErr := db.CreateWebhookLog(ctx, webhookLog); logErr != nil {
			logging.From(ctx).Error(logErr)
		}
	}

	if err != nil {
		return fmt.Errorf("send webhook event: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("webhook returned status %d: %w", status, webhook.ErrUnexpectedStatusCode)
	}
	return nil
}
