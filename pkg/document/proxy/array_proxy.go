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
	gotime "time"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ArrayProxy is a proxy representing Array.
type ArrayProxy struct {
	*json.Array
	context *change.Context
}

// NewArrayProxy creates a new instance of ArrayProxy.
func NewArrayProxy(ctx *change.Context, array *json.Array) *ArrayProxy {
	return &ArrayProxy{
		Array:   array,
		context: ctx,
	}
}

// AddNull adds the null at the last.
func (p *ArrayProxy) AddNull() *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(nil, ticket)
	})

	return p
}

// AddBool adds the given boolean at the last.
func (p *ArrayProxy) AddBool(values ...bool) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddInteger adds the given integer at the last.
func (p *ArrayProxy) AddInteger(values ...int) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddLong adds the given long at the last.
func (p *ArrayProxy) AddLong(values ...int64) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddDouble adds the given double at the last.
func (p *ArrayProxy) AddDouble(values ...float64) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddString adds the given string at the last.
func (p *ArrayProxy) AddString(values ...string) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddBytes adds the given bytes at the last.
func (p *ArrayProxy) AddBytes(values ...[]byte) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddDate adds the given date at the last.
func (p *ArrayProxy) AddDate(values ...gotime.Time) *ArrayProxy {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) json.Element {
			return json.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddNewArray adds a new array at the last.
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

// InsertIntegerAfter inserts the given integer after the given previous
// element.
func (p *ArrayProxy) InsertIntegerAfter(index int, v int) *ArrayProxy {
	p.insertAfterInternal(p.Get(index).CreatedAt(), func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetNull sets the given null at the index.
func (p *ArrayProxy) SetNull(idx int) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(nil, ticket)
	})

	return p
}

// SetBool sets the given boolean at the index.
func (p *ArrayProxy) SetBool(idx int, value bool) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetInteger sets the given integer at the index.
func (p *ArrayProxy) SetInteger(idx int, value int) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetLong sets the given long at the index.
func (p *ArrayProxy) SetLong(idx int, value int64) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetDouble sets the given double at the index.
func (p *ArrayProxy) SetDouble(idx int, value float64) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetString sets the given string at the index.
func (p *ArrayProxy) SetString(idx int, value string) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetBytes sets the given bytes at the index.
func (p *ArrayProxy) SetBytes(idx int, value []byte) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetDate sets the given date at the index.
func (p *ArrayProxy) SetDate(idx int, value gotime.Time) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(value, ticket)
	})

	return p
}

// SetNewArray sets a new array at the index.
func (p *ArrayProxy) SetNewArray(idx int) *ArrayProxy {
	p.setInternal(idx, func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGATreeList(), ticket))
	})

	return p
}

// Delete deletes the element of the given index.
func (p *ArrayProxy) Delete(idx int) json.Element {
	if p.Len() <= idx || idx < 0 {
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
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted
}

// Len returns length of this Array.
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

func (p *ArrayProxy) setInternal(
	idx int,
	creator func(ticket *time.Ticket) json.Element,
) json.Element {
	if p.Len() <= idx || idx < 0 {
		log.Logger.Warnf("the given index is out of bound: %d", idx)
		return nil
	}

	positionAt := p.Array.Get(idx).CreatedAt()
	ticket := p.context.IssueTimeTicket()
	elem := creator(ticket)

	p.context.Push(operation.NewSetByIndex(
		p.CreatedAt(),
		positionAt,
		elem.DeepCopy(),
		ticket,
	))

	deleted := p.SetByIndex(positionAt, elem)
	p.context.RegisterElement(elem)
	if deleted != nil {
		p.context.RegisterRemovedElementPair(p, deleted)
	}

	return elem
}
