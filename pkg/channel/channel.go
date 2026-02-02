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

// Package channel provides channel implementation.
package channel

import (
	gojson "encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
)

var (
	// ChannelKeyPathSeparator is the separator for channel key paths.
	ChannelKeyPathSeparator = "."

	// ErrInvalidChannelKey is returned when a channel key is invalid.
	ErrInvalidChannelKey = errors.InvalidArgument("channel key is invalid").WithCode("ErrInvalidChannelKey")
)

// Channel represents lightweight channel.
type Channel struct {
	// key is the key of the channel.
	key key.Key

	// status is the status of the channel.
	status atomic.Int32

	// actorID is the ID of the actor currently working with this channel.
	actorMu sync.RWMutex
	actorID time.ActorID

	// count is the current count value from server.
	sessionCount atomic.Int64

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

// New creates a new instance of Channel.
func New(k key.Key) (*Channel, error) {
	if !IsValidChannelKeyPath(k) {
		return nil, ErrInvalidChannelKey
	}

	ch := &Channel{
		key:                    k,
		broadcastRequests:      make(chan BroadcastRequest, 1),
		broadcastResponses:     make(chan error, 1),
		broadcastEventHandlers: make(map[string]func(topic, publisher string, payload []byte) error),
	}
	ch.status.Store(int32(attachable.StatusDetached))
	return ch, nil
}

// Key returns the key of this channel.
func (c *Channel) Key() key.Key {
	return c.key
}

// Type returns the type of this resource.
func (c *Channel) Type() attachable.ResourceType {
	return attachable.TypeChannel
}

// Status returns the status of this channel.
func (c *Channel) Status() attachable.StatusType {
	return attachable.StatusType(c.status.Load())
}

// SetStatus updates the status of this channel.
func (c *Channel) SetStatus(status attachable.StatusType) {
	c.status.Store(int32(status))
}

// IsAttached returns whether this channel is attached or not.
func (c *Channel) IsAttached() bool {
	return attachable.StatusType(c.status.Load()) == attachable.StatusAttached
}

// ActorID returns ID of the actor currently working with this channel.
func (c *Channel) ActorID() time.ActorID {
	c.actorMu.RLock()
	defer c.actorMu.RUnlock()
	return c.actorID
}

// SetActor sets actor into this channel.
func (c *Channel) SetActor(actor time.ActorID) {
	c.actorMu.Lock()
	defer c.actorMu.Unlock()
	c.actorID = actor
}

// SessionCount returns the current session count value.
func (c *Channel) SessionCount() int64 {
	return c.sessionCount.Load()
}

// Seq returns the last seen sequence number.
func (c *Channel) Seq() int64 {
	return c.seq.Load()
}

// UpdateSessionCount updates the session count and sequence number if the sequence is newer.
func (c *Channel) UpdateSessionCount(sessionCount int64, seq int64) bool {
	// Only update if sequence is newer (or initial state with seq=0)
	currentSeq := c.seq.Load()
	if seq > currentSeq || seq == 0 {
		c.sessionCount.Store(sessionCount)
		c.seq.Store(seq)
		return true
	}

	return false
}

// BroadcastRequests returns the broadcast requests of this channel.
func (c *Channel) BroadcastRequests() <-chan BroadcastRequest {
	return c.broadcastRequests
}

// BroadcastResponses returns the broadcast responses of this channel.
func (c *Channel) BroadcastResponses() chan error {
	return c.broadcastResponses
}

// Broadcast encodes the given payload and sends a Broadcast request.
func (c *Channel) Broadcast(topic string, payload any) error {
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
func (c *Channel) SubscribeBroadcastEvent(
	topic string,
	handler func(topic, publisher string, payload []byte) error,
) {
	c.broadcastEventHandlers[topic] = handler
}

// UnsubscribeBroadcastEvent unsubscribes to the given topic and deregisters
// the event handler.
func (c *Channel) UnsubscribeBroadcastEvent(
	topic string,
) {
	delete(c.broadcastEventHandlers, topic)
}

// BroadcastEventHandlers returns the registered handlers for broadcast events.
func (c *Channel) BroadcastEventHandlers() map[string]func(
	topic string,
	publisher string,
	payload []byte,
) error {
	return c.broadcastEventHandlers
}

// FirstKeyPath returns the first key path of the given channel key.
func (c *Channel) FirstKeyPath() string {
	return strings.Split(c.key.String(), ChannelKeyPathSeparator)[0]
}

func FirstKeyPath(key key.Key) (string, error) {
	paths, err := ParseKeyPath(key)
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

// ParseKeyPath splits a channel key into key path components.
func ParseKeyPath(key key.Key) ([]string, error) {
	if !IsValidChannelKeyPath(key) {
		return nil, ErrInvalidChannelKey
	}
	return strings.Split(key.String(), ChannelKeyPathSeparator), nil
}

// IsValidChannelKeyPath checks if a channel key is valid.
func IsValidChannelKeyPath(key key.Key) bool {
	if err := key.Validate(); err != nil {
		return false
	}

	if strings.HasPrefix(key.String(), ChannelKeyPathSeparator) ||
		strings.HasSuffix(key.String(), ChannelKeyPathSeparator) ||
		strings.Contains(key.String(), ChannelKeyPathSeparator+ChannelKeyPathSeparator) {
		return false
	}

	return len(strings.Split(key.String(), ChannelKeyPathSeparator)) > 0
}
