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
	"fmt"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrTooManySubscribers is returned when the the subscription limit is exceeded.
	ErrTooManySubscribers = errors.ResourceExhausted("subscription limit exceeded").WithCode("ErrTooManySubscribers")

	// ErrAlreadyConnected is returned when the client is already connected to the document.
	ErrAlreadyConnected = errors.AlreadyExists("already connected to the document").WithCode("ErrAlreadyConnected")
)

const (
	// publishTimeout is the timeout for publishing an event.
	publishTimeout = 100 * gotime.Millisecond
)

// PubSub is the memory implementation of PubSub, used for single server.
type PubSub struct {
	docSubsMap      *cmap.Map[types.DocRefKey, *DocSubscriptions]
	presenceSubsMap *cmap.Map[types.PresenceRefKey, *PresenceSubscriptions]
}

// New creates an instance of PubSub.
func New() *PubSub {
	return &PubSub{
		docSubsMap:      cmap.New[types.DocRefKey, *DocSubscriptions](),
		presenceSubsMap: cmap.New[types.PresenceRefKey, *PresenceSubscriptions](),
	}
}

// Subscribe subscribes to the given document keys.
func (m *PubSub) Subscribe(
	ctx context.Context,
	subscriber time.ActorID,
	docKey types.DocRefKey,
	limit int,
) (*DocSubscription, []time.ActorID, error) {
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
	var newSub *DocSubscription
	_ = m.docSubsMap.Upsert(docKey, func(subs *DocSubscriptions, exists bool) *DocSubscriptions {
		if !exists {
			subs = newSubscriptions(docKey)
		}

		if limit > 0 && subs.Len() >= limit {
			return subs
		}

		newSub = NewDocSubscription(subscriber)
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
	sub *DocSubscription,
) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`Unsubscribe(%s,%s) Start`,
			docKey,
			sub.Subscriber(),
		)
	}

	sub.Close()

	if subs, ok := m.docSubsMap.Get(docKey); ok {
		subs.Delete(sub.ID())

		m.docSubsMap.Delete(docKey, func(subs *DocSubscriptions, exists bool) bool {
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
	_ = event.Actor.String()

	docKey := event.Key

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) Start`,
			docKey,
			publisherID,
		)
	}

	if subs, ok := m.docSubsMap.Get(docKey); ok {
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
	subs, ok := m.docSubsMap.Get(docKey)
	if !ok {
		return nil
	}

	var ids []time.ActorID
	for _, sub := range subs.Values() {
		ids = append(ids, sub.Subscriber())
	}
	return ids
}

// SubscribePresence subscribes to the given presence key.
func (m *PubSub) SubscribePresence(
	ctx context.Context,
	subscriber time.ActorID,
	refKey types.PresenceRefKey,
) (*PresenceSubscription, int64, error) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribePresence(%s,%s) Start`,
			refKey,
			subscriber,
		)
	}

	var newSub *PresenceSubscription

	_ = m.presenceSubsMap.Upsert(refKey, func(subs *PresenceSubscriptions, exists bool) *PresenceSubscriptions {
		if !exists {
			subs = newPresenceSubscriptions(refKey)
		}

		newSub = NewPresenceSubscription(subscriber)
		subs.Set(newSub)
		return subs
	})

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribePresence(%s,%s) End`,
			refKey,
			subscriber,
		)
	}

	return newSub, 0, nil
}

// UnsubscribePresence unsubscribes the given presence key.
func (m *PubSub) UnsubscribePresence(
	ctx context.Context,
	refKey types.PresenceRefKey,
	sub *PresenceSubscription,
) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`UnsubscribePresence(%s,%s) Start`,
			refKey,
			sub.Subscriber(),
		)
	}

	sub.Close()

	if subs, ok := m.presenceSubsMap.Get(refKey); ok {
		subs.Delete(sub.ID())

		m.presenceSubsMap.Delete(refKey, func(subs *PresenceSubscriptions, exists bool) bool {
			if !exists || 0 < subs.Len() {
				return false
			}

			subs.Close()
			return true
		})
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`UnsubscribePresence(%s,%s) End`,
			refKey,
			sub.Subscriber(),
		)
	}
}

// PublishPresence publishes the given presence event.
func (m *PubSub) PublishPresence(
	ctx context.Context,
	event events.PresenceEvent,
) {
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`PublishPresence(%s) Start`, event.Key)
	}

	if subs, ok := m.presenceSubsMap.Get(event.Key); ok {
		subs.Publish(event)
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`PublishPresence(%s) End`, event.Key)
	}
}
