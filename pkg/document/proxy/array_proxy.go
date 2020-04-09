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

func (p *ArrayProxy) AddBool(values ...bool) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddInteger(values ...int) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddLong(values ...int64) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddDouble(values ...float64) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddString(values ...string) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddBytes(values ...[]byte) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddDate(values ...time2.Time) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

func (p *ArrayProxy) AddNewArray() *ArrayProxy {
	v := p.addInternal(func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGATreeList(), ticket))
	})

	return v.(*ArrayProxy)
}

// MoveBefore moves the given element to its new position before the given next element.
func (p *ArrayProxy) MoveBefore(nextCreatedAt, createdAt *time.Ticket) {
	p.moveBeforeInternal(nextCreatedAt, createdAt)
}

func (p *ArrayProxy) InsertIntegerAfter(index int, v int) *ArrayProxy {
	p.insertAfterInternal(p.Get(index).CreatedAt(), func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) Delete(idx int) json.Element {
	if p.Len() <= idx {
		log.Logger.Warnf("the given index is out of bound: %d", idx)
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted := p.Array.Delete(idx, ticket)
	p.context.Push(operation.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))

	return deleted
}

func (p *ArrayProxy) Len() int {
	return p.Array.Len()
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

func (p *ArrayProxy) moveBeforeInternal(nextCreatedAt, createdAt *time.Ticket) {
	ticket := p.context.IssueTimeTicket()

	prevCreatedAt := p.FindPrevCreatedAt(nextCreatedAt)

	p.context.Push(operation.NewMove(
		p.Array.CreatedAt(),
		prevCreatedAt,
		createdAt,
		ticket,
	))

	p.MoveAfter(prevCreatedAt, createdAt, ticket)
}
