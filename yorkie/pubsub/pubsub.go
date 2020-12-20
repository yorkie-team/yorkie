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

package pubsub

import (
	"sync"

	"github.com/google/uuid"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/types"
)

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type      types.EventType
	DocKey    string
	Publisher *time.ActorID
}

// Subscription represents the subscription of a subscriber. It is used across
// several topics.
type Subscription struct {
	id         string
	subscriber *time.ActorID
	closed     bool
	events     chan DocEvent
}

func newSubscription(subscriber *time.ActorID) *Subscription {
	return &Subscription{
		id:         uuid.New().String(),
		subscriber: subscriber,
		// [Workaround] The channel buffer size below avoids stopping during
		//   event issuing to the events channel. This bug occurs in the order
		//   of Publish and Unsubscribe.
		events: make(chan DocEvent, 10),
	}
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription) Events() <-chan DocEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription) Subscriber() *time.ActorID {
	return s.subscriber
}

// SubscriberID returns string representation of the subscriber.
func (s *Subscription) SubscriberID() string {
	return s.subscriber.String()
}

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}

// Subscriptions is a collection of subscriptions that subscribe to a specific
// topic.
type Subscriptions struct {
	internalMap map[string]*Subscription
}

func newSubscriptions() *Subscriptions {
	return &Subscriptions{
		internalMap: make(map[string]*Subscription),
	}
}

// Add adds the given subscription.
func (s *Subscriptions) Add(sub *Subscription) {
	s.internalMap[sub.id] = sub
}

// Map returns the internal map of this Subscriptions.
func (s *Subscriptions) Map() map[string]*Subscription {
	return s.internalMap
}

// Delete deletes the subscription of the given id.
func (s *Subscriptions) Delete(id string) {
	if subscription, ok := s.internalMap[id]; ok {
		subscription.Close()
	}
	delete(s.internalMap, id)
}

// PubSub is a structure to support event publishing/subscription.
// TODO: Temporary Memory PubSub.
//  - We will need to replace this with distributed pubSub.
type PubSub struct {
	mu               *sync.RWMutex
	subscriptionsMap map[string]*Subscriptions
}

// New creates an instance of Pubsub.
func New() *PubSub {
	return &PubSub{
		mu:               &sync.RWMutex{},
		subscriptionsMap: make(map[string]*Subscriptions),
	}
}

// Subscribe subscribes to the given topics.
func (m *PubSub) Subscribe(
	subscriber *time.ActorID,
	topics []string,
) (*Subscription, map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Logger.Debugf(`Subscribe(%s,%s) Start`, topics[0], subscriber.String())

	subscription := newSubscription(subscriber)
	peersMap := make(map[string][]string)

	for _, topic := range topics {
		if _, ok := m.subscriptionsMap[topic]; !ok {
			m.subscriptionsMap[topic] = newSubscriptions()
		}
		m.subscriptionsMap[topic].Add(subscription)

		var peers []string
		for _, sub := range m.subscriptionsMap[topic].Map() {
			peers = append(peers, sub.subscriber.String())
		}
		peersMap[topic] = peers
	}

	log.Logger.Debugf(`Subscribe(%s,%s) End`, topics[0], subscriber.String())
	return subscription, peersMap, nil
}

// Unsubscribe unsubscribes the given topics.
func (m *PubSub) Unsubscribe(topics []string, sub *Subscription) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Logger.Debugf(`Unsubscribe(%s,%s) Start`, topics[0], sub.SubscriberID())

	for _, topic := range topics {
		if subscriptions, ok := m.subscriptionsMap[topic]; ok {
			subscriptions.Delete(sub.id)
		}
	}
	log.Logger.Debugf(`Unsubscribe(%s,%s) End`, topics[0], sub.SubscriberID())
}

// Publish publishes the given event to the given Topic.
func (m *PubSub) Publish(
	publisher *time.ActorID,
	topic string,
	event DocEvent,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	log.Logger.Debugf(`Publish(%s,%s) Start`, event.DocKey, publisher.String())

	if subscriptions, ok := m.subscriptionsMap[topic]; ok {
		for _, sub := range subscriptions.Map() {
			if sub.subscriber.Compare(publisher) != 0 {
				log.Logger.Debugf(
					`Publish(%s,%s) to %s`,
					event.DocKey,
					publisher.String(),
					sub.SubscriberID(),
				)
				sub.events <- event
			}
		}
	}

	log.Logger.Debugf(`Publish(%s,%s) End`, event.DocKey, publisher.String())
}
