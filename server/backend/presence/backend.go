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

// Package presence provides presence management interfaces.
package presence

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// Backend represents the interface for presence backend operations.
type Backend interface {
	// Attach adds a client to a presence counter.
	Attach(ctx context.Context, clientID time.ActorID, presenceKey key.Key) (presenceID string, count int64, err error)

	// Detach removes a client from a presence counter.
	Detach(ctx context.Context, clientID time.ActorID, presenceKey key.Key) (count int64, err error)

	// GetCount returns the current count for a presence key.
	GetCount(presenceKey key.Key) int64

	// Subscribe creates a subscription for count updates.
	Subscribe(presenceKey key.Key, subscriberID string) <-chan CountUpdate

	// Unsubscribe removes a subscription.
	Unsubscribe(presenceKey key.Key, subscriberID string)
}
