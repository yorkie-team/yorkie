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

// ChannelSubscriptions is a map of ChannelSubscriptions.
type ChannelSubscriptions struct {
	refKey      types.ChannelRefKey
	internalMap *cmap.Map[string, *ChannelSubscription]
	publisher   *ChannelPublisher
}

func newChannelSubscriptions(refKey types.ChannelRefKey) *ChannelSubscriptions {
	s := &ChannelSubscriptions{
		refKey:      refKey,
		internalMap: cmap.New[string, *ChannelSubscription](),
	}
	s.publisher = NewChannelBatchPublisher(s, 100*gotime.Millisecond)
	return s
}

// Set adds the given subscription.
func (s *ChannelSubscriptions) Set(sub *ChannelSubscription) {
	s.internalMap.Set(sub.ID(), sub)
}

// Values returns the values of these subscriptions.
func (s *ChannelSubscriptions) Values() []*ChannelSubscription {
	return s.internalMap.Values()
}

// Publish publishes the given event.
func (s *ChannelSubscriptions) Publish(event events.ChannelEvent) {
	s.publisher.Publish(event)
}

// Delete deletes the subscription of the given id.
func (s *ChannelSubscriptions) Delete(id string) {
	s.internalMap.Delete(id, func(sub *ChannelSubscription, exists bool) bool {
		if exists {
			sub.Close()
		}
		return exists
	})
}

// Len returns the length of these subscriptions.
func (s *ChannelSubscriptions) Len() int {
	return s.internalMap.Len()
}

// Close closes the subscriptions.
func (s *ChannelSubscriptions) Close() {
	s.publisher.Close()
}

// ChannelSubscription represents a subscription of a subscriber to presence events.
type ChannelSubscription struct {
	id         string
	subscriber time.ActorID
	mu         sync.Mutex
	closed     bool
	events     chan events.ChannelEvent
}

// NewChannelSubscription creates a new instance of ChannelSubscription.
func NewChannelSubscription(subscriber time.ActorID) *ChannelSubscription {
	return &ChannelSubscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan events.ChannelEvent, 10),
		closed:     false,
	}
}

// ID returns the id of this subscription.
func (s *ChannelSubscription) ID() string {
	return s.id
}

// Events returns the PresenceEvent channel of this subscription.
func (s *ChannelSubscription) Events() chan events.ChannelEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *ChannelSubscription) Subscriber() time.ActorID {
	return s.subscriber
}

// Close closes all resources of this PresenceSubscription.
func (s *ChannelSubscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.events)
	}
}

// Publish publishes the given event to the subscriber.
func (s *ChannelSubscription) Publish(event events.ChannelEvent) bool {
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
