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
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/log"
)

type RichTextProxy struct {
	*json.RichText
	context *change.Context
}

func NewRichTextProxy(ctx *change.Context, text *json.RichText) *RichTextProxy {
	return &RichTextProxy{
		RichText:    text,
		context: ctx,
	}
}

func (p *RichTextProxy) Edit(from, to int, content string) *RichTextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)
	log.Logger.Debugf(
		"EDIT: f:%d->%s, t:%d->%s c:%s",
		from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), content,
	)

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.RichText.Edit(
		fromPos,
		toPos,
		nil,
		content,
		ticket,
	)

	p.context.Push(operation.NewEdit(
		p.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		content,
		ticket,
	))

	return p
}

func (p *RichTextProxy) SetStyle(from, to int, key, value string) *RichTextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.RichText.CreateRange(from, to)
	log.Logger.Debugf(
		"STYL: f:%d->%s, t:%d->%s k:%s, v:%s",
		from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), key, value,
	)

	ticket := p.context.IssueTimeTicket()
	p.RichText.SetStyle(
		fromPos,
		toPos,
		key,
		value,
		ticket,
	)

	p.context.Push(operation.NewStyle(
		p.CreatedAt(),
		fromPos,
		toPos,
		key,
		value,
		ticket,
	))

	return p
}
