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

package sync

import (
	"errors"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
)

var (
	// ErrEmptyTopics is returned when the given topic is empty.
	ErrEmptyTopics = errors.New("empty topics")
)

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type      types.EventType
	DocKey    string
	Publisher types.Client
}

// Subscription represents the subscription of a subscriber. It is used across
// several topics.
type Subscription struct {
	id         string
	subscriber types.Client
	closed     bool
	events     chan DocEvent
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription(subscriber types.Client) *Subscription {
	return &Subscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		// [Workaround] The channel buffer size below avoids stopping during
		//   event issuing to the events channel. This bug occurs in the order
		//   of Publish and Unsubscribe.
		events: make(chan DocEvent, 10),
	}
}

// ID returns the id of this subscription.
func (s *Subscription) ID() string {
	return s.id
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription) Events() chan DocEvent {
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

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}

// PubSub is a structure to support event publishing/subscription.
type PubSub interface {
	// Subscribe subscribes to the given topics.
	Subscribe(
		subscriber types.Client,
		topics []string,
	) (*Subscription, map[string][]types.Client, error)

	// Unsubscribe unsubscribes the given topics.
	Unsubscribe(topics []string, sub *Subscription)

	// Publish publishes the given event to the given Topic.
	Publish(publisherID *time.ActorID, topic string, event DocEvent)
}
