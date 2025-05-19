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

package change

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Context is used to record the context of modification when editing a document.
// Each time we add an operation, a new time ticket is issued.
// Finally, returns a Change after the modification has been completed.
type Context struct {
	prevID         ID
	nextID         ID
	message        string
	operations     []operations.Operation
	delimiter      uint32
	root           *crdt.Root
	presenceChange *innerpresence.Change
}

// NewContext creates a new instance of Context.
func NewContext(prevID ID, message string, root *crdt.Root) *Context {
	return &Context{
		prevID:  prevID,
		nextID:  prevID.Next(),
		message: message,
		root:    root,
	}
}

// NextID returns the next ID of this context. It will be set to the
// document for the next change.
func (c *Context) NextID() ID {
	if len(c.operations) == 0 {
		// Even if the change has only presence change, the next ID for the document
		// shoule have clocks. For this, we pass the clocks of the previous ID.
		id := c.prevID.Next(true)
		id.lamport = c.prevID.lamport
		id.versionVector = c.prevID.versionVector
		return id
	}

	return c.nextID
}

// ToChange creates a new change of this context.
func (c *Context) ToChange() *Change {
	id := c.nextID

	// NOTE(hackerwins): If this context was created only for presence change,
	// we can use the ID without clocks that are used to resolve the
	// conflict.
	if len(c.operations) == 0 {
		id = c.prevID.Next(true)
	}

	return New(id, c.message, c.operations, c.presenceChange)
}

// HasChange returns whether this context has changes.
func (c *Context) HasChange() bool {
	return len(c.operations) > 0 || c.presenceChange != nil
}

// IssueTimeTicket creates a time ticket to be used to create a new operation.
func (c *Context) IssueTimeTicket() *time.Ticket {
	c.delimiter++
	return c.nextID.NewTimeTicket(c.delimiter)
}

// Push pushes a new operations into context queue.
func (c *Context) Push(op operations.Operation) {
	c.operations = append(c.operations, op)
}

// RegisterElement registers the given element to the root.
func (c *Context) RegisterElement(elem crdt.Element) {
	c.root.RegisterElement(elem)
}

// RegisterRemovedElementPair registers the given element pair to hash table.
func (c *Context) RegisterRemovedElementPair(parent crdt.Container, deleted crdt.Element) {
	c.root.RegisterRemovedElementPair(parent, deleted)
}

// RegisterGCPair registers the given GC pair to the root.
func (c *Context) RegisterGCPair(pair crdt.GCPair) {
	c.root.RegisterGCPair(pair)
}

// LastTimeTicket returns the last time ticket issued by this context.
func (c *Context) LastTimeTicket() *time.Ticket {
	return c.nextID.NewTimeTicket(c.delimiter)
}

// SetPresenceChange sets the presence change of the user who made the change.
func (c *Context) SetPresenceChange(presenceChange innerpresence.Change) {
	c.presenceChange = &presenceChange
}
