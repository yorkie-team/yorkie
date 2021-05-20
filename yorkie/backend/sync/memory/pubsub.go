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

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// subscriptions is a collection of subscriptions that subscribe to a specific
// topic.
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

// PubSub is the memory implementation of PubSub, used for single agent or
// tests.
type PubSub struct {
	AgentInfo               *sync.AgentInfo
	subscriptionsMapMu      *gosync.RWMutex
	subscriptionsMapByTopic map[string]*subscriptions
}

// NewPubSub creates an instance of PubSub.
func NewPubSub(info *sync.AgentInfo) *PubSub {
	return &PubSub{
		AgentInfo:               info,
		subscriptionsMapMu:      &gosync.RWMutex{},
		subscriptionsMapByTopic: make(map[string]*subscriptions),
	}
}

// Subscribe subscribes to the given topics.
func (m *PubSub) Subscribe(
	subscriber types.Client,
	topics []string,
) (*sync.Subscription, map[string][]types.Client, error) {
	if len(topics) == 0 {
		return nil, nil, sync.ErrEmptyTopics
	}

	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	log.Logger.Debugf(
		`Subscribe(%s,%s) Start`,
		topics[0],
		subscriber.ID.String(),
	)

	sub := sync.NewSubscription(subscriber)
	peersMap := make(map[string][]types.Client)

	for _, topic := range topics {
		if _, ok := m.subscriptionsMapByTopic[topic]; !ok {
			m.subscriptionsMapByTopic[topic] = newSubscriptions()
		}
		m.subscriptionsMapByTopic[topic].Add(sub)

		var peers []types.Client
		for _, sub := range m.subscriptionsMapByTopic[topic].Map() {
			peers = append(peers, sub.Subscriber())
		}
		peersMap[topic] = peers
	}

	log.Logger.Debugf(
		`Subscribe(%s,%s) End`,
		topics[0],
		subscriber.ID.String(),
	)
	return sub, peersMap, nil
}

// Unsubscribe unsubscribes the given topics.
func (m *PubSub) Unsubscribe(topics []string, sub *sync.Subscription) {
	m.subscriptionsMapMu.Lock()
	defer m.subscriptionsMapMu.Unlock()

	log.Logger.Debugf(`Unsubscribe(%s,%s) Start`, topics[0], sub.SubscriberID())

	for _, topic := range topics {
		if subs, ok := m.subscriptionsMapByTopic[topic]; ok {
			subs.Delete(sub.ID())

			if subs.Len() == 0 {
				delete(m.subscriptionsMapByTopic, topic)
			}
		}
	}
	sub.Close()

	log.Logger.Debugf(`Unsubscribe(%s,%s) End`, topics[0], sub.SubscriberID())
}

// Publish publishes the given event to the given Topic.
func (m *PubSub) Publish(
	publisherID *time.ActorID,
	topic string,
	event sync.DocEvent,
) {
	m.subscriptionsMapMu.RLock()
	defer m.subscriptionsMapMu.RUnlock()

	log.Logger.Debugf(`Publish(%s,%s) Start`, event.DocKey, publisherID.String())

	if subs, ok := m.subscriptionsMapByTopic[topic]; ok {
		for _, sub := range subs.Map() {
			if sub.Subscriber().ID.Compare(publisherID) != 0 {
				log.Logger.Debugf(
					`Publish(%s,%s) to %s`,
					event.DocKey,
					publisherID.String(),
					sub.SubscriberID(),
				)
				sub.Events() <- event
			}
		}
	}

	log.Logger.Debugf(`Publish(%s,%s) End`, event.DocKey, publisherID.String())
}

// Members returns the members of this cluster.
func (m *PubSub) Members() map[string]*sync.AgentInfo {
	members := make(map[string]*sync.AgentInfo)
	members[m.AgentInfo.ID] = m.AgentInfo
	return members
}
