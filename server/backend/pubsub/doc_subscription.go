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

// DocSubscriptions is a map of DocSubscriptions.
type DocSubscriptions struct {
	docKey      types.DocRefKey
	internalMap *cmap.Map[string, *DocSubscription]
	publisher   *DocPublisher
}

func newSubscriptions(docKey types.DocRefKey) *DocSubscriptions {
	s := &DocSubscriptions{
		docKey:      docKey,
		internalMap: cmap.New[string, *DocSubscription](),
	}
	s.publisher = NewDocPublisher(s, 100*gotime.Millisecond)
	return s
}

// Set adds the given subscription.
func (s *DocSubscriptions) Set(sub *DocSubscription) {
	s.internalMap.Set(sub.ID(), sub)
}

// Values returns the values of these subscriptions.
func (s *DocSubscriptions) Values() []*DocSubscription {
	return s.internalMap.Values()
}

// Publish publishes the given event.
func (s *DocSubscriptions) Publish(event events.DocEvent) {
	s.publisher.Publish(event)
}

// Delete deletes the subscription of the given id.
func (s *DocSubscriptions) Delete(id string) {
	s.internalMap.Delete(id, func(sub *DocSubscription, exists bool) bool {
		if exists {
			sub.Close()
		}
		return exists
	})
}

// Len returns the length of these subscriptions.
func (s *DocSubscriptions) Len() int {
	return s.internalMap.Len()
}

// Close closes the subscriptions.
func (s *DocSubscriptions) Close() {
	s.publisher.Close()
}

// DocSubscription represents a subscription of a subscriber to documents.
type DocSubscription struct {
	id         string
	subscriber time.ActorID
	mu         sync.Mutex
	closed     bool
	events     chan events.DocEvent
}

// NewDocSubscription creates a new instance of Subscription.
func NewDocSubscription(subscriber time.ActorID) *DocSubscription {
	return &DocSubscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan events.DocEvent, 1),
		closed:     false,
	}
}

// ID returns the id of this subscription.
func (s *DocSubscription) ID() string {
	return s.id
}

// Events returns the DocEvent channel of this subscription.
func (s *DocSubscription) Events() chan events.DocEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *DocSubscription) Subscriber() time.ActorID {
	return s.subscriber
}

// Close closes all resources of this Subscription.
func (s *DocSubscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.events)
	}
}

// Publish publishes the given event to the subscriber.
func (s *DocSubscription) Publish(event events.DocEvent) bool {
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
	case <-gotime.After(publishTimeout):
		return false
	}
}
