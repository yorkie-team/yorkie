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

// ChannelSubscription is a subscription to channel events.
type ChannelSubscription = Subscription[events.ChannelEvent]

// ChannelSubscriptions is a collection of channel subscriptions.
type ChannelSubscriptions = Subscriptions[events.ChannelEvent]

// NewChannelSubscription creates a new instance of ChannelSubscription.
func NewChannelSubscription(subscriber time.ActorID) *ChannelSubscription {
	return NewSubscription[events.ChannelEvent](subscriber, 10)
}

// newChannelSubscriptions creates a new ChannelSubscriptions for the given channel key.
func newChannelSubscriptions(refKey types.ChannelRefKey) *ChannelSubscriptions {
	return NewSubscriptions[events.ChannelEvent](
		refKey.String(),
		func(subs *Subscriptions[events.ChannelEvent]) *BatchPublisher[events.ChannelEvent] {
			return NewBatchPublisher(subs, 100*gotime.Millisecond, BatchPublisherConfig[events.ChannelEvent]{
				Filter: func(subscriber time.ActorID, event events.ChannelEvent) bool {
					// Skip sending broadcast events to the publisher themselves
					return event.Type == events.ChannelBroadcast && event.Publisher == subscriber
				},
			})
		},
	)
}
