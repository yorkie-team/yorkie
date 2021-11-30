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
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
)

// TextProxy is a proxy representing Text.
type TextProxy struct {
	*json.Text
	context *change.Context
}

// NewTextProxy creates a new instance of TextProxy.
func NewTextProxy(ctx *change.Context, text *json.Text) *TextProxy {
	return &TextProxy{
		Text:    text,
		context: ctx,
	}
}

// Edit edits the given range with the given content.
func (p *TextProxy) Edit(from, to int, content string) *TextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.Text.CreateRange(from, to)

	if log.Core.Enabled(zap.DebugLevel) {
		log.Logger.Debugf(
			"EDIT: f:%d->%s, t:%d->%s c:%s",
			from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(), content,
		)
	}

	ticket := p.context.IssueTimeTicket()
	_, maxCreationMapByActor := p.Text.Edit(
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
	if !fromPos.Equal(toPos) {
		p.context.RegisterTextElementWithGarbage(p)
	}
	return p
}

// Select stores that the given range has been selected.
func (p *TextProxy) Select(from, to int) *TextProxy {
	if from > to {
		panic("from should be less than or equal to to")
	}
	fromPos, toPos := p.Text.CreateRange(from, to)

	if log.Core.Enabled(zap.DebugLevel) {
		log.Logger.Debugf(
			"SELT: f:%d->%s, t:%d->%s",
			from, fromPos.AnnotatedString(), to, toPos.AnnotatedString(),
		)
	}

	ticket := p.context.IssueTimeTicket()
	p.Text.Select(
		fromPos,
		toPos,
		ticket,
	)

	p.context.Push(operation.NewSelect(
		p.CreatedAt(),
		fromPos,
		toPos,
		ticket,
	))
	return p
}
