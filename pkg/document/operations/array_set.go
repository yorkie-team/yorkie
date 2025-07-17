/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

// ArraySet is an operation representing setting an element in Array.
type ArraySet struct {
	// parentCreatedAt is the creation time of the Array that executes ArraySet.
	parentCreatedAt *time.Ticket

	// createdAt is the creation time of the target element to set.
	createdAt *time.Ticket

	// value is an element set by the set_by_index operations.
	value crdt.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewArraySet creates a new instance of ArraySet.
func NewArraySet(
	parentCreatedAt *time.Ticket,
	createdAt *time.Ticket,
	value crdt.Element,
	executedAt *time.Ticket,
) *ArraySet {
	return &ArraySet{
		parentCreatedAt: parentCreatedAt,
		createdAt:       createdAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *ArraySet) Execute(root *crdt.Root, _ time.VersionVector) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	obj, ok := parent.(*crdt.Array)
	if !ok {
		return ErrNotApplicableDataType
	}

	value, err := o.value.DeepCopy()
	if err != nil {
		return err
	}

	if err := obj.InsertAfter(o.createdAt, value, o.executedAt); err != nil {
		return err
	}

	_, err = obj.DeleteByCreatedAt(o.createdAt, o.executedAt)
	if err != nil {
		return err
	}

	// TODO(junseo): GC logic is not implemented here
	// because there is no way to distinguish between old and new element with same `createdAt`.
	root.RegisterElement(value)
	return nil
}

// Value returns the value of this operation.
func (o *ArraySet) Value() crdt.Element {
	return o.value
}

// ParentCreatedAt returns the creation time of the Array.
func (o *ArraySet) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *ArraySet) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *ArraySet) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// CreatedAt returns the creation time of the target element.
func (o *ArraySet) CreatedAt() *time.Ticket {
	return o.createdAt
}
