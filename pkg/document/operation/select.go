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

// Select represents an operation that selects an area in the text.
type Select struct {
	// parentCreatedAt is the creation time of the Text that executes Select.
	parentCreatedAt *time.Ticket

	// from represents the start point of the selection.
	from *json.RGATreeSplitNodePos

	// to represents the end point of the selection.
	to *json.RGATreeSplitNodePos

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSelect creates a new instance of Select.
func NewSelect(
	parentCreatedAt *time.Ticket,
	from *json.RGATreeSplitNodePos,
	to *json.RGATreeSplitNodePos,
	executedAt *time.Ticket,
) *Select {
	return &Select{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (s *Select) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(s.parentCreatedAt)

	switch obj := parent.(type) {
	case *json.Text:
		obj.Select(s.from, s.to, s.executedAt)
	case *json.RichText:
		obj.Select(s.from, s.to, s.executedAt)
	default:
		return errors.WithStack(ErrNotApplicableDataType)
	}

	return nil
}

// From returns the start point of the selection.
func (s *Select) From() *json.RGATreeSplitNodePos {
	return s.from
}

// To returns the end point of the selection.
func (s *Select) To() *json.RGATreeSplitNodePos {
	return s.to
}

// ExecutedAt returns execution time of this operation.
func (s *Select) ExecutedAt() *time.Ticket {
	return s.executedAt
}

// SetActor sets the given actor to this operation.
func (s *Select) SetActor(actorID *time.ActorID) {
	s.executedAt = s.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Text.
func (s *Select) ParentCreatedAt() *time.Ticket {
	return s.parentCreatedAt
}
