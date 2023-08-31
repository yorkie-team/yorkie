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

	locks           *locker.Locker
	docPubSub       *PubSub[*sync.DocEvent]
	broadcastPubSub *PubSub[*sync.BroadcastEvent]
}

// NewCoordinator creates an instance of Coordinator.
func NewCoordinator(serverInfo *sync.ServerInfo) *Coordinator {
	return &Coordinator{
		serverInfo:      serverInfo,
		locks:           locker.New(),
		docPubSub:       NewPubSub[*sync.DocEvent](),
		broadcastPubSub: NewPubSub[*sync.BroadcastEvent](),
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
func (c *Coordinator) SubscribeDocEvent(
	ctx context.Context,
	subscriber *time.ActorID,
	documentID types.ID,
) (*sync.Subscription[*sync.DocEvent], []*time.ActorID, error) {
	sub, err := c.docPubSub.SubscribeDoc(ctx, subscriber, documentID)
	if err != nil {
		return nil, nil, err
	}

	c.docPubSub.SubscribeEvent(ctx, documentID, "document", sub.ID())

	ids := c.docPubSub.ClientIDs(documentID)
	return sub, ids, nil
}

// Unsubscribe unsubscribes the given documents.
func (c *Coordinator) UnsubscribeDocEvent(
	ctx context.Context,
	documentID types.ID,
	sub *sync.Subscription[*sync.DocEvent],
) error {
	c.docPubSub.UnsubscribeDoc(ctx, documentID, sub)
	return nil
}

// Publish publishes the given event.
func (c *Coordinator) PublishDocEvent(
	ctx context.Context,
	publisherID *time.ActorID,
	event *sync.DocEvent,
) {
	c.docPubSub.Publish(ctx, event.DocumentID, "document", publisherID, event)
}

// Subscribe subscribes to the given documents.
func (c *Coordinator) SubscribeBroadcasts(
	ctx context.Context,
	subscriber *time.ActorID,
	documentID types.ID,
) (*sync.Subscription[*sync.BroadcastEvent], error) {
	sub, err := c.broadcastPubSub.SubscribeDoc(ctx, subscriber, documentID)
	if err != nil {
		return nil, err
	}

	return sub, nil
}

// Unsubscribe unsubscribes the given documents.
func (c *Coordinator) UnsubscribeBroadcasts(
	ctx context.Context,
	documentID types.ID,
	sub *sync.Subscription[*sync.BroadcastEvent],
) error {
	c.broadcastPubSub.UnsubscribeDoc(ctx, documentID, sub)
	return nil
}

func (c *Coordinator) SubscribeBroadcastEvent(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	subID string,
) {
	c.broadcastPubSub.SubscribeEvent(ctx, documentID, eventType, subID)
}

func (c *Coordinator) UnsubscribeBroadcastEvent(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	subID string,
) {
	c.broadcastPubSub.UnsubscribeEvent(ctx, documentID, eventType, subID)
}

// Publish publishes the given event.
func (c *Coordinator) PublishBroadcastEvent(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	publisherID *time.ActorID,
	event *sync.BroadcastEvent,
) {
	c.broadcastPubSub.Publish(ctx, documentID, eventType, publisherID, event)
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
