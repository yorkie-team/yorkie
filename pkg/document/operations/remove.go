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

// Remove is an operation representing removes an element from Container.
type Remove struct {
	// parentCreatedAt is the creation time of the Container that executes
	// Remove.
	parentCreatedAt *time.Ticket

	// createdAt is the creation time of the target element to remove.
	createdAt *time.Ticket

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewRemove creates a new instance of Remove.
func NewRemove(
	parentCreatedAt *time.Ticket,
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) *Remove {
	return &Remove{
		parentCreatedAt: parentCreatedAt,
		createdAt:       createdAt,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Remove) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (ExecutionResult, error) {
	parentElem := root.FindByCreatedAt(o.parentCreatedAt)

	parent, ok := parentElem.(crdt.Container)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	target := root.FindByCreatedAt(o.createdAt)

	// During undo/redo, skip rather than execute when the target or any of
	// its ancestors has been concurrently removed (remove_operation.ts:
	// 84-92).
	if source == OpSourceUndoRedo && isRemovedOrOrphaned(root, target) {
		return ExecutionResult{}, ErrOperationSkipped
	}

	// Both toReverseOperation and DeleteByCreatedAt look up the target
	// element by the same createdAt, so the reverse must be built before
	// DeleteByCreatedAt removes it (remove_operation.ts:94-99).
	//
	// Skipped when the source discards the reverse (see OpSource.NeedsReverse,
	// and the same gate in Edit.Execute): on a remote apply or a server
	// replay the DeepCopy and the FindPrevCreatedAt scan are pure cost, and
	// their error paths could abort a change that applied fine before this
	// operation grew a reverse. The delete and its bookkeeping below stay
	// unconditional.
	var reverseOp Operation
	if source.NeedsReverse() {
		var err error
		if reverseOp, err = o.toReverseOperation(parent, target); err != nil {
			return ExecutionResult{}, err
		}
	}

	elem, err := parent.DeleteByCreatedAt(o.createdAt, o.executedAt)
	if err != nil {
		return ExecutionResult{}, err
	}
	if elem != nil {
		root.RegisterRemovedElementPair(parent, elem)
	}
	return ExecutionResult{Reverse: reverseOp, Observable: true}, nil
}

// toReverseOperation returns the reverse operation of this Remove, or nil
// when it has none. It mirrors RemoveOperation.toReverseOperation
// (remove_operation.ts:125-155): for an Array parent the reverse is an Add
// that restores the element after its previous sibling; for an Object
// parent it is a Set that restores the element under its key.
func (o *Remove) toReverseOperation(parent crdt.Container, value crdt.Element) (Operation, error) {
	if value == nil {
		return nil, nil
	}

	switch p := parent.(type) {
	case *crdt.Array:
		prevCreatedAt, err := p.FindPrevCreatedAt(o.createdAt)
		if err != nil {
			return nil, err
		}
		copied, err := value.DeepCopy()
		if err != nil {
			return nil, err
		}
		return NewAdd(o.parentCreatedAt, prevCreatedAt, copied, o.executedAt), nil
	case *crdt.Object:
		key, ok := p.SubPathOf(o.createdAt)
		if !ok {
			return nil, nil
		}
		copied, err := value.DeepCopy()
		if err != nil {
			return nil, err
		}
		return NewSet(o.parentCreatedAt, key, copied, o.executedAt), nil
	default:
		return nil, nil
	}
}

// ParentCreatedAt returns the creation time of the Container.
func (o *Remove) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Remove) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Remove) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Remove) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// CreatedAt returns the creation time of the target element.
func (o *Remove) CreatedAt() *time.Ticket {
	return o.createdAt
}

// SetCreatedAt sets the creation time of the target element. Used by
// History.ReconcileCreatedAt when a stacked Remove still targets an
// element's previous createdAt after undo/redo replaced it with a fresh
// one.
func (o *Remove) SetCreatedAt(createdAt *time.Ticket) {
	o.createdAt = createdAt
}
