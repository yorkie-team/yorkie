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

// EditReverse is the reverse operation of Edit operation, which representing editing Text. Most of the same as
// Edit, but with index range.
type EditReverse struct {
	// parentCreatedAt is the creation time of the Text that executes
	// Edit.
	parentCreatedAt *time.Ticket

	// deletedIDs represents the ids of deleted nodes from the edit operation.
	deletedIDs []*crdt.RGATreeSplitNodePos

	// insertedIDs represents the ids of inserted nodes from the edit operation.
	insertedIDs []*crdt.RGATreeSplitNodePos

	// latestCreatedAtMapByActor is a map that stores the latest creation time
	// by actor for the nodes included in the editing range.
	latestCreatedAtMapByActor map[string]*time.Ticket

	// attributes represents the text style.
	attributes map[string]string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewEditReverse creates a new instance of Edit.
func NewEditReverse(
	parentCreatedAt *time.Ticket,
	deletedIDs []*crdt.RGATreeSplitNodePos,
	insertedIDs []*crdt.RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) *EditReverse {
	return &EditReverse{
		parentCreatedAt: parentCreatedAt,
		deletedIDs:      deletedIDs,
		insertedIDs:     insertedIDs,
		attributes:      attributes,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
// TODO(Hyemmie): port js-sdk's EditReverseOperation.execute() code
func (e *EditReverse) Execute(root *crdt.Root) error {
	return nil
}

// DeletedIDs returns the deleted IDs of this operation.
func (e *EditReverse) DeletedIDs() []*crdt.RGATreeSplitNodePos {
	return e.deletedIDs
}

// InsertedIDs returns the inserted IDs of this operation.
func (e *EditReverse) InsertedIDs() []*crdt.RGATreeSplitNodePos {
	return e.insertedIDs
}

// ExecutedAt returns execution time of this operation.
func (e *EditReverse) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *EditReverse) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Text.
func (e *EditReverse) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Attributes returns the attributes of this Edit.
func (e *EditReverse) Attributes() map[string]string {
	return e.attributes
}

// CreatedAtMapByActor returns the map that stores the latest creation time
// by actor for the nodes included in the editing range.
func (e *EditReverse) CreatedAtMapByActor() map[string]*time.Ticket {
	return e.latestCreatedAtMapByActor
}
