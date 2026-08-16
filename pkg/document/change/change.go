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
	"errors"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
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

	// presenceChange is the change of presence information.
	presenceChange *inner.Change
}

// New creates a new instance of Change.
func New(id ID, message string, operations []operations.Operation, pc *inner.Change) *Change {
	return &Change{
		id:             id,
		message:        message,
		operations:     operations,
		presenceChange: pc,
	}
}

// ExecutionResult is what executing a Change reports to its caller. It is
// the port of what JS's Change.execute returns (change.ts:196), minus the
// OpInfo list Go does not materialize.
type ExecutionResult struct {
	// Executed lists the operations that actually applied, in the order they
	// ran. An operation that declined to execute is not among them.
	Executed []operations.Operation

	// Observable reports whether any executed operation changed something a
	// peer or an editor binding would see. It stands in for JS's
	// `opInfos.length > 0`; see operations.ExecutionResult.Observable for
	// what each operation counts.
	Observable bool

	// ReverseOps holds the operations that undo this change, in reverse
	// order, so replaying them walks the change backwards.
	ReverseOps []operations.Operation
}

// Execute applies this change to the given JSON root.
func (c *Change) Execute(
	root *crdt.Root,
	presences *inner.Map,
	source operations.OpSource,
) (ExecutionResult, error) {
	var result ExecutionResult

	for _, op := range c.operations {
		opResult, err := op.Execute(root, source, c.ID().versionVector)
		if err != nil {
			// NOTE(hackerwins): An operation whose target was concurrently
			// removed declines to execute during undo/redo. It is dropped
			// before it can reach the executed list, so a change whose
			// every operation is skipped ends up empty and the caller can
			// refuse to propagate it (change.ts:172-175).
			if errors.Is(err, operations.ErrOperationSkipped) {
				continue
			}
			return ExecutionResult{}, err
		}
		result.Executed = append(result.Executed, op)
		result.Observable = result.Observable || opResult.Observable

		// NOTE(hackerwins): Reverse operations are accumulated in reverse
		// order so that undoing a change replays its operations backwards.
		if opResult.Reverse != nil {
			result.ReverseOps = append(
				[]operations.Operation{opResult.Reverse}, result.ReverseOps...)
		}
	}

	if c.presenceChange != nil {
		c.presenceChange.Execute(c.id.actorID, presences)
	}

	return result, nil
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
func (c *Change) SetActor(actor time.ActorID) {
	c.id = c.id.SetActor(actor)
	for _, op := range c.operations {
		op.SetActor(actor)
	}
}

// PresenceChange returns the presence change of this change.
func (c *Change) PresenceChange() *inner.Change {
	return c.presenceChange
}

// SetPresenceChange replaces the presence change carried by this change.
// Passing nil drops the presence change in place; the server uses this to
// strip presence on documents created with disable_presence.
func (c *Change) SetPresenceChange(pc *inner.Change) {
	c.presenceChange = pc
}

// HasOperations reports whether this change carries at least one operation.
// A change with zero operations and a nil presence change can be safely
// dropped from the wire.
func (c *Change) HasOperations() bool {
	return len(c.operations) > 0
}

// AfterOrEqual returns whether this change is after or equal to the given change.
func (c *Change) AfterOrEqual(other *Change) bool {
	return c.id.AfterOrEqual(other.id)
}
