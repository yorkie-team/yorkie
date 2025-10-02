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

// Package presence provides presence counter implementation.
package presence

import (
	"sync/atomic"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// Counter represents a lightweight presence counter for tracking online users.
type Counter struct {
	// key is the key of the presence counter.
	key key.Key

	// status is the status of the presence counter.
	status atomic.Int32 // Use atomic for thread-safe status operations

	// actorID is the ID of the actor currently working with this counter.
	actorID atomic.Pointer[time.ActorID]

	// count is the current count value from server.
	count atomic.Int64

	// seq is the last seen sequence number for ordering.
	seq atomic.Int64
}

// New creates a new instance of presence Counter.
func New(k key.Key) *Counter {
	counter := &Counter{
		key: k,
	}
	counter.status.Store(int32(attachable.StatusDetached))
	return counter
}

// Key returns the key of this presence counter.
func (c *Counter) Key() key.Key {
	return c.key
}

// Type returns the type of this resource.
func (c *Counter) Type() attachable.AttachableType {
	return attachable.AttachableTypePresence
}

// Status returns the status of this presence counter.
func (c *Counter) Status() attachable.StatusType {
	return attachable.StatusType(c.status.Load())
}

// SetStatus updates the status of this presence counter.
func (c *Counter) SetStatus(status attachable.StatusType) {
	c.status.Store(int32(status))
}

// IsAttached returns whether this presence counter is attached or not.
func (c *Counter) IsAttached() bool {
	return attachable.StatusType(c.status.Load()) == attachable.StatusAttached
}

// ActorID returns ID of the actor currently working with this counter.
func (c *Counter) ActorID() time.ActorID {
	if ptr := c.actorID.Load(); ptr != nil {
		return *ptr
	}
	return time.ActorID{}
}

// SetActor sets actor into this presence counter.
func (c *Counter) SetActor(actor time.ActorID) {
	c.actorID.Store(&actor)
}

// Count returns the current count value.
func (c *Counter) Count() int64 {
	return c.count.Load()
}

// Seq returns the last seen sequence number.
func (c *Counter) Seq() int64 {
	return c.seq.Load()
}

// UpdateCount updates the count and sequence number if the sequence is newer.
func (c *Counter) UpdateCount(count int64, seq int64) bool {
	// Only update if sequence is newer (or initial state with seq=0)
	currentSeq := c.seq.Load()
	if seq > currentSeq || seq == 0 {
		c.count.Store(count)
		c.seq.Store(seq)
		return true
	}
	return false
}

// Subscribe creates a subscription channel for count updates.
// This is a placeholder for the subscription mechanism.
func (c *Counter) Subscribe(callback func(count int64)) func() {
	// TODO(presence): Implement actual subscription mechanism
	// This will be connected to the watch stream
	return func() {
		// unsubscribe function
	}
}
