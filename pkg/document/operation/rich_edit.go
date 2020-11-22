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

type RichEdit struct {
	parentCreatedAt           *time.Ticket
	from                      *json.RGATreeSplitNodePos
	to                        *json.RGATreeSplitNodePos
	latestCreatedAtMapByActor map[string]*time.Ticket
	content                   string
	attributes                map[string]string
	executedAt                *time.Ticket
}

func NewRichEdit(
	parentCreatedAt *time.Ticket,
	from *json.RGATreeSplitNodePos,
	to *json.RGATreeSplitNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) *RichEdit {
	return &RichEdit{
		parentCreatedAt:           parentCreatedAt,
		from:                      from,
		to:                        to,
		latestCreatedAtMapByActor: latestCreatedAtMapByActor,
		content:                   content,
		attributes:                attributes,
		executedAt:                executedAt,
	}
}

func (e *RichEdit) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *json.RichText:
		obj.Edit(e.from, e.to, e.latestCreatedAtMapByActor, e.content, e.attributes, e.executedAt)
		if e.from.Compare(e.to) != 0 {
			root.RegisterRemovedNodeTextElement(obj)
		}
	default:
		return ErrNotApplicableDataType
	}

	return nil
}

func (e *RichEdit) From() *json.RGATreeSplitNodePos {
	return e.from
}

func (e *RichEdit) To() *json.RGATreeSplitNodePos {
	return e.to
}

func (e *RichEdit) ExecutedAt() *time.Ticket {
	return e.executedAt
}

func (e *RichEdit) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}
func (e *RichEdit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

func (e *RichEdit) Content() string {
	return e.content
}

func (e *RichEdit) Attributes() map[string]string {
	return e.attributes
}

func (e *RichEdit) CreatedAtMapByActor() map[string]*time.Ticket {
	return e.latestCreatedAtMapByActor
}
