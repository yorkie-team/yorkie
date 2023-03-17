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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// Subscription represents a subscription of a subscriber to documents.
type Subscription struct {
	id         string
	subscriber types.Client
	closed     bool
	events     chan ClientDocEvent
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription(subscriber types.Client) *Subscription {
	return &Subscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan ClientDocEvent, 1),
	}
}

// ID returns the id of this subscription.
func (s *Subscription) ID() string {
	return s.id
}

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type        types.DocEventType
	Publisher   types.Client
	DocumentID  types.ID
	DocumentKey key.Key
}

// ClientDocEvent represents doc events that should be sent to the client.
type ClientDocEvent struct {
	Type        types.DocEventType
	Publisher   types.Client
	DocumentKey key.Key
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription) Events() chan ClientDocEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription) Subscriber() types.Client {
	return s.subscriber
}

// SubscriberID returns string representation of the subscriber.
func (s *Subscription) SubscriberID() string {
	return s.subscriber.ID.String()
}

// UpdatePresence updates the presence of the subscriber.
func (s *Subscription) UpdatePresence(info types.PresenceInfo) {
	s.subscriber.PresenceInfo.Update(info)
}

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}
