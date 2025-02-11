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

// SendEvent send an event to project endpoint.
func SendEvent(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	docKey string,
	eventType events.DocEventType,
) error {
	eventWebhookType := eventType.WebhookType()
	if eventWebhookType == "" {
		return fmt.Errorf("invalid event webhook type")
	}

	if !prj.RequireEventWebhook(eventWebhookType) {
		return nil
	}

	req := types.EventWebhookRequest{
		Type: eventWebhookType,
		Attributes: types.EventWebhookAttribute{
			Key:      docKey,
			IssuedAt: gotime.Now().String(),
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal event webhook request: %w", err)
	}

	cacheKey := generateCacheKey(prj.PublicKey, docKey, string(eventType))
	if _, ok := be.EventWebhookCache.Get(cacheKey); ok {
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
		pkgtypes.Pair[int, *types.EventWebhookResponse]{First: status, Second: res},
		5*gotime.Second,
	)

	return nil
}

// generateCacheKey creates a unique key for caching webhook responses.
func generateCacheKey(publicKey string, docKey, event string) string {
	return fmt.Sprintf("%s:event:%s:%s", publicKey, docKey, event)
}
