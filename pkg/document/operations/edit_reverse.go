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

	// from represents the start index of the editing range.
	fromIdx int32

	// to represents the end index of the editing range.
	toIdx int32

	// latestCreatedAtMapByActor is a map that stores the latest creation time
	// by actor for the nodes included in the editing range.
	latestCreatedAtMapByActor map[string]*time.Ticket

	// content is the content of text added when editing.
	content string

	// attributes represents the text style.
	attributes map[string]string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewEditReverse creates a new instance of Edit.
func NewEditReverse(
	parentCreatedAt *time.Ticket,
	fromIdx int32,
	toIdx int32,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) *EditReverse {
	return &EditReverse{
		parentCreatedAt:           parentCreatedAt,
		fromIdx:                   fromIdx,
		toIdx:                     toIdx,
		latestCreatedAtMapByActor: latestCreatedAtMapByActor,
		content:                   content,
		attributes:                attributes,
		executedAt:                executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (e *EditReverse) Execute(root *crdt.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *crdt.Text:
		from, to, err := obj.CreateRange(int(e.fromIdx), int(e.toIdx))
		if err != nil {
			return err
		}
		_, _, err = obj.Edit(from, to, e.latestCreatedAtMapByActor, e.content, e.attributes, e.executedAt)
		if err != nil {
			return err
		}
		if !from.Equal(to) {
			root.RegisterElementHasRemovedNodes(obj)
		}
	default:
		return ErrNotApplicableDataType
	}

	return nil
}

// FromIdx returns the start index of the editing range.
func (e *EditReverse) FromIdx() int32 {
	return e.fromIdx
}

// ToIdx returns the end index of the editing range.
func (e *EditReverse) ToIdx() int32 {
	return e.toIdx
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

// Content returns the content of Edit.
func (e *EditReverse) Content() string {
	return e.content
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
