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

// Package events defines the events that occur in the document and the client.
package events

import (
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ClientEventType represents the type of the ClientEvent.
type ClientEventType string

const (
	// ClientActivatedEvent is an event that occurs when the client is activated.
	ClientActivatedEvent ClientEventType = "client-activated"

	// ClientDeactivatedEvent is an event that occurs when the client is deactivated.
	ClientDeactivatedEvent ClientEventType = "client-deactivated"
)

// DocEventType represents the type of the DocEvent.
type DocEventType string

const (
	// DocAttached is an event that occurs when a document is attached to a client.
	DocAttached DocEventType = "document-attached"

	// DocChanged is an event indicating that document is being
	// modified by a change.
	DocChanged DocEventType = "document-changed"

	// DocRootChanged is an event indicating that document's root content
	// is being changed by operation.
	DocRootChanged DocEventType = "document-root-changed"

	// DocWatched is an event that occurs when document is watched
	// by other clients.
	DocWatched DocEventType = "document-watched"

	// DocUnwatched is an event that occurs when document is
	// unwatched by other clients.
	DocUnwatched DocEventType = "document-unwatched"
)

// WebhookType returns a matched event webhook type.
func (t DocEventType) WebhookType() types.EventWebhookType {
	switch t {
	case DocRootChanged:
		return types.DocRootChanged
	default:
		return ""
	}
}

// DocEventBody includes additional data specific to the DocEvent.
type DocEventBody struct {
	Topic   string
	Payload []byte
}

// PayloadLen returns the size of the payload.
func (b *DocEventBody) PayloadLen() int {
	return len(b.Payload)
}

// DocEvent represents an event that occurs in the document.
type DocEvent struct {
	// Type is the type of the event.
	Type DocEventType

	// Key is the key of the document that the event occurred.
	Key types.DocRefKey

	// Actor is the actor who published the event.
	Actor time.ActorID

	// Body includes additional data specific to the DocEvent.
	Body DocEventBody
}

// ChannelEventType represents the type of the channel event.
type ChannelEventType string

const (
	// ChannelCreated is an event that occurs when a channel is created.
	ChannelCreated ChannelEventType = "channel-created"

	// ChannelPresenceChanged is an event that occurs when presence changes.
	ChannelPresenceChanged ChannelEventType = "presence-changed"

	// ChannelBroadcast is an event that occurs when a payload is broadcasted
	// on a specific topic in a channel.
	ChannelBroadcast ChannelEventType = "channel-broadcast"

	// SessionCreated is an event that occurs when a session is created.
	SessionCreated ChannelEventType = "session-created"
)

// ChannelEvent represents a channel event (count change or broadcast).
type ChannelEvent struct {
	// Type is the type of the channel event.
	Type ChannelEventType

	// Key is the key of the channel that the event occurred.
	Key types.ChannelRefKey

	// Seq is the sequence number of the channel event.
	Seq int64

	// Publisher is the actor who published the broadcast (for broadcasts).
	Publisher time.ActorID

	// Count is the current session count of the channel.
	SessionCount int64

	// Topic is the topic of the broadcast (for broadcasts).
	Topic string

	// Payload is the payload of the broadcast (for broadcasts).
	Payload []byte
}
