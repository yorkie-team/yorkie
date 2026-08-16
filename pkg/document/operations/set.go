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

// Set represents an operation that stores the value corresponding to the
// given key in the Object.
type Set struct {
	// parentCreatedAt is the creation time of the Object that executes Set.
	parentCreatedAt *time.Ticket

	// key corresponds to the key of the object to set the value.
	key string

	// value is the value of this operation.
	value crdt.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSet creates a new instance of Set.
func NewSet(
	parentCreatedAt *time.Ticket,
	key string,
	value crdt.Element,
	executedAt *time.Ticket,
) *Set {
	return &Set{
		key:             key,
		value:           value,
		parentCreatedAt: parentCreatedAt,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Set) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*crdt.Object)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	// During undo/redo, skip rather than execute when obj or any of its
	// ancestors has been concurrently removed (set_operation.ts:81-89).
	if source == OpSourceUndoRedo && isRemovedOrOrphaned(root, obj) {
		return nil, ErrOperationSkipped
	}

	// The reverse must be built from the value at this key before it is
	// overwritten below (set_operation.ts:91-92): it restores the previous
	// value, or removes the key entirely when there was none.
	//
	// Skipped when the source discards the reverse (see OpSource.NeedsReverse,
	// and the same gate in Edit.Execute): on a remote apply or a server
	// replay the DeepCopy is pure cost, and its error path could abort a
	// change that applied fine before this operation grew a reverse. The
	// forward mutation and every size/GC bookkeeping below stay unconditional.
	var reverseOp Operation
	if source.NeedsReverse() {
		previous := obj.Get(o.key)
		if previous != nil && previous.RemovedAt() == nil {
			copied, err := previous.DeepCopy()
			if err != nil {
				return nil, err
			}
			reverseOp = NewSet(o.parentCreatedAt, o.key, copied, o.executedAt)
		} else {
			reverseOp = NewRemove(o.parentCreatedAt, o.value.CreatedAt(), o.executedAt)
		}
	}

	value, err := o.value.DeepCopy()
	if err != nil {
		return nil, err
	}
	// SetWithExecutedAt uses o.executedAt (rather than value's own createdAt)
	// as the LWW tie-break ticket. For local and remote Sets these are
	// always equal (the json layer issues one fresh ticket for both), so
	// this is behavior-preserving there; for undo/redo restoring an older
	// value under its original createdAt, it is required for the restore to
	// win the LWW comparison at all.
	removed := obj.SetWithExecutedAt(o.key, value, o.executedAt)

	// NOTE(hackerwins): During undo/redo, this Set may restore an element
	// under a createdAt that is already registered (set_operation.ts:98-104)
	// -- for example, undoing a Remove re-inserts the removed element under
	// its original identity. The stale entry must be deregistered before the
	// restored element is registered again.
	if source == OpSourceUndoRedo && root.FindByCreatedAt(value.CreatedAt()) != nil {
		root.DeregisterElement(value)
	}
	root.RegisterElement(value)
	if removed != nil {
		root.RegisterRemovedElementPair(obj, removed)
	}
	if value.RemovedAt() != nil {
		root.RegisterRemovedElementPair(obj, value)
	}
	return reverseOp, nil
}

// ParentCreatedAt returns the creation time of the Object.
func (o *Set) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Set) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Set) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Set) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// Key returns the key of this operation.
func (o *Set) Key() string {
	return o.key
}

// Value returns the value of this operation.
func (o *Set) Value() crdt.Element {
	return o.value
}
