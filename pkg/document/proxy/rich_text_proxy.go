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

package proxy

import (
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
)

// RichTextProxy is a proxy representing RichText.
type RichTextProxy struct {
	*json.RichText
	context *change.Context
}

// NewRichTextProxy creates a new instance of RichTextProxy.
func NewRichTextProxy(ctx *change.Context, text *json.RichText) *RichTextProxy {
	return &RichTextProxy{
		RichText: text,
		context:  ctx,
	}
}

// Edit edits the given range with the given content and attributes.
func (p *RichTextProxy) Edit(from, to int, content string, attributes map[string]string) *RichTextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)
	log.Logger.Debugf(
		"EDIT: f:%d->%s, t:%d->%s, c:%s, attrs:%v",
		from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), content,
	)

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.RichText.Edit(
		fromPos,
		toPos,
		nil,
		content,
		attributes,
		ticket,
	)

	p.context.Push(operation.NewRichEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		attributes,
		ticket,
	))
	if fromPos.Compare(toPos) != 0 {
		p.context.RegisterRemovedNodeTextElement(p)
	}

	return p
}

// SetStyle applies the style of the given range.
func (p *RichTextProxy) SetStyle(from, to int, attributes map[string]string) *RichTextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)
	log.Logger.Debugf(
		"STYL: f:%d->%s, t:%d->%s, attrs:%v",
		from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), attributes,
	)

	ticket := p.context.IssueTimeTicket()
	p.RichText.SetStyle(
		fromPos,
		toPos,
		attributes,
		ticket,
	)

	p.context.Push(operation.NewStyle(
		p.CreatedAt(),
		fromPos,
		toPos,
		attributes,
		ticket,
	))

	return p
}
