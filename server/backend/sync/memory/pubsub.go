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
type subscriptionIDs map[string]struct{}

func (s subscriptionIDs) Add(subID string) {
	s[subID] = struct{}{}
}

func (s subscriptionIDs) Contains(subID string) bool {
	_, exists := s[subID]
	return exists
}

func (s subscriptionIDs) Remove(subID string) {
	delete(s, subID)
}

func (s subscriptionIDs) Len() int {
	return len(s)
}

type DocSubs[E sync.Event] struct {
	documentID       types.ID
	subMapBySubIDs   map[string]*sync.Subscription[E]
	subIDsMapByEvent map[types.EventType]subscriptionIDs
}

func NewDocSubs[E sync.Event](docID types.ID) *DocSubs[E] {
	return &DocSubs[E]{
		documentID:       docID,
		subMapBySubIDs:   make(map[string]*sync.Subscription[E]),
		subIDsMapByEvent: make(map[types.EventType]subscriptionIDs),
	}
}

func (s *DocSubs[E]) Len() int {
	return len(s.subMapBySubIDs)
}

func (s *DocSubs[E]) Add(sub *sync.Subscription[E]) {
	s.subMapBySubIDs[sub.ID()] = sub
}

func (s *DocSubs[E]) Remove(subID string) {
	if sub, ok := s.subMapBySubIDs[subID]; ok {
		// Close the subscription
		sub.Close()

		// Remove the subID from subIDsMapByEvent
		for _, t := range sub.Types() {
			if subIDs, ok := s.subIDsMapByEvent[t]; ok {
				subIDs.Remove(subID)

				if subIDs.Len() == 0 {
					delete(s.subIDsMapByEvent, t)
				}
			}
		}
	}

	// Remove the subscription from subMapBySubIDs
	delete(s.subMapBySubIDs, subID)
}

func (s *DocSubs[E]) SubscribeEvent(eventType types.EventType, subID string) {
	if sub, ok := s.subMapBySubIDs[subID]; ok {
		if _, ok := s.subIDsMapByEvent[eventType]; !ok {
			s.subIDsMapByEvent[eventType] = make(subscriptionIDs)
		}
		s.subIDsMapByEvent[eventType].Add(subID)

		sub.AddType(eventType)
	}
}

func (s *DocSubs[E]) UnsubscribeEvent(eventType types.EventType, subID string) {
	if sub, ok := s.subMapBySubIDs[subID]; ok {
		if subIDs, ok := s.subIDsMapByEvent[eventType]; ok {
			subIDs.Remove(subID)
		}

		sub.RemoveType(eventType)
	}
}

// PubSub is the memory implementation of PubSub, used for single server.
type PubSub[E sync.Event] struct {
	docSubsMapMu      *gosync.RWMutex
	docSubsMapByDocID map[types.ID]*DocSubs[E]
}

// NewPubSub creates an instance of PubSub.
func NewPubSub[E sync.Event]() *PubSub[E] {
	return &PubSub[E]{
		docSubsMapMu:      &gosync.RWMutex{},
		docSubsMapByDocID: make(map[types.ID]*DocSubs[E]),
	}
}

// Watch subscribes to the given document keys.
func (m *PubSub[E]) SubscribeDoc(
	ctx context.Context,
	subscriber *time.ActorID,
	documentID types.ID,
) (string, error) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeDoc(%s,%s) Start`,
			documentID.String(),
			subscriber.String(),
		)
	}

	sub := sync.NewSubscription[E](documentID, subscriber)
	if _, ok := m.docSubsMapByDocID[documentID]; !ok {
		m.docSubsMapByDocID[documentID] = NewDocSubs[E](documentID)
	}

	m.docSubsMapByDocID[documentID].Add(sub)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeDoc(%s,%s) End`,
			documentID.String(),
			subscriber.String(),
		)
	}
	return sub.ID(), nil
}

// Unwatch unsubscribes the given docKeys.
func (m *PubSub[E]) UnsubscribeDoc(
	ctx context.Context,
	documentID types.ID,
	subID string,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	sub, ok := docSubs.subMapBySubIDs[subID]
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

	docSubs.Remove(subID)

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

func (m *PubSub[E]) SubscribeEvent(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	subID string,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	// TODO(sejongk): log subscriber info
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) Start`,
			documentID,
			eventType,
			subID,
		)
	}

	docSubs.SubscribeEvent(eventType, subID)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) End`,
			documentID,
			eventType,
			subID,
		)
	}
}

func (m *PubSub[E]) UnsubscribeEvent(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	subID string,
) {
	m.docSubsMapMu.Lock()
	defer m.docSubsMapMu.Unlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return
	}

	// TODO(sejongk): log subscriber info
	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) Start`,
			documentID,
			eventType,
			subID,
		)
	}

	docSubs.UnsubscribeEvent(eventType, subID)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			`SubscribeEvent(%s,%s,%s) End`,
			documentID,
			eventType,
			subID,
		)
	}
}

// Publish publishes the given event.
func (m *PubSub[E]) Publish(
	ctx context.Context,
	documentID types.ID,
	eventType types.EventType,
	publisherID *time.ActorID,
	event E,
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

	subIDs, ok := docSubs.subIDsMapByEvent[eventType]
	if !ok {
		return
	}

	for subID := range subIDs {
		sub, ok := docSubs.subMapBySubIDs[subID]
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
func (m *PubSub[E]) ClientIDs(documentID types.ID, eventType types.EventType) []*time.ActorID {
	m.docSubsMapMu.RLock()
	defer m.docSubsMapMu.RUnlock()

	docSubs, ok := m.docSubsMapByDocID[documentID]
	if !ok {
		return nil
	}

	subIDs, ok := docSubs.subIDsMapByEvent[eventType]
	if !ok {
		return nil
	}

	var ids []*time.ActorID
	for subID := range subIDs {
		if sub, ok := docSubs.subMapBySubIDs[subID]; ok {
			ids = append(ids, sub.Subscriber())
		}
	}

	return ids
}
