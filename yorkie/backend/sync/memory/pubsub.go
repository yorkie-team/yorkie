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
	gosync "sync"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
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

// Map returns the internal map of this subscriptions.
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

// Len returns the length of this subscriptions.
func (s *subscriptions) Len() int {
	return len(s.internalMap)
}

// PubSub is the memory implementation of PubSub, used for single agent.
type PubSub struct {
	subscriptionsMapMu           *gosync.RWMutex
	subscriptionsMapBySubscriber map[string]*sync.Subscription
	subscriptionsMapByDocKey     map[string]*subscriptions
}

// NewPubSub creates an instance of PubSub.
func NewPubSub() *PubSub {
	return &PubSub{
		subscriptionsMapMu:           &gosync.RWMutex{},
		subscriptionsMapBySubscriber: make(map[string]*sync.Subscription),
		subscriptionsMapByDocKey:     make(map[string]*subscriptions),
	}
}

// Subscribe subscribes to the given document keys.
func (m *PubSub) Subscribe(
	subscriber types.Client,
	keys []*key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	if len(keys) == 0 {
		return nil, nil, sync.ErrEmptyDocKeys
	}

	log.Logger.Debugf(
		`Subscribe(%s,%s) Start`,
		keys[0].BSONKey(),
		subscriber.ID.String(),
	)

	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	sub := sync.NewSubscription(subscriber)
	m.subscriptionsMapBySubscriber[sub.SubscriberID()] = sub

	peersMap := make(map[string][]types.Client)
	for _, docKey := range keys {
		bsonKey := docKey.BSONKey()
		if _, ok := m.subscriptionsMapByDocKey[bsonKey]; !ok {
			m.subscriptionsMapByDocKey[bsonKey] = newSubscriptions()
		}
		m.subscriptionsMapByDocKey[bsonKey].Add(sub)

		var peers []types.Client
		for _, sub := range m.subscriptionsMapByDocKey[bsonKey].Map() {
			peers = append(peers, sub.Subscriber())
		}
		peersMap[bsonKey] = peers
	}

	log.Logger.Debugf(
		`Subscribe(%s,%s) End`,
		keys[0].BSONKey(),
		subscriber.ID.String(),
	)
	return sub, peersMap, nil
}

// Unsubscribe unsubscribes the given docKeys.
func (m *PubSub) Unsubscribe(
	docKeys []*key.Key,
	sub *sync.Subscription,
) {
	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	log.Logger.Debugf(
		`Unsubscribe(%s,%s) Start`,
		docKeys[0].BSONKey(),
		sub.SubscriberID(),
	)

	sub.Close()

	delete(m.subscriptionsMapBySubscriber, sub.SubscriberID())
	for _, docKey := range docKeys {
		k := docKey.BSONKey()
		if subs, ok := m.subscriptionsMapByDocKey[k]; ok {
			subs.Delete(sub.ID())

			if subs.Len() == 0 {
				delete(m.subscriptionsMapByDocKey, k)
			}
		}
	}

	log.Logger.Debugf(
		`Unsubscribe(%s,%s) End`,
		docKeys[0].BSONKey(),
		sub.SubscriberID(),
	)
}

// Publish publishes the given event.
func (m *PubSub) Publish(
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	m.subscriptionsMapMu.RLock()
	defer m.subscriptionsMapMu.RUnlock()

	for _, docKey := range event.DocumentKeys {
		k := docKey.BSONKey()

		log.Logger.Debugf(`Publish(%s,%s) Start`, k, publisherID.String())

		if subs, ok := m.subscriptionsMapByDocKey[k]; ok {
			for _, sub := range subs.Map() {
				if sub.Subscriber().ID.Compare(publisherID) == 0 {
					continue
				}

				log.Logger.Debugf(
					`Publish(%s,%s) to %s`,
					k,
					publisherID.String(),
					sub.SubscriberID(),
				)
				sub.Events() <- event
			}
		}
		log.Logger.Debugf(`Publish(%s,%s) End`, k, publisherID.String())
	}
}

// UpdateMetadata updates the metadata of the given client.
func (m *PubSub) UpdateMetadata(
	publisher *types.Client,
	keys []*key.Key,
) {
	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	if sub, ok := m.subscriptionsMapBySubscriber[publisher.ID.String()]; ok {
		sub.UpdateMetadata(publisher.MetadataInfo)
	}
}
