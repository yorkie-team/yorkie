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

	// ErrBroadcastUnavailable is returned when a broadcast cannot be delivered
	// because nothing is servicing this channel's broadcast requests: the
	// channel is not being watched, or its watch has been retired by a detach
	// or a deactivation.
	ErrBroadcastUnavailable = errors.FailedPrecond("broadcast is unavailable").
				WithCode("ErrBroadcastUnavailable")
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

	// broadcastMu guards servingDone, the signal that tells Broadcast whether
	// anything is servicing broadcastRequests. Broadcast hands its request to
	// the goroutine Client.WatchChannel starts and then waits for that
	// goroutine's answer; the goroutine lives on the attachment's watch
	// context, so a detach or a deactivation retires it, and after that an
	// unguarded Broadcast would block forever on an answer nobody sends.
	// servingDone is closed whenever no servicer is running -- including before
	// the first watch -- so those calls fail fast instead. It doubles as the
	// current servicer's claim token, which is what keeps a retiring servicer
	// from retiring its successor instead of itself.
	broadcastMu sync.Mutex
	servingDone ServingToken

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

// ServingToken identifies one servicer's claim on a channel's broadcast
// requests. The channel hands it out in StartBroadcastServing and accepts it
// back in StopBroadcastServing and SendBroadcastResponse, which both ignore a
// token that no longer holds the claim: a servicer that outlives its own watch
// must not retire its successor, nor land a late answer on its successor's
// caller. It is closed once the claim it names is retired.
type ServingToken chan struct{}

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
		servingDone:            make(ServingToken),
	}
	close(ch.servingDone)
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

// StartBroadcastServing claims this channel's broadcast requests for a servicer
// that is about to start answering them, so Broadcast may wait for it.
// Client.WatchChannel calls it before launching that goroutine.
//
// The claim is exclusive: a second servicer is refused while one still holds
// it, since both would read from the same broadcastRequests and the answer
// would go to whichever won the race. The caller must pass the returned token
// back to StopBroadcastServing and SendBroadcastResponse, which is what keeps
// a servicer retiring late from disowning its successor.
func (c *Channel) StartBroadcastServing() (ServingToken, bool) {
	c.broadcastMu.Lock()
	defer c.broadcastMu.Unlock()

	select {
	case <-c.servingDone:
	default:
		return nil, false
	}
	c.servingDone = make(ServingToken)

	// A retired servicer can leave behind a request it never answered, or an
	// answer whose caller has already given up on it. Both belong to a
	// Broadcast that has returned, so drop them rather than let the new
	// servicer pair them with the next call.
	select {
	case <-c.broadcastRequests:
	default:
	}
	select {
	case <-c.broadcastResponses:
	default:
	}

	return c.servingDone, true
}

// StopBroadcastServing announces that the servicer holding token is gone:
// waiting and subsequent Broadcast calls return ErrBroadcastUnavailable instead
// of blocking on an answer nobody will send. It is idempotent, and a token that
// no longer holds the claim retires nothing.
func (c *Channel) StopBroadcastServing(token ServingToken) {
	c.broadcastMu.Lock()
	defer c.broadcastMu.Unlock()

	if token == nil || c.servingDone != token {
		return
	}

	select {
	case <-c.servingDone:
	default:
		close(c.servingDone)
	}
}

// SendBroadcastResponse hands the servicer's answer to the Broadcast waiting
// for it and reports whether it was delivered. A token that no longer holds the
// claim delivers nothing: its caller has already been released with
// ErrBroadcastUnavailable, and the answer would otherwise be read by the next
// servicer's caller as its own.
func (c *Channel) SendBroadcastResponse(token ServingToken, result error) bool {
	c.broadcastMu.Lock()
	defer c.broadcastMu.Unlock()

	if token == nil || c.servingDone != token {
		return false
	}

	// The send cannot block: the claim is exclusive and its holder answers one
	// request at a time, so broadcastResponses -- drained at every claim -- has
	// room for this answer.
	select {
	case c.broadcastResponses <- result:
		return true
	default:
		return false
	}
}

// Broadcast encodes the given payload and sends a Broadcast request. It returns
// ErrBroadcastUnavailable when no servicer is running -- the channel is not
// watched, or its watch was retired while this call was waiting.
func (c *Channel) Broadcast(topic string, payload any) error {
	marshaled, err := gojson.Marshal(payload)
	if err != nil {
		return fmt.Errorf("broadcast payload: %w", err)
	}

	c.broadcastMu.Lock()
	done := c.servingDone
	c.broadcastMu.Unlock()

	select {
	case c.broadcastRequests <- BroadcastRequest{
		Topic:   topic,
		Payload: marshaled,
	}:
	case <-done:
		return ErrBroadcastUnavailable
	}

	select {
	case err := <-c.broadcastResponses:
		return err
	case <-done:
		// The servicer may have answered just as it retired; prefer its answer
		// over reporting a broadcast that did go out as unavailable.
		select {
		case err := <-c.broadcastResponses:
			return err
		default:
			return ErrBroadcastUnavailable
		}
	}
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
