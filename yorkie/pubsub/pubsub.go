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
	"github.com/yorkie-team/yorkie/pkg/types"
)

type DocEvent struct {
	Type    types.EventType
	DocKey  string
	ActorID *time.ActorID
}

type SubscriptionID string

type Subscription struct {
	id     string
	actor  *time.ActorID
	events chan DocEvent
}

func newSubscription(actor *time.ActorID) *Subscription {
	return &Subscription{
		id:     uuid.New().String(),
		actor:  actor,
		events: make(chan DocEvent),
	}
}

func (s *Subscription) Events() <-chan DocEvent {
	return s.events
}

func (s *Subscription) Actor() *time.ActorID {
	return s.actor
}

type Topic string

type Subscriptions map[string]*Subscription

// PubSub is a structure to support event publishing/subscription.
// TODO: Temporary Memory PubSub.
//  - We will need to replace this with distributed pubSub.
type PubSub struct {
	mu               *sync.RWMutex
	subscriptionsMap map[string]Subscriptions
}

func New() *PubSub {
	return &PubSub{
		mu:               &sync.RWMutex{},
		subscriptionsMap: make(map[string]Subscriptions),
	}
}

// Subscribe subscribes to the given topics.
func (m *PubSub) Subscribe(
	actor *time.ActorID,
	topics []string,
) (*Subscription, map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subscription := newSubscription(actor)
	peersMap := make(map[string][]string)

	for _, topic := range topics {
		if _, ok := m.subscriptionsMap[topic]; !ok {
			m.subscriptionsMap[topic] = make(Subscriptions)
		}
		m.subscriptionsMap[topic][subscription.id] = subscription

		var peers []string
		for _, subscription := range m.subscriptionsMap[topic] {
			peers = append(peers, subscription.actor.String())
		}
		peersMap[topic] = peers
	}

	return subscription, peersMap, nil
}

// Unsubscribe unsubscribes the given topics.
func (m *PubSub) Unsubscribe(topics []string, subscription *Subscription) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, topic := range topics {
		if subscriptions, ok := m.subscriptionsMap[topic]; ok {
			delete(subscriptions, subscription.id)
		}
	}
}

// Publish publishes the given event to the given Topic.
func (m *PubSub) Publish(actor *time.ActorID, topic string, event DocEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if subscriptions, ok := m.subscriptionsMap[topic]; ok {
		for _, subscription := range subscriptions {
			if subscription.actor.Compare(actor) != 0 {
				subscription.events <- event
			}
		}
	}
}
