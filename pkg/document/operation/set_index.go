/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// SetIndex is an operation representing setting an element to an Array.
type SetIndex struct {
	// parentCreatedAt is the creation time of the Object that executes Set.
	parentCreatedAt *time.Ticket

	// positionAt is the creation time of the Object that executes Set.
	positionAt *time.Ticket

	// value is the value of this operation.
	value json.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSetIndex creates a new instance of SetIndex.
func NewSetIndex(
	parentCreatedAt *time.Ticket,
	positionAt *time.Ticket,
	value json.Element,
	executedAt *time.Ticket,
) *SetIndex {
	return &SetIndex{
		parentCreatedAt: parentCreatedAt,
		positionAt:      positionAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (s *SetIndex) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(s.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		return ErrNotApplicableDataType
	}

	value := s.value.DeepCopy()
	deleted := obj.SetIndex(s.positionAt, value)
	root.RegisterElement(value)
	if deleted != nil {
		root.RegisterRemovedElementPair(obj, deleted)
	}

	return nil
}

// Value return the value of this operation.
func (s *SetIndex) Value() json.Element {
	return s.value
}

// ExecutedAt returns execution time of this operation.
func (s *SetIndex) ExecutedAt() *time.Ticket {
	return s.executedAt
}

// SetActor sets the given actor to this operation.
func (s *SetIndex) SetActor(actorID *time.ActorID) {
	s.executedAt = s.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Object.
func (s *SetIndex) ParentCreatedAt() *time.Ticket {
	return s.parentCreatedAt
}

// PositionAt returns the creation time of the Object.
func (s *SetIndex) PositionAt() *time.Ticket {
	return s.positionAt
}
