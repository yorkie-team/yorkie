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

package sync

import (
	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Subscription represents a subscription of a subscriber to documents.
type Subscription struct {
	id         string
	types      []string
	subscriber *time.ActorID
	closed     bool
	events     chan Event
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription(subscriber *time.ActorID) *Subscription {
	return &Subscription{
		id:         xid.New().String(),
		types:      make([]string, 0),
		subscriber: subscriber,
		events:     make(chan Event, 1),
	}
}

// ID returns the id of this subscription.
func (s *Subscription) ID() string {
	return s.id
}

// Types returns the subscribed event type list.
func (s *Subscription) Types() []string {
	return s.types
}

// AddType adds the event type to the subscribed event type list.
func (s *Subscription) AddType(t string) {
	s.types = append(s.types, t)
}

// RemoveType removes the event type from the subscribed event type list.
func (s *Subscription) RemoveType(t string) {
	found := -1
	for i, v := range s.types {
		if v == t {
			found = i
		}
	}
	if found != -1 {
		s.types = append(s.types[:found], s.types[found+1:]...)
	}
}

// Events returns the Event channel of this subscription.
func (s *Subscription) Events() chan Event {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription) Subscriber() *time.ActorID {
	return s.subscriber
}

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}

// Event represents a type for publishable events.
type Event interface{}
