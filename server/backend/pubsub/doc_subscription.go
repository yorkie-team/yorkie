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
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// DocSubscription is a subscription to document events.
type DocSubscription = Subscription[events.DocEvent]

// DocSubscriptions is a collection of document subscriptions.
type DocSubscriptions = Subscriptions[events.DocEvent]

// NewDocSubscription creates a new instance of DocSubscription.
func NewDocSubscription(subscriber time.ActorID) *DocSubscription {
	return NewSubscription[events.DocEvent](subscriber, 1)
}

// newSubscriptions creates a new DocSubscriptions for the given document key.
func newSubscriptions(docKey types.DocRefKey) *DocSubscriptions {
	return NewSubscriptions[events.DocEvent](
		docKey.String(),
		func(subs *Subscriptions[events.DocEvent]) *BatchPublisher[events.DocEvent] {
			docChangedCountMap := make(map[string]int)
			return NewBatchPublisher(subs, 100*gotime.Millisecond, BatchPublisherConfig[events.DocEvent]{
				Filter: func(subscriber time.ActorID, event events.DocEvent) bool {
					// Skip sending events to the actor who published them
					return subscriber.Compare(event.Actor) == 0
				},
				OnEnqueue: func(evts []events.DocEvent, newEvent events.DocEvent) ([]events.DocEvent, bool) {
					// NOTE(hackerwins): If the queue contains multiple DocChangedEvents from
					// the same publisher, only two events are processed.
					// This occurs when a client attaches/detaches a document since the order
					// of Changed and Watch/Unwatch events is not guaranteed.
					if newEvent.Type == events.DocChanged {
						count, exists := docChangedCountMap[newEvent.Actor.String()]
						if exists && count > 1 {
							return evts, false
						}
						docChangedCountMap[newEvent.Actor.String()] = count + 1
					}
					return append(evts, newEvent), true
				},
				OnPublish: func() {
					// Reset the dedup map after each publish batch
					for k := range docChangedCountMap {
						delete(docChangedCountMap, k)
					}
				},
			})
		},
	)
}
