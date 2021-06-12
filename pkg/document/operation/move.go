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
func (o *Move) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		return errors.WithStack(ErrNotApplicableDataType)
	}

	obj.MoveAfter(o.prevCreatedAt, o.createdAt, o.executedAt)

	return nil
}

// CreatedAt returns the creation time of the target element.
func (o *Move) CreatedAt() *time.Ticket {
	return o.createdAt
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
func (o *Move) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// PrevCreatedAt returns the creation time of previous element.
func (o *Move) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}
