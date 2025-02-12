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
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/server/backend"
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
		return fmt.Errorf("invalid event webhook type")
	}

	if !prj.RequireEventWebhook(webhookType) {
		return nil
	}

	body, err := buildEventWebhookBody(docKey, webhookType)
	if err != nil {
		return fmt.Errorf("marshal event webhook request: %w", err)
	}

	cacheKey := generateEventCacheKey(prj.PublicKey, docKey, string(webhookType))
	if _, found := be.EventWebhookCache.Get(cacheKey); found {
		return nil
	}

	res, status, err := be.EventWebhookClient.Send(
		ctx,
		prj.EventWebhookURL,
		prj.SecretKey,
		body,
	)
	if err != nil {
		return fmt.Errorf("send event webhook: %w", err)
	}

	be.EventWebhookCache.Add(
		cacheKey,
		pkgtypes.Pair[int, *types.EventWebhookResponse]{
			First:  status,
			Second: res,
		},
		be.Config.ParseEventWebhookCacheTTL(),
	)

	return nil
}

// buildEventWebhookBody creates a new EventWebhookRequest given a document key and webhook type.
func buildEventWebhookBody(docKey string, webhookType types.EventWebhookType) ([]byte, error) {
	req := types.EventWebhookRequest{
		Type: webhookType,
		Attributes: types.EventWebhookAttribute{
			Key:      docKey,
			IssuedAt: gotime.Now().Format(gotime.RFC3339),
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal event webhook request: %w", err)
	}

	return body, nil
}

// generateEventCacheKey creates a unique cache key for an event webhook.
func generateEventCacheKey(publicKey, docKey, webhookType string) string {
	return fmt.Sprintf("%s:event:%s:%s", publicKey, docKey, webhookType)
}
