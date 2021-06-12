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

package operation

import (
	"github.com/pkg/errors"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Add is an operation representing adding an element to an Array.
type Add struct {
	// parentCreatedAt is the creation time of the Array that executes Add.
	parentCreatedAt *time.Ticket

	// prevCreatedAt is the creation time of the previous element.
	prevCreatedAt *time.Ticket

	// value is an element added by the insert operation.
	value json.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewAdd creates a new instance of Add.
func NewAdd(
	parentCreatedAt *time.Ticket,
	prevCreatedAt *time.Ticket,
	value json.Element,
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
func (o *Add) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		return errors.WithStack(ErrNotApplicableDataType)
	}

	value := o.value.DeepCopy()
	obj.InsertAfter(o.prevCreatedAt, value)

	root.RegisterElement(value)
	return nil
}

// Value returns the value of this operation.
func (o *Add) Value() json.Element {
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
func (o *Add) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// PrevCreatedAt returns the creation time of previous element.
func (o *Add) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}
