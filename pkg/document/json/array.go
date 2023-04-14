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
	"fmt"
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
func (p *Array) AddNull() (*Array, error) {
	_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			return nil, fmt.Errorf("json array add null: %w", err)
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json array add null: %w", err)
	}

	return p, nil
}

// AddBool adds the given boolean at the last.
func (p *Array) AddBool(values ...bool) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add bool: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add bool: %w", err)
		}
	}

	return p, nil
}

// AddInteger adds the given integer at the last.
func (p *Array) AddInteger(values ...int) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add integer: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add integer: %w", err)
		}
	}

	return p, nil
}

// AddLong adds the given long at the last.
func (p *Array) AddLong(values ...int64) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add long: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add long: %w", err)
		}
	}

	return p, nil
}

// AddDouble adds the given double at the last.
func (p *Array) AddDouble(values ...float64) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add double: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add double: %w", err)
		}
	}

	return p, nil
}

// AddString adds the given string at the last.
func (p *Array) AddString(values ...string) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add string: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add string: %w", err)
		}
	}

	return p, nil
}

// AddBytes adds the given bytes at the last.
func (p *Array) AddBytes(values ...[]byte) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add bytes: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add bytes: %w", err)
		}
	}

	return p, nil
}

// AddDate adds the given date at the last.
func (p *Array) AddDate(values ...gotime.Time) (*Array, error) {
	for _, value := range values {
		_, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				return nil, fmt.Errorf("json array add date: %w", err)
			}
			return primitive, nil
		})
		if err != nil {
			return nil, fmt.Errorf("json array add date: %w", err)
		}
	}

	return p, nil
}

// AddNewArray adds a new array at the last.
func (p *Array) AddNewArray() (*Array, error) {
	v, err := p.addInternal(func(ticket *time.Ticket) (crdt.Element, error) {
		elements, err := crdt.NewRGATreeList()
		if err != nil {
			return nil, fmt.Errorf("json array add array: %w", err)
		}
		return NewArray(p.context, crdt.NewArray(elements, ticket)), nil
	})
	if err != nil {
		return nil, fmt.Errorf("json array add array: %w", err)
	}

	return v.(*Array), nil
}

// MoveBefore moves the given element to its new position before the given next element.
func (p *Array) MoveBefore(nextCreatedAt, createdAt *time.Ticket) error {
	if err := p.moveBeforeInternal(nextCreatedAt, createdAt); err != nil {
		return fmt.Errorf("json array move before: %w", err)
	}
	return nil
}

// InsertIntegerAfter inserts the given integer after the given previous element.
func (p *Array) InsertIntegerAfter(index int, v int) (*Array, error) {
	elem, err := p.Get(index)
	if err != nil {
		return nil, fmt.Errorf("json array insert integer after: %w", err)
	}
	createdAt := elem.CreatedAt()
	_, err = p.insertAfterInternal(createdAt, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, fmt.Errorf("json array insert integer after: %w", err)
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json array insert integer after: %w", err)
	}

	return p, nil
}

// Delete deletes the element of the given index.
func (p *Array) Delete(idx int) (crdt.Element, error) {
	if p.Len() <= idx {
		return nil, nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted, err := p.Array.Delete(idx, ticket)
	if err != nil {
		return nil, fmt.Errorf("json array delete: %w", err)
	}
	p.context.Push(operations.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted, nil
}

// Len returns length of this Array.
func (p *Array) Len() int {
	return p.Array.Len()
}

func (p *Array) addInternal(
	creator func(ticket *time.Ticket) (crdt.Element, error),
) (crdt.Element, error) {
	elem, err := p.insertAfterInternal(p.Array.LastCreatedAt(), creator)
	if err != nil {
		return nil, fmt.Errorf("json array add internal: %w", err)
	}
	return elem, nil
}

func (p *Array) insertAfterInternal(
	prevCreatedAt *time.Ticket,
	creator func(ticket *time.Ticket) (crdt.Element, error),
) (crdt.Element, error) {
	ticket := p.context.IssueTimeTicket()
	elem, err := creator(ticket)
	if err != nil {
		return nil, fmt.Errorf("json array insert after internal: %w", err)
	}
	value := toOriginal(elem)
	copiedValue, err := value.DeepCopy()
	if err != nil {
		return nil, fmt.Errorf("json array insert after internal: %w", err)
	}
	p.context.Push(operations.NewAdd(
		p.Array.CreatedAt(),
		prevCreatedAt,
		copiedValue,
		ticket,
	))

	if err := p.InsertAfter(prevCreatedAt, value); err != nil {
		return nil, fmt.Errorf("json array insert after internal: %w", err)
	}
	p.context.RegisterElement(value)

	return elem, nil
}

func (p *Array) moveBeforeInternal(nextCreatedAt, createdAt *time.Ticket) error {
	ticket := p.context.IssueTimeTicket()

	prevCreatedAt, err := p.FindPrevCreatedAt(nextCreatedAt)
	if err != nil {
		return fmt.Errorf("json array move before internal: %w", err)
	}

	p.context.Push(operations.NewMove(
		p.Array.CreatedAt(),
		prevCreatedAt,
		createdAt,
		ticket,
	))

	if err := p.MoveAfter(prevCreatedAt, createdAt, ticket); err != nil {
		return fmt.Errorf("json array move before internal: %w", err)
	}
	return nil
}
