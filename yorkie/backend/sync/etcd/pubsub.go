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

package etcd

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// Subscribe subscribes to the given topics.
func (c *Client) Subscribe(
	subscriber types.Client,
	topics []string,
) (*sync.Subscription, map[string][]types.Client, error) {
	return c.pubSub.Subscribe(subscriber, topics)
}

// Unsubscribe unsubscribes the given topics.
func (c *Client) Unsubscribe(topics []string, sub *sync.Subscription) {
	c.pubSub.Unsubscribe(topics, sub)
}

// Publish publishes the given event to the given Topic.
func (c *Client) Publish(
	publisherID *time.ActorID,
	topic string,
	event sync.DocEvent,
) {
	c.pubSub.Publish(publisherID, topic, event)

	// TODO(hackerwins): broadcast the event to other agents.
}
