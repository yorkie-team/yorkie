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

type Remove struct {
	parentCreatedAt *time.Ticket
	createdAt       *time.Ticket
	executedAt      *time.Ticket
}

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

func (o *Remove) Execute(root *json.Root) error {
	parentElem := root.FindByCreatedAt(o.parentCreatedAt)

	switch parent := parentElem.(type) {
	case *json.Object:
		elem := parent.DeleteByCreatedAt(o.createdAt, o.executedAt)
		root.RegisterRemovedElementPair(parent, elem)
	case *json.Array:
		elem := parent.DeleteByCreatedAt(o.createdAt, o.executedAt)
		root.RegisterRemovedElementPair(parent, elem)
	default:
		return ErrNotApplicableDataType
	}

	return nil
}

func (o *Remove) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Remove) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Remove) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Remove) CreatedAt() *time.Ticket {
	return o.createdAt
}
