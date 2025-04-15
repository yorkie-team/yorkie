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

// DocEventType represents the type of the DocEvent.
type DocEventType string

const (
	// DocChangedEvent is an event indicating that document is being
	// modified by a change.
	DocChangedEvent DocEventType = "document-changed"

	// DocRootChangedEvent is an event indicating that document's root content
	// is being changed by operation.
	DocRootChangedEvent DocEventType = "document-root-changed"

	// DocWatchedEvent is an event that occurs when document is watched
	// by other clients.
	DocWatchedEvent DocEventType = "document-watched"

	// DocUnwatchedEvent is an event that occurs when document is
	// unwatched by other clients.
	DocUnwatchedEvent DocEventType = "document-unwatched"

	// DocBroadcastEvent is an event that occurs when a payload is broadcasted
	// on a specific topic.
	DocBroadcastEvent DocEventType = "document-broadcast"
)

// WebhookType returns a matched event webhook type.
func (t DocEventType) WebhookType() types.EventWebhookType {
	switch t {
	case DocRootChangedEvent:
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

	// Publisher is the actor who published the event.
	Publisher time.ActorID

	// DocRefKey is the key of the document that the event occurred.
	DocRefKey types.DocRefKey

	// Body includes additional data specific to the DocEvent.
	Body DocEventBody
}

// ClientEventType represents the type of the ClientEvent.
type ClientEventType string

const (
	// ClientActivatedEvent is an event that occurs when the client is activated.
	ClientActivatedEvent ClientEventType = "client-activated"
)

// ClientEvent represents an event that occurs in the client.
type ClientEvent struct {
	// Type is the type of the event.
	Type ClientEventType
}
