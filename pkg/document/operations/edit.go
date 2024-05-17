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

// Edit is an operation representing editing Text. Most of the same as
// Edit, but with additional style properties, attributes.
type Edit struct {
	// parentCreatedAt is the creation time of the Text that executes
	// Edit.
	parentCreatedAt *time.Ticket

	// from represents the start point of the editing range.
	from *crdt.RGATreeSplitNodePos

	// to represents the end point of the editing range.
	to *crdt.RGATreeSplitNodePos

	// maxCreatedAtMapByActor is a map that stores the latest creation time
	// by actor for the nodes included in the editing range.
	maxCreatedAtMapByActor map[string]*time.Ticket

	// content is the content of text added when editing.
	content string

	// attributes represents the text style.
	attributes map[string]string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewEdit creates a new instance of Edit.
func NewEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.RGATreeSplitNodePos,
	to *crdt.RGATreeSplitNodePos,
	maxCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) *Edit {
	return &Edit{
		parentCreatedAt:        parentCreatedAt,
		from:                   from,
		to:                     to,
		maxCreatedAtMapByActor: maxCreatedAtMapByActor,
		content:                content,
		attributes:             attributes,
		executedAt:             executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (e *Edit) Execute(root *crdt.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *crdt.Text:
		_, _, pairs, err := obj.Edit(e.from, e.to, e.maxCreatedAtMapByActor, e.content, e.attributes, e.executedAt)
		if err != nil {
			return err
		}

		for _, pair := range pairs {
			root.RegisterGCPair(pair)
		}
	default:
		return ErrNotApplicableDataType
	}

	return nil
}

// From returns the start point of the editing range.
func (e *Edit) From() *crdt.RGATreeSplitNodePos {
	return e.from
}

// To returns the end point of the editing range.
func (e *Edit) To() *crdt.RGATreeSplitNodePos {
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

// Attributes returns the attributes of this Edit.
func (e *Edit) Attributes() map[string]string {
	return e.attributes
}

// MaxCreatedAtMapByActor returns the map that stores the latest creation time
// by actor for the nodes included in the editing range.
func (e *Edit) MaxCreatedAtMapByActor() map[string]*time.Ticket {
	return e.maxCreatedAtMapByActor
}
