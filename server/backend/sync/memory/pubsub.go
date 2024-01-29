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

package memory

import (
	"context"
	gosync "sync"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// subscriptions is a map of subscriptions.
type subscriptions struct {
	internalMap map[string]*sync.Subscription
}

func newSubscriptions() *subscriptions {
	return &subscriptions{
		internalMap: make(map[string]*sync.Subscription),
	}
}

// Add adds the given subscription.
func (s *subscriptions) Add(sub *sync.Subscription) {
	s.internalMap[sub.ID()] = sub
}

// Map returns the internal map of these subscriptions.
func (s *subscriptions) Map() map[string]*sync.Subscription {
	return s.internalMap
}

// Delete deletes the subscription of the given id.
func (s *subscriptions) Delete(id string) {
	if subscription, ok := s.internalMap[id]; ok {
		subscription.Close()
	}
	delete(s.internalMap, id)
}

// Len returns the length of these subscriptions.
func (s *subscriptions) Len() int {
	return len(s.internalMap)
}

// PubSub is the memory implementation of PubSub, used for single server.
type PubSub struct {
	subscriptionsMapMu          *gosync.RWMutex
	subscriptionsMapByDocRefKey map[types.DocRefKey]*subscriptions
}

// NewPubSub creates an instance of PubSub.
func NewPubSub() *PubSub {
	return &PubSub{
		subscriptionsMapMu:          &gosync.RWMutex{},
		subscriptionsMapByDocRefKey: make(map[types.DocRefKey]*subscriptions),
	}
}

// Subscribe subscribes to the given document keys.
func (m *PubSub) Subscribe(
	ctx context.Context,
	subscriber *time.ActorID,
	documentRefKey types.DocRefKey,
) (*sync.Subscription, error) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Subscribe(%s,%s) Start`,
			documentRefKey,
			subscriber,
		)
	}

	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	sub := sync.NewSubscription(subscriber)
	if _, ok := m.subscriptionsMapByDocRefKey[documentRefKey]; !ok {
		m.subscriptionsMapByDocRefKey[documentRefKey] = newSubscriptions()
	}
	m.subscriptionsMapByDocRefKey[documentRefKey].Add(sub)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Subscribe(%s,%s) End`,
			documentRefKey,
			subscriber,
		)
	}
	return sub, nil
}

// Unsubscribe unsubscribes the given docKeys.
func (m *PubSub) Unsubscribe(
	ctx context.Context,
	documentRefKey types.DocRefKey,
	sub *sync.Subscription,
) {
	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Unsubscribe(%s,%s) Start`,
			documentRefKey,
			sub.Subscriber(),
		)
	}

	sub.Close()

	if subs, ok := m.subscriptionsMapByDocRefKey[documentRefKey]; ok {
		subs.Delete(sub.ID())

		if subs.Len() == 0 {
			delete(m.subscriptionsMapByDocRefKey, documentRefKey)
		}
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Unsubscribe(%s,%s) End`,
			documentRefKey,
			sub.Subscriber(),
		)
	}
}

// Publish publishes the given event.
func (m *PubSub) Publish(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	m.subscriptionsMapMu.RLock()
	defer m.subscriptionsMapMu.RUnlock()

	documentRefKey := event.DocumentRefKey
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) Start`,
			documentRefKey,
			publisherID)
	}

	if subs, ok := m.subscriptionsMapByDocRefKey[documentRefKey]; ok {
		for _, sub := range subs.Map() {
			if sub.Subscriber().Compare(publisherID) == 0 {
				continue
			}

			if logging.Enabled(zap.DebugLevel) {
				logging.From(ctx).Debugf(
					`Publish %s(%s,%s) to %s`,
					event.Type,
					documentRefKey,
					publisherID,
					sub.Subscriber(),
				)
			}

			// NOTE: When a subscription is being closed by a subscriber,
			// the subscriber may not receive messages.
			select {
			case sub.Events() <- event:
			case <-gotime.After(100 * gotime.Millisecond):
				logging.From(ctx).Warnf(
					`Publish(%s,%s) to %s timeout`,
					documentRefKey,
					publisherID,
					sub.Subscriber(),
				)
			}
		}
	}
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) End`,
			documentRefKey,
			publisherID)
	}
}

// ClientIDs returns the clients of the given document.
func (m *PubSub) ClientIDs(documentRefKey types.DocRefKey) []*time.ActorID {
	m.subscriptionsMapMu.RLock()
	defer m.subscriptionsMapMu.RUnlock()

	var ids []*time.ActorID
	for _, sub := range m.subscriptionsMapByDocRefKey[documentRefKey].Map() {
		ids = append(ids, sub.Subscriber())
	}
	return ids
}
