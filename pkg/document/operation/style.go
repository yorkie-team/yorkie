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

type Style struct {
	parentCreatedAt *time.Ticket
	from            *json.RGATreeSplitNodePos
	to              *json.RGATreeSplitNodePos
	attributes      map[string]string
	executedAt      *time.Ticket
}

func NewStyle(
	parentCreatedAt *time.Ticket,
	from *json.RGATreeSplitNodePos,
	to *json.RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) *Style {
	return &Style{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		attributes:      attributes,
		executedAt:      executedAt,
	}
}

func (e *Style) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	obj, ok := parent.(*json.RichText)
	if !ok {
		return ErrNotApplicableDataType
	}

	obj.SetStyle(e.from, e.to, e.attributes, e.executedAt)
	return nil
}

func (e *Style) From() *json.RGATreeSplitNodePos {
	return e.from
}

func (e *Style) To() *json.RGATreeSplitNodePos {
	return e.to
}

func (e *Style) ExecutedAt() *time.Ticket {
	return e.executedAt
}

func (e *Style) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}
func (e *Style) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

func (e *Style) Attributes() map[string]string {
	return e.attributes
}
