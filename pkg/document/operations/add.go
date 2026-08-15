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

// Add is an operation representing adding an element to an Array.
type Add struct {
	// parentCreatedAt is the creation time of the Array that executes Add.
	parentCreatedAt *time.Ticket

	// prevCreatedAt is the creation time of the previous element.
	prevCreatedAt *time.Ticket

	// value is an element added by the insert operations.
	value crdt.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewAdd creates a new instance of Add.
func NewAdd(
	parentCreatedAt *time.Ticket,
	prevCreatedAt *time.Ticket,
	value crdt.Element,
	executedAt *time.Ticket,
) *Add {
	return &Add{
		parentCreatedAt: parentCreatedAt,
		prevCreatedAt:   prevCreatedAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Add) Execute(root *crdt.Root, _ OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*crdt.Array)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	value, err := o.value.DeepCopy()
	if err != nil {
		return nil, err
	}

	if err = obj.InsertAfter(o.prevCreatedAt, value, o.executedAt); err != nil {
		return nil, err
	}

	root.RegisterElement(value)

	// The reverse is a Remove of the just-added element, mirroring
	// AddOperation.toReverseOperation (add_operation.ts:92-100). Its own
	// createdAt is reissued at execution time when this reverse is later
	// replayed as an UndoRemove (executeUndoRedo's Add branch), so the
	// target here is always the identity the value ends up living under.
	return NewRemove(o.parentCreatedAt, o.value.CreatedAt(), o.executedAt), nil
}

// Value returns the value of this operation.
func (o *Add) Value() crdt.Element {
	return o.value
}

// ParentCreatedAt returns the creation time of the Array.
func (o *Add) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Add) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Add) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Add) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// PrevCreatedAt returns the creation time of previous element.
func (o *Add) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}

// SetPrevCreatedAt sets the creation time of the previous element. Used by
// History.ReconcileCreatedAt when a stacked Add still anchors on an
// element's previous createdAt after undo/redo replaced it with a fresh
// one.
func (o *Add) SetPrevCreatedAt(prevCreatedAt *time.Ticket) {
	o.prevCreatedAt = prevCreatedAt
}
