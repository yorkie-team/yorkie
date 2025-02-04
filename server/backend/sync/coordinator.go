/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

// Package memory provides the memory implementation of the sync package.
package sync

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/locker"
)

type Coordinator struct {
	locks  *locker.Locker
	pubSub *PubSub
}

// NewCoordinator creates a new instance of Coordinator.
func NewCoordinator() *Coordinator {
	return &Coordinator{
		locks:      locker.New(),
		pubSub:     NewPubSub(),
	}
}

// NewLocker creates locker of the given key.
func (c *Coordinator) NewLocker(
	_ context.Context,
	key Key,
) (Locker, error) {
	return &internalLocker{
		key.String(),
		c.locks,
	}, nil
}

// Subscribe subscribes to the given documents.
func (c *Coordinator) Subscribe(
	ctx context.Context,
	subscriber *time.ActorID,
	documentRefKey types.DocRefKey,
) (*Subscription, []*time.ActorID, error) {
	sub, err := c.pubSub.Subscribe(ctx, subscriber, documentRefKey)
	if err != nil {
		return nil, nil, err
	}

	ids := c.pubSub.ClientIDs(documentRefKey)
	return sub, ids, nil
}

// Unsubscribe unsubscribes the given documents.
func (c *Coordinator) Unsubscribe(
	ctx context.Context,
	documentRefKey types.DocRefKey,
	sub *Subscription,
) error {
	c.pubSub.Unsubscribe(ctx, documentRefKey, sub)
	return nil
}

// Publish publishes the given event.
func (c *Coordinator) Publish(
	ctx context.Context,
	publisherID *time.ActorID,
	event DocEvent,
) {
	// NOTE(hackerwins): String() triggers the cache of ActorID to avoid
	// race condition of concurrent access to the cache.
	_ = event.Publisher.String()
	c.pubSub.Publish(ctx, publisherID, event)
}
