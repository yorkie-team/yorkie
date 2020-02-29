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
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

type Add struct {
	parentCreatedAt *time.Ticket
	prevCreatedAt   *time.Ticket
	value           json.Element
	executedAt      *time.Ticket
}

func NewAdd(
	parentCreatedAt *time.Ticket,
	prevCreatedAt *time.Ticket,
	value json.Element,
	executedAt *time.Ticket,
) *Add {
	return &Add{
		parentCreatedAt: parentCreatedAt,
		prevCreatedAt:   prevCreatedAt,
		value:           value,
		executedAt:      executedAt,
	}
}

func (o *Add) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*json.Array)
	if !ok {
		err := fmt.Errorf("fail to execute, only Array can execute Set")
		log.Logger.Error(err)
		return err
	}

	value := o.value.Deepcopy()
	obj.InsertAfter(o.prevCreatedAt, value)
	root.RegisterElement(value)
	return nil
}

func (o *Add) Value() json.Element {
	return o.value
}

func (o *Add) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

func (o *Add) ExecutedAt() *time.Ticket {
	return o.executedAt
}

func (o *Add) SetActor(actorID *time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

func (o *Add) PrevCreatedAt() *time.Ticket {
	return o.prevCreatedAt
}
