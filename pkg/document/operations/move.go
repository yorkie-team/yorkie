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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Move is an operation representing moving an element to an Array.
type Move struct {
	// parentCreatedAt is the creation time of the Array that executes Move.
	parentCreatedAt *time.Ticket

	// prevCreatedAt is the creation time of the previous element.
	prevCreatedAt *time.Ticket

	// createdAt is the creation time of the target element to move.
	createdAt *time.Ticket

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewMove creates a new instance of Move.
func NewMove(
	parentCreatedAt *time.Ticket,
	prevCreatedAt *time.Ticket,
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) *Move {
	return &Move{
		parentCreatedAt: parentCreatedAt,
		prevCreatedAt:   prevCreatedAt,
		createdAt:       createdAt,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Move) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*crdt.Array)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	// The reverse must capture the target's current predecessor before
	// MoveAfter changes its position (move_operation.ts:80-81,110-119): it
	// moves the target back after whatever precedes it now.
	//
	// Skipped when the source discards the reverse (see OpSource.NeedsReverse,
	// and the same gate in Edit.Execute): on a remote apply or a server
	// replay the FindPrevCreatedAt lookup is pure cost, and its error path
	// could abort a change that applied fine before this operation grew a
	// reverse. The move and its GC bookkeeping below stay unconditional.
	var reverseOp Operation
	if source.NeedsReverse() {
		prevCreatedAt, err := obj.FindPrevCreatedAt(o.createdAt)
		if err != nil {
			return nil, err
		}
		reverseOp = NewMove(o.parentCreatedAt, prevCreatedAt, o.createdAt, o.executedAt)
	}

	deadNode, err := obj.MoveAfter(o.prevCreatedAt, o.createdAt, o.executedAt)
	if err != nil {
		return nil, err
	}

	if deadNode != nil {
		root.RegisterGCPair(crdt.GCPair{
			Parent: obj.RGATreeList(),
			Child:  deadNode,
		})
	}

	return reverseOp, nil
}

// CreatedAt returns the creation time of the target element.
func (o *Move) CreatedAt() *time.Ticket {
	return o.createdAt
}

// SetCreatedAt sets the creation time of the target element. Used by
// History.ReconcileCreatedAt when a stacked Move still targets an
// element's previous createdAt after undo/redo replaced it with a fresh
// one.
func (o *Move) SetCreatedAt(createdAt *time.Ticket) {
	o.createdAt = createdAt
}

// ParentCreatedAt returns the creation time of the Array.
func (o *Move) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Move) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Move) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Move) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// PrevCreatedAt returns the creation time of previous element.
func (o *Move) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}

// SetPrevCreatedAt sets the creation time of the previous element. Used by
// History.ReconcileCreatedAt when a stacked Move still anchors on an
// element's previous createdAt after undo/redo replaced it with a fresh
// one.
func (o *Move) SetPrevCreatedAt(prevCreatedAt *time.Ticket) {
	o.prevCreatedAt = prevCreatedAt
}
