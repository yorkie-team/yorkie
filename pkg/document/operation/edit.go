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

// Edit is an operation representing editing Text.
type Edit struct {
	// parentCreatedAt is the creation time of the Text that executes Edit.
	parentCreatedAt *time.Ticket

	// from represents the start point of the editing range.
	from *json.RGATreeSplitNodePos

	// to represents the end point of the editing range.
	to *json.RGATreeSplitNodePos

	// latestCreatedAtMapByActor is a map that stores the latest creation time
	// by actor for the nodes included in the editing range.
	latestCreatedAtMapByActor map[string]*time.Ticket

	// content is the content of text added when editing.
	content string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewEdit creates a new instance of Edit.
func NewEdit(
	parentCreatedAt *time.Ticket,
	from *json.RGATreeSplitNodePos,
	to *json.RGATreeSplitNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	executedAt *time.Ticket,
) *Edit {
	return &Edit{
		parentCreatedAt:           parentCreatedAt,
		from:                      from,
		to:                        to,
		latestCreatedAtMapByActor: latestCreatedAtMapByActor,
		content:                   content,
		executedAt:                executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (e *Edit) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *json.Text:
		obj.Edit(e.from, e.to, e.latestCreatedAtMapByActor, e.content, e.executedAt)
		if e.from.Compare(e.to) != 0 {
			root.RegisterRemovedNodeTextElement(obj)
		}
	default:
		return errors.WithStack(ErrNotApplicableDataType)
	}

	return nil
}

// From returns the start point of the editing range.
func (e *Edit) From() *json.RGATreeSplitNodePos {
	return e.from
}

// To returns the end point of the editing range.
func (e *Edit) To() *json.RGATreeSplitNodePos {
	return e.to
}

// ExecutedAt returns execution time of this operation.
func (e *Edit) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *Edit) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Text.
func (e *Edit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Content returns the content of Edit.
func (e *Edit) Content() string {
	return e.content
}

// CreatedAtMapByActor returns the map that stores the latest creation time
// by actor for the nodes included in the editing range.
func (e *Edit) CreatedAtMapByActor() map[string]*time.Ticket {
	return e.latestCreatedAtMapByActor
}
