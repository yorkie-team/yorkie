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

// SetByIndex is an operation representing setting an element to an Array.
type SetByIndex struct {
	// parentCreatedAt is the creation time of the Object that executes Set.
	parentCreatedAt *time.Ticket

	// targetCreatedAt is the creation time of the Object that executes Set.
	targetCreatedAt *time.Ticket

	// value is the value of this operation.
	value json.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSetByIndex creates a new instance of SetByIndex.
func NewSetByIndex(
	parentCreatedAt *time.Ticket,
	targetCreatedAt *time.Ticket,
	value json.Element,
	executedAt *time.Ticket,
) *SetByIndex {
	return &SetByIndex{
		parentCreatedAt: parentCreatedAt,
		targetCreatedAt: targetCreatedAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (s *SetByIndex) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(s.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		return ErrNotApplicableDataType
	}

	value := s.value.DeepCopy()
	deleted := obj.SetByIndex(s.targetCreatedAt, value)
	root.RegisterElement(value)
	if deleted != nil {
		root.RegisterRemovedElementPair(obj, deleted)
	}

	return nil
}

// Value return the value of this operation.
func (s *SetByIndex) Value() json.Element {
	return s.value
}

// ExecutedAt returns execution time of this operation.
func (s *SetByIndex) ExecutedAt() *time.Ticket {
	return s.executedAt
}

// SetActor sets the given actor to this operation.
func (s *SetByIndex) SetActor(actorID *time.ActorID) {
	s.executedAt = s.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Object.
func (s *SetByIndex) ParentCreatedAt() *time.Ticket {
	return s.parentCreatedAt
}

// TargetCreatedAt returns the creation time of the Object.
func (s *SetByIndex) TargetCreatedAt() *time.Ticket {
	return s.targetCreatedAt
}
