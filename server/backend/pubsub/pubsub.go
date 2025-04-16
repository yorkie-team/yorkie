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
	"context"
	"errors"
	"fmt"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrTooManySubscribers is returned when the the subscription limit is exceeded.
	ErrTooManySubscribers = errors.New("subscription limit exceeded")

	// ErrAlreadyConnected is returned when the client is already connected to the document.
	ErrAlreadyConnected = errors.New("already connected to the document")
)

const (
	// publishTimeout is the timeout for publishing an event.
	publishTimeout = 100 * gotime.Millisecond
)

// Subscriptions is a map of Subscriptions.
type Subscriptions struct {
	docKey      types.DocRefKey
	internalMap *cmap.Map[string, *Subscription]
	publisher   *BatchPublisher
}

func newSubscriptions(docKey types.DocRefKey) *Subscriptions {
	s := &Subscriptions{
		docKey:      docKey,
		internalMap: cmap.New[string, *Subscription](),
	}
	s.publisher = NewBatchPublisher(s, 100*gotime.Millisecond)
	return s
}

// Set adds the given subscription.
func (s *Subscriptions) Set(sub *Subscription) {
	s.internalMap.Set(sub.ID(), sub)
}

// Values returns the values of these subscriptions.
func (s *Subscriptions) Values() []*Subscription {
	return s.internalMap.Values()
}

// Publish publishes the given event.
func (s *Subscriptions) Publish(event events.DocEvent) {
	s.publisher.Publish(event)
}

// Delete deletes the subscription of the given id.
func (s *Subscriptions) Delete(id string) {
	s.internalMap.Delete(id, func(sub *Subscription, exists bool) bool {
		if exists {
			sub.Close()
		}
		return exists
	})
}

// Len returns the length of these subscriptions.
func (s *Subscriptions) Len() int {
	return s.internalMap.Len()
}

// Close closes the subscriptions.
func (s *Subscriptions) Close() {
	s.publisher.Close()
}

// PubSub is the memory implementation of PubSub, used for single server.
type PubSub struct {
	subscriptionsMap *cmap.Map[types.DocRefKey, *Subscriptions]
}

// New creates an instance of PubSub.
func New() *PubSub {
	return &PubSub{
		subscriptionsMap: cmap.New[types.DocRefKey, *Subscriptions](),
	}
}

// Subscribe subscribes to the given document keys.
func (m *PubSub) Subscribe(
	ctx context.Context,
	subscriber time.ActorID,
	docKey types.DocRefKey,
	limit int,
) (*Subscription, []time.ActorID, error) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Subscribe(%s,%s) Start`,
			docKey,
			subscriber,
		)
	}

	// Note(emplam27): This is a workaround to avoid race condition.
	// The race condition is that the subscription limit is exceeded
	// and the new subscription is not set. If newSub is nil,
	// it means the limit was exceeded and the subscription was not created.
	var newSub *Subscription
	_ = m.subscriptionsMap.Upsert(docKey, func(subs *Subscriptions, exists bool) *Subscriptions {
		if !exists {
			subs = newSubscriptions(docKey)
		}

		if limit > 0 && subs.Len() >= limit {
			return subs
		}

		newSub = NewSubscription(subscriber)
		subs.Set(newSub)
		return subs
	})

	if newSub == nil {
		return nil, nil, fmt.Errorf(
			"%d subscribers allowed per document: %w",
			limit,
			ErrTooManySubscribers,
		)
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Subscribe(%s,%s) End`,
			docKey,
			subscriber,
		)
	}

	ids := m.ClientIDs(docKey)
	return newSub, ids, nil
}

// Unsubscribe unsubscribes the given docKeys.
func (m *PubSub) Unsubscribe(
	ctx context.Context,
	docKey types.DocRefKey,
	sub *Subscription,
) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Unsubscribe(%s,%s) Start`,
			docKey,
			sub.Subscriber(),
		)
	}

	sub.Close()

	if subs, ok := m.subscriptionsMap.Get(docKey); ok {
		subs.Delete(sub.ID())

		m.subscriptionsMap.Delete(docKey, func(subs *Subscriptions, exists bool) bool {
			if !exists || 0 < subs.Len() {
				return false
			}

			subs.Close()
			return true
		})
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Unsubscribe(%s,%s) End`,
			docKey,
			sub.Subscriber(),
		)
	}
}

// Publish publishes the given event.
func (m *PubSub) Publish(
	ctx context.Context,
	publisherID time.ActorID,
	event events.DocEvent,
) {
	// NOTE(hackerwins): String() triggers the cache of ActorID to avoid
	// race condition of concurrent access to the cache.
	_ = event.Publisher.String()

	docKey := event.DocRefKey

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) Start`,
			docKey,
			publisherID,
		)
	}

	if subs, ok := m.subscriptionsMap.Get(docKey); ok {
		subs.Publish(event)
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) End`,
			docKey,
			publisherID,
		)
	}
}

// ClientIDs returns the clients of the given document.
func (m *PubSub) ClientIDs(docKey types.DocRefKey) []time.ActorID {
	subs, ok := m.subscriptionsMap.Get(docKey)
	if !ok {
		return nil
	}

	var ids []time.ActorID
	for _, sub := range subs.Values() {
		ids = append(ids, sub.Subscriber())
	}
	return ids
}
