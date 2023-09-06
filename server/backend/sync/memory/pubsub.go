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

package memory

import (
	"context"
	gosync "sync"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// subscriptionIDs is a set of subscriptionIDs.
type subscribers map[string]struct{}

func (s subscribers) Add(subscriber *time.ActorID) {
	s[subscriber.String()] = struct{}{}
}

func (s subscribers) Contains(subscriber *time.ActorID) bool {
	_, exists := s[subscriber.String()]
	return exists
}

func (s subscribers) Remove(subscriber *time.ActorID) {
	delete(s, subscriber.String())
}

func (s subscribers) Len() int {
	return len(s)
}

// DocSubs has the subscription data of the document.
type DocSubs struct {
	documentID            types.ID
	subMapBySubscriber    map[string]*sync.Subscription
	subscribersMapByEvent map[string]subscribers
}

// NewDocSubs returns a new DocSubs of the given document id.
func NewDocSubs(docID types.ID) *DocSubs {
	return &DocSubs{
		documentID:            docID,
		subMapBySubscriber:    make(map[string]*sync.Subscription),
		subscribersMapByEvent: make(map[string]subscribers),
	}
}

// Len returns the number of the subscribers in the DocSubs.
func (s *DocSubs) Len() int {
	return len(s.subMapBySubscriber)
}

// Add adds the given subscription to the DocSubs.
func (s *DocSubs) Add(sub *sync.Subscription) {
	s.subMapBySubscriber[sub.Subscriber().String()] = sub
}

// Remove closes the given subscription and cleans up the
// subscription entries related to it.
func (s *DocSubs) Remove(subscriber *time.ActorID) {
	if sub, ok := s.subMapBySubscriber[subscriber.String()]; ok {
		// Close the subscription.
		sub.Close()

		// Remove the subscriber from the subscriber lists of subscribed events.
		for _, t := range sub.Types() {
			if subscribers, ok := s.subscribersMapByEvent[t]; ok {
				subscribers.Remove(subscriber)

				if subscribers.Len() == 0 {
					delete(s.subscribersMapByEvent, t)
				}
			}
		}

		delete(s.subMapBySubscriber, subscriber.String())
	}

	// Remove the subscription from subMapBySubscriber
	delete(s.subMapBySubscriber, subscriber.String())
}

// SubscribeEvent adds a new subscription entry for the given eventType to the DocSubs.
func (s *DocSubs) SubscribeEvent(eventType string, subscriber *time.ActorID) {
	if sub, ok := s.subMapBySubscriber[subscriber.String()]; ok {
		if _, ok := s.subscribersMapByEvent[eventType]; !ok {
			s.subscribersMapByEvent[eventType] = make(subscribers)
		}
		s.subscribersMapByEvent[eventType].Add(subscriber)

		sub.AddType(eventType)
	}
}

// UnsubscribeEvent removes the subscription entry for the given eventType from the DocSubs.
func (s *DocSubs) UnsubscribeEvent(eventType string, subscriber *time.ActorID) {
	if sub, ok := s.subMapBySubscriber[subscriber.String()]; ok {
		if subscribers, ok := s.subscribersMapByEvent[eventType]; ok {
			subscribers.Remove(subscriber)

			if subscribers.Len() == 0 {
				delete(s.subscribersMapByEvent, eventType)
			}
		}

		sub.RemoveType(eventType)
	}
}

// PubSub is the memory implementation of PubSub, used for single server.
type PubSub struct {
	docSubsMapMu      *gosync.RWMutex
	docSubsMapByDocID map[types.ID]*DocSubs
}

// NewPubSub creates an instance of PubSub.
func NewPubSub() *PubSub {
	return &PubSub{
		docSubsMapMu:      &gosync.RWMutex{},
		docSubsMapByDocID: make(map[types.ID]*DocSubs),
	}
}

// SubscribeDoc subscribes to the given document keys.
func (m *PubSub) SubscribeDoc(
	ctx context.Context,
	subscriber *time.ActorID,
	documentID types.ID,
) (*sync.Subscription, error) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeDoc(%s,%s) Start`,
			documentID.String(),
			subscriber.String(),
		)
	}

	sub := sync.NewSubscription(subscriber)
	if _, ok := m.docSubsMapByDocID[documentID]; !ok {
		m.docSubsMapByDocID[documentID] = NewDocSubs(documentID)
	}

	m.docSubsMapByDocID[documentID].Add(sub)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeDoc(%s,%s) End`,
			documentID.String(),
			subscriber.String(),
		)
	}
	return sub, nil
}

// UnsubscribeDoc unsubscribes the given docKeys.
func (m *PubSub) UnsubscribeDoc(
	ctx context.Context,
	documentID types.ID,
	sub *sync.Subscription,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`UnsubscribeDoc(%s,%s) Start`,
			documentID,
			sub.Subscriber().String(),
		)
	}

	docSubs.Remove(sub.Subscriber())

	if docSubs.Len() == 0 {
		delete(m.docSubsMapByDocID, documentID)
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`UnsubscribeDoc(%s,%s) End`,
			documentID,
			sub.Subscriber().String(),
		)
	}
}

// SubscribeEvent subscribes to the given event that occurred in the specified document.
func (m *PubSub) SubscribeEvent(
	ctx context.Context,
	documentID types.ID,
	eventType string,
	subscriber *time.ActorID,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) Start`,
			documentID,
			eventType,
			subscriber,
		)
	}
	docSubs.SubscribeEvent(eventType, subscriber)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) End`,
			documentID,
			eventType,
			subscriber,
		)
	}
}

// UnsubscribeEvent unsubscribes to the given event that occurred in the specified document.
func (m *PubSub) UnsubscribeEvent(
	ctx context.Context,
	documentID types.ID,
	eventType string,
	subscriber *time.ActorID,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) Start`,
			documentID,
			eventType,
			subscriber,
		)
	}

	docSubs.UnsubscribeEvent(eventType, subscriber)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) End`,
			documentID,
			eventType,
			subscriber,
		)
	}
}

// Publish publishes the given event.
func (m *PubSub) Publish(
	ctx context.Context,
	documentID types.ID,
	eventType string,
	publisherID *time.ActorID,
	event sync.Event,
) {
	m.docSubsMapMu.RLock()
	defer m.docSubsMapMu.RUnlock()

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) Start`, documentID.String(), publisherID.String())
	}

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	subscribers, ok := docSubs.subscribersMapByEvent[eventType]
	if !ok {
		return
	}

	for subscriber := range subscribers {
		sub, ok := docSubs.subMapBySubscriber[subscriber]
		if !ok {
			continue
		}

		if sub.Subscriber().Compare(publisherID) == 0 {
			continue
		}

		if logging.Enabled(zap.DebugLevel) {
			logging.From(ctx).Debugf(
				`Publish %s(%s,%s) to %s`,
				eventType,
				documentID.String(),
				publisherID.String(),
				sub.Subscriber().String(),
			)
		}

		// NOTE: When a subscription is being closed by a subscriber,
		// the subscriber may not receive messages.
		select {
		case sub.Events() <- event:
		case <-gotime.After(100 * gotime.Millisecond):
			logging.From(ctx).Warnf(
				`Publish(%s,%s) to %s timeout`,
				documentID.String(),
				publisherID.String(),
				sub.Subscriber().String(),
			)
		}

	}
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(`Publish(%s,%s) End`, documentID.String(), publisherID.String())
	}
}

// ClientIDs returns the clients of the given document.
func (m *PubSub) ClientIDs(documentID types.ID) []*time.ActorID {
	m.docSubsMapMu.RLock()
	defer m.docSubsMapMu.RUnlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return nil
	}

	var ids []*time.ActorID
	for _, sub := range docSubs.subMapBySubscriber {
		ids = append(ids, sub.Subscriber())
	}

	return ids
}
