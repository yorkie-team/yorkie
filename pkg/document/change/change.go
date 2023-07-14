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

// Package change provides the implementation of Change. Change is a set of
// operations that can be applied to a document.
package change

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Change represents a unit of modification in the document.
type Change struct {
	// id is the unique identifier of the change.
	id ID

	// message is used to save a description of the change.
	message string

	// operations represent a series of user edits.
	operations []operations.Operation

	// presenceChange represents the presenceChange of the user who made the change.
	// TODO(hackerwins): Consider using changes instead of entire presenceChange.
	presenceChange *innerpresence.PresenceChange
}

// New creates a new instance of Change.
func New(id ID, message string, operations []operations.Operation, p *innerpresence.PresenceChange) *Change {
	return &Change{
		id:             id,
		message:        message,
		operations:     operations,
		presenceChange: p,
	}
}

// Execute applies this change to the given JSON root.
func (c *Change) Execute(root *crdt.Root, presences *innerpresence.Map) error {
	for _, op := range c.operations {
		if err := op.Execute(root); err != nil {
			return err
		}
	}

	if c.presenceChange != nil {
		if c.presenceChange.ChangeType == innerpresence.Clear {
			presences.Delete(c.id.actorID.String())
		} else {
			presences.Store(c.id.actorID.String(), c.presenceChange.Presence)
		}
	}

	return nil
}

// ID returns the ID of this change.
func (c *Change) ID() ID {
	return c.id
}

// Message returns the message of this change.
func (c *Change) Message() string {
	return c.message
}

// Operations returns the operations of this change.
func (c *Change) Operations() []operations.Operation {
	return c.operations
}

// ServerSeq returns the serverSeq of this change.
func (c *Change) ServerSeq() int64 {
	return c.id.ServerSeq()
}

// ClientSeq returns the clientSeq of this change.
func (c *Change) ClientSeq() uint32 {
	return c.id.ClientSeq()
}

// SetServerSeq sets the given serverSeq.
func (c *Change) SetServerSeq(serverSeq int64) {
	c.id = c.id.SetServerSeq(serverSeq)
}

// SetActor sets the given actorID.
func (c *Change) SetActor(actor *time.ActorID) {
	c.id = c.id.SetActor(actor)
	for _, op := range c.operations {
		op.SetActor(actor)
	}
}

// PresenceChange returns the presence change of this change.
func (c *Change) PresenceChange() *innerpresence.PresenceChange {
	return c.presenceChange
}
