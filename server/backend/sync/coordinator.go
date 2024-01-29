/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package sync provides the synchronization primitives for the server.
package sync

import (
	"context"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ServerInfo represents the information of the Server.
type ServerInfo struct {
	ID        string      `json:"id"`
	Hostname  string      `json:"hostname"`
	UpdatedAt gotime.Time `json:"updated_at"`
}

// Coordinator provides synchronization functions such as locks and event Pub/Sub.
type Coordinator interface {
	// NewLocker creates a sync.Locker.
	NewLocker(ctx context.Context, key Key) (Locker, error)

	// Subscribe subscribes to the given documents.
	Subscribe(
		ctx context.Context,
		subscriber *time.ActorID,
		documentRefKey types.DocRefKey,
	) (*Subscription, []*time.ActorID, error)

	// Unsubscribe unsubscribes from the given documents.
	Unsubscribe(
		ctx context.Context,
		documentRefKey types.DocRefKey,
		sub *Subscription,
	) error

	// Publish publishes the given event.
	Publish(ctx context.Context, publisherID *time.ActorID, event DocEvent)

	// Members returns the members of this cluster.
	Members() map[string]*ServerInfo

	// Close closes all resources of this Coordinator.
	Close() error
}
