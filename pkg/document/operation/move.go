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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type Move struct {
	parentCreatedAt *time.Ticket
	prevCreatedAt   *time.Ticket
	createdAt       *time.Ticket
	executedAt      *time.Ticket
}

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

func (o *Move) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		return ErrNotApplicableDataType
	}

	obj.MoveAfter(o.prevCreatedAt, o.createdAt, o.executedAt)

	return nil
}

func (o *Move) CreatedAt() *time.Ticket {
	return o.createdAt
}

func (o *Move) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Move) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Move) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Move) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}
