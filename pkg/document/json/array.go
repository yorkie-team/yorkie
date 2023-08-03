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
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Array represents an array in the document. As a proxy for the CRDT array,
// it is used when the user manipulate the array from the outside.
type Array struct {
	*crdt.Array
	context *change.Context
}

// NewArray creates a new instance of Array.
func NewArray(ctx *change.Context, array *crdt.Array) *Array {
	return &Array{
		Array:   array,
		context: ctx,
	}
}

// AddNull adds the null at the last.
func (p *Array) AddNull() *Array {
	p.addInternal(func(ticket *time.Ticket) crdt.Element {
		return crdt.NewPrimitive(nil, ticket)
	})

	return p
}

// AddBool adds the given boolean at the last.
func (p *Array) AddBool(values ...bool) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddInteger adds the given integer at the last.
func (p *Array) AddInteger(values ...int) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddLong adds the given long at the last.
func (p *Array) AddLong(values ...int64) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddDouble adds the given double at the last.
func (p *Array) AddDouble(values ...float64) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddString adds the given string at the last.
func (p *Array) AddString(values ...string) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddBytes adds the given bytes at the last.
func (p *Array) AddBytes(values ...[]byte) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddDate adds the given date at the last.
func (p *Array) AddDate(values ...gotime.Time) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			return crdt.NewPrimitive(value, ticket)
		})
	}

	return p
}

// AddNewArray adds a new array at the last.
func (p *Array) AddNewArray() *Array {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		return NewArray(p.context, crdt.NewArray(crdt.NewRGATreeList(), ticket))
	})

	return v.(*Array)
}

// MoveBefore moves the given element to its new position before the given next element.
func (p *Array) MoveBefore(nextCreatedAt, createdAt *time.Ticket) {
	p.moveBeforeInternal(nextCreatedAt, createdAt)
}

// InsertIntegerAfter inserts the given integer after the given previous
// element.
func (p *Array) InsertIntegerAfter(index int, v int) *Array {
	p.insertAfterInternal(p.Get(index).CreatedAt(), func(ticket *time.Ticket) crdt.Element {
		return crdt.NewPrimitive(v, ticket)
	})

	return p
}

// Get element of the given index.
func (p *Array) Get(idx int) crdt.Element {
	if p.Len() <= idx {
		return nil
	}

	element, err := p.Array.Get(idx)
	if err != nil {
		panic(err)
	}

	return element
}

// Delete deletes the element of the given index.
func (p *Array) Delete(idx int) crdt.Element {
	if p.Len() <= idx {
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted, err := p.Array.Delete(idx, ticket)
	if err != nil {
		panic(err)
	}
	p.context.Push(operations.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted
}

// Len returns length of this Array.
func (p *Array) Len() int {
	return p.Array.Len()
}

func (p *Array) addInternal(
	creator func(ticket *time.Ticket) crdt.Element,
) crdt.Element {
	return p.insertAfterInternal(p.Array.LastCreatedAt(), creator)
}

func (p *Array) insertAfterInternal(
	prevCreatedAt *time.Ticket,
	creator func(ticket *time.Ticket) crdt.Element,
) crdt.Element {
	ticket := p.context.IssueTimeTicket()
	elem := creator(ticket)
	value := toOriginal(elem)

	copiedValue, err := value.DeepCopy()
	if err != nil {
		panic(err)
	}
	p.context.Push(operations.NewAdd(
		p.Array.CreatedAt(),
		prevCreatedAt,
		copiedValue,
		ticket,
	))

	if err = p.InsertAfter(prevCreatedAt, value); err != nil {
		panic(err)
	}
	p.context.RegisterElement(value)

	return elem
}

func (p *Array) moveBeforeInternal(nextCreatedAt, createdAt *time.Ticket) {
	ticket := p.context.IssueTimeTicket()

	prevCreatedAt, err := p.FindPrevCreatedAt(nextCreatedAt)
	if err != nil {
		panic(err)
	}

	p.context.Push(operations.NewMove(
		p.Array.CreatedAt(),
		prevCreatedAt,
		createdAt,
		ticket,
	))

	if err = p.MoveAfter(prevCreatedAt, createdAt, ticket); err != nil {
		panic(err)
	}
}
