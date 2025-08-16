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
	"github.com/yorkie-team/yorkie/pkg/document/yson"
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
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// AddBool adds the given boolean at the last.
func (p *Array) AddBool(values ...bool) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddInteger adds the given integer at the last.
func (p *Array) AddInteger(values ...int) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddLong adds the given long at the last.
func (p *Array) AddLong(values ...int64) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddDouble adds the given double at the last.
func (p *Array) AddDouble(values ...float64) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddString adds the given string at the last.
func (p *Array) AddString(values ...string) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddBytes adds the given bytes at the last.
func (p *Array) AddBytes(values ...[]byte) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddDate adds the given date at the last.
func (p *Array) AddDate(values ...gotime.Time) *Array {
	for _, value := range values {
		p.addInternal(func(ticket *time.Ticket) crdt.Element {
			primitive, err := crdt.NewPrimitive(value, ticket)
			if err != nil {
				panic(err)
			}
			return primitive
		})
	}
	return p
}

// AddNewArray adds a new array at the last.
func (p *Array) AddNewArray() *Array {
	elements := crdt.NewRGATreeList()
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		return NewArray(p.context, crdt.NewArray(elements, ticket))
	})

	return v.(*Array)
}

// AddNewCounter adds a new counter at the last.
func (p *Array) AddNewCounter(valueType crdt.CounterType, value interface{}) *Counter {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		counter, err := crdt.NewCounter(valueType, value, ticket)
		if err != nil {
			panic(err)
		}
		return NewCounter(value, valueType).Initialize(p.context, counter)
	})
	return v.(*Counter)
}

// AddNewText adds a new text at the last.
func (p *Array) AddNewText() *Text {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		text := NewText()
		return text.Initialize(p.context, crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket))
	})
	return v.(*Text)
}

// AddNewTree adds a new tree at the last.
func (p *Array) AddNewTree(initialRoot ...TreeNode) *Tree {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		var root TreeNode
		if len(initialRoot) > 0 {
			root = initialRoot[0]
		}
		tree := NewTree(&root)
		return tree.Initialize(p.context, crdt.NewTree(buildRoot(p.context, &root, ticket), ticket))
	})
	return v.(*Tree)
}

// AddNewObject adds a new object at the last.
func (p *Array) AddNewObject() *Object {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		return NewObject(p.context, crdt.NewObject(crdt.NewElementRHT(), ticket))
	})
	return v.(*Object)
}

// AddYSON adds the given YSON element to the array.
func (p *Array) AddYSON(value interface{}) *Array {
	switch y := value.(type) {
	case yson.Counter:
		p.AddNewCounter(y.Type, y.Value)
	case yson.Array:
		a := p.AddNewArray()
		for _, elem := range y {
			a.AddYSON(elem)
		}
	case yson.Object:
		o := p.AddNewObject()
		for key, value := range y {
			o.SetYSONElement(key, value)
		}
	case yson.Text:
		t := p.AddNewText()
		t.EditFromYSON(y)
	case yson.Tree:
		p.AddNewTree(y.Root)
	default:
		switch v := y.(type) {
		case nil:
			p.AddNull()
		case bool:
			p.AddBool(v)
		case int32:
			p.AddInteger(int(v))
		case int64:
			p.AddLong(v)
		case float64:
			p.AddDouble(v)
		case string:
			p.AddString(v)
		case []byte:
			p.AddBytes(v)
		case gotime.Time:
			p.AddDate(v)
		default:
			panic(fmt.Errorf("unsupported primitive type: %v", v))
		}
	}

	return p
}

// MoveBefore moves the given element to its new position before the given next element.
func (p *Array) MoveBefore(nextCreatedAt, createdAt *time.Ticket) {
	p.moveBeforeInternal(nextCreatedAt, createdAt)
}

// MoveAfterByIndex moves the given element to its new position after the given previous element.
func (p *Array) MoveAfterByIndex(prevIndex, targetIndex int) {
	prev := p.Get(prevIndex)
	target := p.Get(targetIndex)
	if prev == nil || target == nil {
		panic("index out of bound")
	}
	p.moveAfterInternal(prev.CreatedAt(), target.CreatedAt())
}

// InsertIntegerAfter inserts the given integer after the given previous
// element.
func (p *Array) InsertIntegerAfter(index int, v int) *Array {
	prev := p.Get(index)
	if prev == nil {
		panic("index out of bound")
	}
	p.insertAfterInternal(prev.CreatedAt(), func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// Get element of the given index.
func (p *Array) Get(idx int) crdt.Element {
	if idx < 0 || p.Len() <= idx {
		return nil
	}

	element, err := p.Array.Get(idx)
	if err != nil {
		panic(err)
	}

	return element
}

// GetObject returns Object of the given index.
func (p *Array) GetObject(idx int) *Object {
	element := p.Get(idx)
	if element == nil {
		return nil
	}

	switch elem := element.(type) {
	case *crdt.Object:
		return NewObject(p.context, elem)
	case *Object:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetArray returns Array of the given index.
func (p *Array) GetArray(idx int) *Array {
	element := p.Get(idx)
	if element == nil {
		return nil
	}

	switch elem := element.(type) {
	case *crdt.Array:
		return NewArray(p.context, elem)
	case *Array:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetText returns Text of the given index.
func (p *Array) GetText(idx int) *Text {
	element := p.Get(idx)
	if element == nil {
		return nil
	}

	switch elem := element.(type) {
	case *crdt.Text:
		text := NewText()
		return text.Initialize(p.context, elem)
	case *Text:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetCounter returns Counter of the given index.
func (p *Array) GetCounter(idx int) *Counter {
	element := p.Get(idx)
	if element == nil {
		return nil
	}

	switch elem := element.(type) {
	case *crdt.Counter:
		counter := NewCounter(elem.Value(), elem.ValueType())
		return counter.Initialize(p.context, elem)
	case *Counter:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetTree returns Tree of the given index.
func (p *Array) GetTree(idx int) *Tree {
	element := p.Get(idx)
	if element == nil {
		return nil
	}

	switch elem := element.(type) {
	case *crdt.Tree:
		tree := NewTree()
		return tree.Initialize(p.context, elem)
	case *Tree:
		return elem
	default:
		panic("unsupported type")
	}
}

// SetInteger sets element of the given index.
func (p *Array) SetInteger(idx int, value int) *Array {
	target := p.Get(idx)
	if target == nil {
		panic("index out of bound")
	}

	p.setByIndexInternal(target.CreatedAt(), func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(value, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})
	return p
}

// Delete deletes the element of the given index.
func (p *Array) Delete(idx int) crdt.Element {
	if idx < 0 || p.Len() <= idx {
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

	if err = p.InsertAfter(prevCreatedAt, value, nil); err != nil {
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

func (p *Array) moveAfterInternal(prevCreatedAt, createdAt *time.Ticket) {
	ticket := p.context.IssueTimeTicket()

	p.context.Push(operations.NewMove(
		p.Array.CreatedAt(),
		prevCreatedAt,
		createdAt,
		ticket,
	))

	if err := p.MoveAfter(prevCreatedAt, createdAt, ticket); err != nil {
		panic(err)
	}
}

func (p *Array) MoveFront(createdAt *time.Ticket) {
	p.moveBeforeInternal(p.Get(0).CreatedAt(), createdAt)
}

func (p *Array) MoveLast(createdAt *time.Ticket) {
	p.moveAfterInternal(p.LastCreatedAt(), createdAt)
}

func (p *Array) setByIndexInternal(
	createdAt *time.Ticket,
	creator func(ticket *time.Ticket) crdt.Element,
) crdt.Element {
	ticket := p.context.IssueTimeTicket()
	elem := creator(ticket)
	value := toOriginal(elem)

	copiedValue, err := value.DeepCopy()
	if err != nil {
		panic(err)
	}
	p.context.Push(operations.NewArraySet(
		p.Array.CreatedAt(),
		createdAt,
		copiedValue,
		ticket,
	))

	_, err = p.Set(createdAt, value, ticket)
	if err != nil {
		panic(err)
	}
	// TODO(junseo): GC logic is not implemented here
	// because there is no way to distinguish between old and new element with same `createdAt`.
	p.context.RegisterElement(value)
	return elem
}
