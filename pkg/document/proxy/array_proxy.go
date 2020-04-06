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
	time2 "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

type ArrayProxy struct {
	*json.Array
	context *change.Context
}

func NewArrayProxy(ctx *change.Context, array *json.Array) *ArrayProxy {
	return &ArrayProxy{
		Array:   array,
		context: ctx,
	}
}

func (p *ArrayProxy) AddBool(v bool) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddInteger(v int) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddLong(v int64) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddDouble(v float64) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddBytes(v []byte) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddDate(v time2.Time) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddString(v string) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) InsertStringAfter(index int, v string) *ArrayProxy {
	prev := p.Get(index)
	p.insertAfterInternal(prev.CreatedAt(), func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddNewArray() *ArrayProxy {
	v := p.addInternal(func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGA(), ticket))
	})

	return v.(*ArrayProxy)
}

func (p *ArrayProxy) Remove(idx int) json.Element {
	if p.Len() <= idx {
		log.Logger.Warnf("the given index is out of bound: %d", idx)
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	removed := p.Array.Remove(idx, ticket)
	p.context.Push(operation.NewRemove(
		p.CreatedAt(),
		removed.CreatedAt(),
		ticket,
	))

	return removed
}

func (p *ArrayProxy) addInternal(
	creator func(ticket *time.Ticket) json.Element,
) json.Element {
	return p.insertAfterInternal(p.Array.LastCreatedAt(), creator)
}

func (p *ArrayProxy) insertAfterInternal(
	prevCreatedAt *time.Ticket,
	creator func(ticket *time.Ticket) json.Element,
) json.Element {
	ticket := p.context.IssueTimeTicket()
	proxy := creator(ticket)
	value := toOriginal(proxy)

	p.context.Push(operation.NewAdd(
		p.Array.CreatedAt(),
		prevCreatedAt,
		value.DeepCopy(),
		ticket,
	))

	p.InsertAfter(prevCreatedAt, value)
	p.context.RegisterElement(value)

	return proxy
}

func (p *ArrayProxy) Len() int {
	return p.Array.Len()
}

