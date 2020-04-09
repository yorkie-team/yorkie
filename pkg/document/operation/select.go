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

type Select struct {
	parentCreatedAt *time.Ticket
	from            *json.TextNodePos
	to              *json.TextNodePos
	executedAt      *time.Ticket
}

func NewSelect(
	parentCreatedAt *time.Ticket,
	from *json.TextNodePos,
	to *json.TextNodePos,
	executedAt *time.Ticket,
) *Select {
	return &Select{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		executedAt:      executedAt,
	}
}

func (s *Select) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(s.parentCreatedAt)
	obj, ok := parent.(*json.Text)
	if !ok {
		return ErrNotApplicableDataType
	}

	obj.Select(s.from, s.to, s.executedAt)
	return nil
}

func (s *Select) From() *json.TextNodePos {
	return s.from
}

func (s *Select) To() *json.TextNodePos {
	return s.to
}

func (s *Select) ExecutedAt() *time.Ticket {
	return s.executedAt
}

func (s *Select) SetActor(actorID *time.ActorID) {
	s.executedAt = s.executedAt.SetActorID(actorID)
}
func (s *Select) ParentCreatedAt() *time.Ticket {
	return s.parentCreatedAt
}
