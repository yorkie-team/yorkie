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

package pubsub

import (
	"sync"
	gotime "time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// PresenceSubscriptions is a map of PresenceSubscriptions.
type PresenceSubscriptions struct {
	refKey      types.PresenceRefKey
	internalMap *cmap.Map[string, *PresenceSubscription]
	publisher   *PresencePublisher
}

func newPresenceSubscriptions(refKey types.PresenceRefKey) *PresenceSubscriptions {
	s := &PresenceSubscriptions{
		refKey:      refKey,
		internalMap: cmap.New[string, *PresenceSubscription](),
	}
	s.publisher = NewPresenceBatchPublisher(s, 100*gotime.Millisecond)
	return s
}

// Set adds the given subscription.
func (s *PresenceSubscriptions) Set(sub *PresenceSubscription) {
	s.internalMap.Set(sub.ID(), sub)
}

// Values returns the values of these subscriptions.
func (s *PresenceSubscriptions) Values() []*PresenceSubscription {
	return s.internalMap.Values()
}

// Publish publishes the given event.
func (s *PresenceSubscriptions) Publish(event events.PresenceEvent) {
	s.publisher.Publish(event)
}

// Delete deletes the subscription of the given id.
func (s *PresenceSubscriptions) Delete(id string) {
	s.internalMap.Delete(id, func(sub *PresenceSubscription, exists bool) bool {
		if exists {
			sub.Close()
		}
		return exists
	})
}

// Len returns the length of these subscriptions.
func (s *PresenceSubscriptions) Len() int {
	return s.internalMap.Len()
}

// Close closes the subscriptions.
func (s *PresenceSubscriptions) Close() {
	s.publisher.Close()
}

// PresenceSubscription represents a subscription of a subscriber to presence events.
type PresenceSubscription struct {
	id         string
	subscriber time.ActorID
	mu         sync.Mutex
	closed     bool
	events     chan events.PresenceEvent
}

// NewPresenceSubscription creates a new instance of PresenceSubscription.
func NewPresenceSubscription(subscriber time.ActorID) *PresenceSubscription {
	return &PresenceSubscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan events.PresenceEvent, 10),
		closed:     false,
	}
}

// ID returns the id of this subscription.
func (s *PresenceSubscription) ID() string {
	return s.id
}

// Events returns the PresenceEvent channel of this subscription.
func (s *PresenceSubscription) Events() chan events.PresenceEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *PresenceSubscription) Subscriber() time.ActorID {
	return s.subscriber
}

// Close closes all resources of this PresenceSubscription.
func (s *PresenceSubscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.events)
	}
}

// Publish publishes the given event to the subscriber.
func (s *PresenceSubscription) Publish(event events.PresenceEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	// NOTE(hackerwins): When a subscription is being closed by a subscriber,
	// the subscriber may not receive messages.
	select {
	case s.Events() <- event:
		return true
	case <-gotime.After(100 * gotime.Millisecond):
		return false
	}
}
