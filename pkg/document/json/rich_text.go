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

// RichText represents a rich text in the document. As a proxy for the CRDT rich
// text, it is used when the user manipulates the rich text from the outside.
type RichText struct {
	*crdt.RichText
	context *change.Context
}

// NewRichText creates a new instance of RichText.
func NewRichText(ctx *change.Context, text *crdt.RichText) *RichText {
	return &RichText{
		RichText: text,
		context:  ctx,
	}
}

// Edit edits the given range with the given content and attributes.
func (p *RichText) Edit(from, to int, content string, attributes map[string]string) *RichText {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.RichText.Edit(
		fromPos,
		toPos,
		nil,
		content,
		attributes,
		ticket,
	)

	p.context.Push(operations.NewRichEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		attributes,
		ticket,
	))
	if !fromPos.Equal(toPos) {
		p.context.RegisterTextElementWithGarbage(p)
	}

	return p
}

// SetStyle applies the style of the given range.
func (p *RichText) SetStyle(from, to int, attributes map[string]string) *RichText {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)

	ticket := p.context.IssueTimeTicket()
	p.RichText.SetStyle(
		fromPos,
		toPos,
		attributes,
		ticket,
	)

	p.context.Push(operations.NewStyle(
		p.CreatedAt(),
		fromPos,
		toPos,
		attributes,
		ticket,
	))

	return p
}
