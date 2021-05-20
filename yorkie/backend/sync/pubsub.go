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
	gotime "time"

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

// AgentInfo represents the information of the Agent.
type AgentInfo struct {
	ID        string      `json:"id"`
	Hostname  string      `json:"hostname"`
	RPCAddr   string      `json:"rpc_addr"`
	UpdatedAt gotime.Time `json:"updated_at"`
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

	// Members returns the members of this cluster.
	Members() map[string]*AgentInfo
}
