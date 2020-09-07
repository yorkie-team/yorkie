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

// Increase can be used to increment data of numerical type.
// It can be used in counter type Integer, Long, and Double data.
type Increase struct {
	parentCreatedAt *time.Ticket
	value           json.Element
	executedAt      *time.Ticket
}

// NewIncrease creates the increase instance.
func NewIncrease(
	parentCreatedAt *time.Ticket,
	value json.Element,
	executedAt *time.Ticket,
) *Increase {
	return &Increase{
		parentCreatedAt: parentCreatedAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// Execute executes operation.
func (o *Increase) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	cnt, ok := parent.(*json.Counter)
	if !ok {
		return ErrNotApplicableDataType
	}

	value := o.value.(*json.Primitive)
	cnt.Increase(value)

	return nil
}

// Value gets a value
func (o *Increase) Value() json.Element {
	return o.value
}

// ParentCreatedAt gets a parentCreatedAt
func (o *Increase) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt gets an executedAt
func (o *Increase) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets executedAt by actorID.
func (o *Increase) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}
