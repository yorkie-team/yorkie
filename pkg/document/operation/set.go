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

// Set represents an operation that stores the value corresponding to the
// given key in the Object.
type Set struct {
	// parentCreatedAt is the creation time of the Object that executes Set.
	parentCreatedAt *time.Ticket

	// key corresponds to the key of the object to set the value.
	key string

	// value is the value of this operation.
	value json.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSet creates a new instance of Set.
func NewSet(
	parentCreatedAt *time.Ticket,
	key string,
	value json.Element,
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
func (o *Set) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Object)
	if !ok {
		return errors.WithStack(ErrNotApplicableDataType)
	}

	value := o.value.DeepCopy()
	obj.Set(o.key, value)
	root.RegisterElement(value)
	return nil
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
func (o *Set) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// Key returns the key of this operation.
func (o *Set) Key() string {
	return o.key
}

// Value returns the value of this operation.
func (o *Set) Value() json.Element {
	return o.value
}
