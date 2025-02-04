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

	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Subscription represents a subscription of a subscriber to documents.
type Subscription struct {
	id         string
	subscriber *time.ActorID
	mu         sync.Mutex
	closed     bool
	events     chan events.DocEvent
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription(subscriber *time.ActorID) *Subscription {
	return &Subscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan events.DocEvent, 1),
		closed:     false,
	}
}

// ID returns the id of this subscription.
func (s *Subscription) ID() string {
	return s.id
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription) Events() chan events.DocEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription) Subscriber() *time.ActorID {
	return s.subscriber
}

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.events)
	}
}

// Publish publishes the given event to the subscriber.
func (s *Subscription) Publish(event events.DocEvent) bool {
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
