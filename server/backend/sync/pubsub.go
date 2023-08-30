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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Subscription represents a subscription of a subscriber to documents.
type Subscription[E Event] struct {
	id         string
	docID      types.ID
	types      []types.EventType
	subscriber *time.ActorID
	closed     bool
	events     chan E
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription[E Event](docID types.ID, subscriber *time.ActorID) *Subscription[E] {
	return &Subscription[E]{
		id:         xid.New().String(),
		docID:      docID,
		types:      make([]types.EventType, 0),
		subscriber: subscriber,
		events:     make(chan E, 1),
	}
}

// ID returns the id of this subscription.
func (s *Subscription[E]) ID() string {
	return s.id
}

// // DocID returns the doc id of this subscription.
// func (s *Subscription[E]) DocID() types.ID {
// 	return s.docID
// }

func (s *Subscription[E]) Types() []types.EventType {
	return s.types
}

func (s *Subscription[E]) AddType(t types.EventType) {
	s.types = append(s.types, t)
}

func (s *Subscription[E]) RemoveType(t types.EventType) {
	var idx int
	for i, v := range s.types {
		if v == t {
			idx = i
		}
	}
	s.types = append(s.types[:idx], s.types[idx+1:]...)
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription[E]) Events() chan E {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription[E]) Subscriber() *time.ActorID {
	return s.subscriber
}

// Close closes all resources of this Subscription.
func (s *Subscription[E]) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}

type Event interface {
	TypeString() string
	PublisherString() string
	PayloadString() string
}

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type       types.DocEventType
	Publisher  *time.ActorID
	DocumentID types.ID
}

func (e *DocEvent) TypeString() string {
	return string(e.Type)
}

func (e *DocEvent) PublisherString() string {
	return e.Publisher.String()
}

func (e *DocEvent) PayloadString() string {
	return string(e.DocumentID)
}

type BroadcastEvent struct {
	Type      string
	Publisher *time.ActorID
	Payload   string
}

func (e *BroadcastEvent) TypeString() string {
	return e.Type
}

func (e *BroadcastEvent) PublisherString() string {
	return e.Publisher.String()
}

func (e *BroadcastEvent) PayloadString() string {
	return e.Payload
}
