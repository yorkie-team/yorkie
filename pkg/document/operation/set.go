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

type Set struct {
	parentCreatedAt *time.Ticket
	key             string
	value           json.Element
	executedAt      *time.Ticket
}

func NewSet(
	parentCreatedAt *time.Ticket,
	key string,
	value json.Element,
	executedAt *time.Ticket,
) *Set {
	return &Set{
		key:             key,
		value:           value,
		parentCreatedAt: parentCreatedAt,
		executedAt:      executedAt,
	}
}

func (o *Set) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Object)
	if !ok {
		return ErrNotApplicableDataType
	}

	value := o.value.DeepCopy()
	obj.Set(o.key, value)
	root.RegisterElement(value)
	return nil
}

func (o *Set) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Set) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Set) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Set) Key() string {
	return o.key
}

func (o *Set) Value() json.Element {
	return o.value
}
