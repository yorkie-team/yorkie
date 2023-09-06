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

// Text represents a text in the document. As a proxy for the CRDT
// text, it is used when the user manipulates the rich text from the outside.
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

// CreateRange creates a range from the given positions.
func (p *Text) CreateRange(from, to int) (*crdt.RGATreeSplitNodePos, *crdt.RGATreeSplitNodePos) {
	fromPos, toPos, err := p.Text.CreateRange(from, to)
	if err != nil {
		panic(err)
	}
	return fromPos, toPos
}

// Edit edits the given range with the given content and attributes.
func (p *Text) Edit(from, to int, content string, attributes ...map[string]string) *Text {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos, err := p.Text.CreateRange(from, to)
	if err != nil {
		panic(err)
	}

	// TODO(hackerwins): We need to consider the case where the length of
	//  attributes is greater than 1.
	var attrs map[string]string
	if len(attributes) > 0 {
		attrs = attributes[0]
	}

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor, err := p.Text.Edit(
		fromPos,
		toPos,
		nil,
		content,
		attrs,
		ticket,
	)
	if err != nil {
		panic(err)
	}

	p.context.Push(operations.NewEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		attrs,
		ticket,
	))
	if !fromPos.Equal(toPos) {
		p.context.RegisterElementHasRemovedNodes(p)
	}

	return p
}

// Style applies the style of the given range.
func (p *Text) Style(from, to int, attributes map[string]string) *Text {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos, err := p.Text.CreateRange(from, to)
	if err != nil {
		panic(err)
	}

	ticket := p.context.IssueTimeTicket()
	maxCreationMapByActor, err := p.Text.Style(
		fromPos,
		toPos,
		nil,
		attributes,
		ticket,
	)
	if err != nil {
		panic(err)
	}

	p.context.Push(operations.NewStyle(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		attributes,
		ticket,
	))

	return p
}
