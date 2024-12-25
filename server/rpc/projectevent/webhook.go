/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

// Package projectevent provides the webhook event functions for the project.
package projectevent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
)

// DocumentCreated sends a webhook event when a document is created.
func DocumentCreated(ctx context.Context, be *backend.Backend, documentKey, clientKey string) {
	project := projects.From(ctx)
	if !project.RequireEventWebhook(types.DocumentCreated) {
		return
	}

	be.Background.AttachGoroutine(func(ctx context.Context) {
		reqBody, err := json.Marshal(types.WebhookRequest{
			Type: types.DocumentCreated,
			Attributes: types.WebhookAttribute{
				DocumentKey: documentKey,
				ClientKey:   clientKey,
				IssuedAt:    gotime.Now().String(),
			},
		})
		if err != nil {
			logging.From(ctx).Error(err)
			return
		}

		if err = sendWebhookEvent(ctx, be.Config, reqBody, project.SecretKey, project.EventWebhookURL); err != nil {
			logging.From(ctx).Error(err)
			//NOTE(window9u): I think we could send failed event to Yorkie dashboard server, so failed events could
			// be recovered and resent.
		}
	}, "webhook")
}

// DocumentRemoved sends a webhook event when a document is removed.
func DocumentRemoved(ctx context.Context, be *backend.Backend, documentKey, clientKey string) {
	project := projects.From(ctx)
	if !project.RequireEventWebhook(types.DocumentRemoved) {
		return
	}

	be.Background.AttachGoroutine(func(ctx context.Context) {
		reqBody, err := json.Marshal(types.WebhookRequest{
			Type: types.DocumentRemoved,
			Attributes: types.WebhookAttribute{
				DocumentKey: documentKey,
				ClientKey:   clientKey,
				IssuedAt:    gotime.Now().String(),
			},
		})
		if err != nil {
			logging.From(ctx).Error(err)
			return
		}

		if err = sendWebhookEvent(ctx, be.Config, reqBody, project.SecretKey, project.EventWebhookURL); err != nil {
			logging.From(ctx).Error(err)
		}
	}, "webhook")
}

// sendWebhookEvent sends the given request body to the provided webhook URL
// using a HMAC-based client, then retries with exponential backoff
// on transient errors or specific status codes.
func sendWebhookEvent(ctx context.Context, config *backend.Config, reqBody []byte, secretKey, endpoint string) error {
	client := webhook.NewClient(config.ParseProjectWebhookTimeout(), secretKey)

	return webhook.WithExponentialBackoff(
		ctx,
		config.EventWebhookMaxRetries,
		config.ParseProjectWebhookBaseWaitInterval(),
		config.ParseProjectWebhookMaxWaitInterval(),
		func() (int, error) {
			resp, err := client.Post(
				endpoint,
				"application/json",
				bytes.NewBuffer(reqBody),
			)
			if err != nil {
				return 0, fmt.Errorf("post to webhook: %w", err)
			}

			defer func() {
				if err = resp.Body.Close(); err != nil {
					logging.From(ctx).Error(err)
				}
			}()

			if resp.StatusCode != http.StatusOK &&
				resp.StatusCode != http.StatusUnauthorized &&
				resp.StatusCode != http.StatusForbidden {
				return resp.StatusCode, webhook.ErrUnexpectedStatusCode
			}

			return resp.StatusCode, nil
		})
}
