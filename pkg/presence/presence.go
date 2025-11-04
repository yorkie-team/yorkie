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

// Package presence provides presence implementation.
package presence

import (
	gojson "encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// Presence represents a presence in a document.
type Presence struct {
	// key is the key of the presence.
	key key.Key

	// status is the status of the presence.
	status atomic.Int32

	// actorID is the ID of the actor currently working with this presence.
	actorMu sync.RWMutex
	actorID time.ActorID

	// count is the current count value from server.
	count atomic.Int64

	// seq is the last seen sequence number for ordering.
	seq atomic.Int64

	// broadcastRequests is the send-only channel to send broadcast requests.
	broadcastRequests chan BroadcastRequest

	// broadcastResponses is the receive-only channel to receive broadcast responses.
	broadcastResponses chan error

	// broadcastEventHandlers is a map of registered event handlers for broadcast events.
	broadcastEventHandlers map[string]func(
		topic, publisher string,
		payload []byte,
	) error
}

// BroadcastRequest represents a broadcast request that will be delivered to the client.
type BroadcastRequest struct {
	Topic   string
	Payload []byte
}

// New creates a new instance of Presence.
func New(k key.Key) *Presence {
	counter := &Presence{
		key:                    k,
		broadcastRequests:      make(chan BroadcastRequest, 1),
		broadcastResponses:     make(chan error, 1),
		broadcastEventHandlers: make(map[string]func(topic, publisher string, payload []byte) error),
	}
	counter.status.Store(int32(attachable.StatusDetached))
	return counter
}

// Key returns the key of this presence.
func (c *Presence) Key() key.Key {
	return c.key
}

// Type returns the type of this resource.
func (c *Presence) Type() attachable.ResourceType {
	return attachable.TypePresence
}

// Status returns the status of this presence.
func (c *Presence) Status() attachable.StatusType {
	return attachable.StatusType(c.status.Load())
}

// SetStatus updates the status of this presence.
func (c *Presence) SetStatus(status attachable.StatusType) {
	c.status.Store(int32(status))
}

// IsAttached returns whether this presence is attached or not.
func (c *Presence) IsAttached() bool {
	return attachable.StatusType(c.status.Load()) == attachable.StatusAttached
}

// ActorID returns ID of the actor currently working with this presence.
func (c *Presence) ActorID() time.ActorID {
	c.actorMu.RLock()
	defer c.actorMu.RUnlock()
	return c.actorID
}

// SetActor sets actor into this presence.
func (c *Presence) SetActor(actor time.ActorID) {
	c.actorMu.Lock()
	defer c.actorMu.Unlock()
	c.actorID = actor
}

// Count returns the current count value.
func (c *Presence) Count() int64 {
	return c.count.Load()
}

// Seq returns the last seen sequence number.
func (c *Presence) Seq() int64 {
	return c.seq.Load()
}

// UpdateCount updates the count and sequence number if the sequence is newer.
func (c *Presence) UpdateCount(count int64, seq int64) bool {
	// Only update if sequence is newer (or initial state with seq=0)
	currentSeq := c.seq.Load()
	if seq > currentSeq || seq == 0 {
		c.count.Store(count)
		c.seq.Store(seq)
		return true
	}

	return false
}

// BroadcastRequests returns the broadcast requests of this presence.
func (c *Presence) BroadcastRequests() <-chan BroadcastRequest {
	return c.broadcastRequests
}

// BroadcastResponses returns the broadcast responses of this presence.
func (c *Presence) BroadcastResponses() chan error {
	return c.broadcastResponses
}

// Broadcast encodes the given payload and sends a Broadcast request.
func (c *Presence) Broadcast(topic string, payload any) error {
	marshaled, err := gojson.Marshal(payload)
	if err != nil {
		return fmt.Errorf("broadcast payload: %w", err)
	}

	c.broadcastRequests <- BroadcastRequest{
		Topic:   topic,
		Payload: marshaled,
	}
	return <-c.broadcastResponses
}

// SubscribeBroadcastEvent subscribes to the given topic and registers
// an event handler.
func (c *Presence) SubscribeBroadcastEvent(
	topic string,
	handler func(topic, publisher string, payload []byte) error,
) {
	c.broadcastEventHandlers[topic] = handler
}

// UnsubscribeBroadcastEvent unsubscribes to the given topic and deregisters
// the event handler.
func (c *Presence) UnsubscribeBroadcastEvent(
	topic string,
) {
	delete(c.broadcastEventHandlers, topic)
}

// BroadcastEventHandlers returns the registered handlers for broadcast events.
func (c *Presence) BroadcastEventHandlers() map[string]func(
	topic string,
	publisher string,
	payload []byte,
) error {
	return c.broadcastEventHandlers
}
