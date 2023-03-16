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
package memory

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/locker"
	"github.com/yorkie-team/yorkie/server/backend/sync"
)

// Coordinator is a memory-based implementation of sync.Coordinator.
type Coordinator struct {
	serverInfo *sync.ServerInfo

	locks  *locker.Locker
	pubSub *PubSub
}

// NewCoordinator creates an instance of Coordinator.
func NewCoordinator(serverInfo *sync.ServerInfo) *Coordinator {
	return &Coordinator{
		serverInfo: serverInfo,
		locks:      locker.New(),
		pubSub:     NewPubSub(),
	}
}

// NewLocker creates locker of the given key.
func (c *Coordinator) NewLocker(
	ctx context.Context,
	key sync.Key,
) (sync.Locker, error) {
	return &internalLocker{
		key.String(),
		c.locks,
	}, nil
}

// Subscribe subscribes to the given documents.
func (c *Coordinator) Subscribe(
	ctx context.Context,
	subscriber types.Client,
	documentID types.ID,
) (*sync.Subscription, []types.Client, error) {
	sub, err := c.pubSub.Subscribe(ctx, subscriber, documentID)
	if err != nil {
		return nil, nil, err
	}

	var peers []types.Client
	for _, sub := range c.pubSub.subscriptionsMapByDocID[documentID].Map() {
		peers = append(peers, sub.Subscriber())
	}
	return sub, peers, nil
}

// Unsubscribe unsubscribes the given documents.
func (c *Coordinator) Unsubscribe(
	ctx context.Context,
	documentID types.ID,
	sub *sync.Subscription,
) error {
	c.pubSub.Unsubscribe(ctx, documentID, sub)
	return nil
}

// Publish publishes the given event.
func (c *Coordinator) Publish(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.pubSub.Publish(ctx, publisherID, event)
}

// PublishToLocal publishes the given event.
func (c *Coordinator) PublishToLocal(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.pubSub.Publish(ctx, publisherID, event)
}

// UpdatePresence updates the presence of the given client.
func (c *Coordinator) UpdatePresence(
	_ context.Context,
	publisher *types.Client,
	documentID types.ID,
) error {
	c.pubSub.UpdatePresence(publisher, documentID)
	return nil
}

// Members returns the members of this cluster.
func (c *Coordinator) Members() map[string]*sync.ServerInfo {
	members := make(map[string]*sync.ServerInfo)
	members[c.serverInfo.ID] = c.serverInfo
	return members
}

// Close closes all resources of this Coordinator.
func (c *Coordinator) Close() error {
	return nil
}
