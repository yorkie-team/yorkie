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
func (o *Remove) Execute(root *json.Root) error {
	parentElem := root.FindByCreatedAt(o.parentCreatedAt)

	switch parent := parentElem.(type) {
	case json.Container:
		elem := parent.DeleteByCreatedAt(o.createdAt, o.executedAt)
		root.RegisterRemovedElementPair(parent, elem)
	default:
		return errors.WithStack(ErrNotApplicableDataType)
	}

	return nil
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
func (o *Remove) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// CreatedAt returns the creation time of the target element.
func (o *Remove) CreatedAt() *time.Ticket {
	return o.createdAt
}
