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

package json

import (
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
)

// Text represents a text in the document. As a proxy for the CRDT text, it is
// used when the user manipulates the text from the outside.
type Text struct {
	*crdt.Text
	context *change.Context
}

// NewText creates a new instance of Text.
func NewText(ctx *change.Context, text *crdt.Text) *Text {
	return &Text{
		Text:    text,
		context: ctx,
	}
}

// Edit edits the given range with the given content.
func (p *Text) Edit(from, to int, content string) *Text {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.Text.CreateRange(from, to)

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.Text.Edit(
		fromPos,
		toPos,
		nil,
		content,
		ticket,
	)

	p.context.Push(operations.NewEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		ticket,
	))
	if !fromPos.Equal(toPos) {
		p.context.RegisterTextElementWithGarbage(p)
	}
	return p
}

// Select stores that the given range has been selected.
func (p *Text) Select(from, to int) *Text {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.Text.CreateRange(from, to)

	ticket := p.context.IssueTimeTicket()
	p.Text.Select(
		fromPos,
		toPos,
		ticket,
	)

	p.context.Push(operations.NewSelect(
		p.CreatedAt(),
		fromPos,
		toPos,
		ticket,
	))
	return p
}
