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
	"reflect"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/yson"
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
func (p *Array) AddNewTree(initialRoot ...*TreeNode) *Tree {
	v := p.addInternal(func(ticket *time.Ticket) crdt.Element {
		var root *TreeNode
		if len(initialRoot) > 0 {
			root = initialRoot[0]
		}
		tree := NewTree(root)
		return tree.Initialize(p.context, crdt.NewTree(buildRoot(p.context, root, ticket), ticket))
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

// AddArrFromYSON adds an element from the given YSON at the last.
func (p *Array) AddArrFromYSON(value yson.YSON) *Array {
	switch j := value.(type) {
	case *yson.Primitive:
		switch j.ValueType {
		case crdt.Null:
			p.AddNull()
		case crdt.Boolean:
			p.AddBool(j.Value.(bool))
		case crdt.Integer:
			p.AddInteger(int(j.Value.(int32)))
		case crdt.Long:
			p.AddLong(j.Value.(int64))
		case crdt.Double:
			p.AddDouble(j.Value.(float64))
		case crdt.String:
			p.AddString(j.Value.(string))
		case crdt.Bytes:
			p.AddBytes(j.Value.([]byte))
		case crdt.Date:
			p.AddDate(j.Value.(gotime.Time))
		default:
			panic(fmt.Errorf("unsupported primitive type: %T", j))
		}
	case *yson.Counter:
		p.AddNewCounter(j.ValueType, j.Value)
	case *yson.Array:
		a := p.AddNewArray()
		for _, elem := range j.Value {
			a.AddArrFromYSON(elem)
		}
	case *yson.Object:
		o := p.AddNewObject()
		for key, value := range j.Value {
			o.SetObjFromYSON(key, value)
		}
	case *yson.Text:
		t := p.AddNewText()
		t.EditFromYSON(*j)
	case *yson.Tree:
		treeNode, err := GetTreeRootNodeFromYSON(*j)
		if err != nil {
			panic(err)
		}
		p.AddNewTree(treeNode)
	default:
		panic(fmt.Errorf("unsupported YSON type: %T", j))
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

func (p *Array) setByIndexInternal(
	createdAt *time.Ticket,
	creator func(ticket *time.Ticket) crdt.Element,
) crdt.Element {
	ticket := p.context.IssueTimeTicket()
	// NOTE(junseo): It uses `creator(createdAt)` instead of `creator(ticket)`
	// because the new element must have the same `createdAt` as the old element.
	elem := creator(createdAt)
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

// buildArrayElements return the element slice of the given array.
// Because the type of the given array is `any`, it is necessary to type assertion.
func buildArrayElements(
	context *change.Context,
	elements any,
	stat *buildState,
) []crdt.Element {
	// 01. The type of elements of the given array is one of the basic types.
	switch elements := elements.(type) {
	case []any:
		return sliceToElements[any](elements, context, stat)
	case []int:
		return sliceToElements[int](elements, context, stat)
	case []int32:
		return sliceToElements[int32](elements, context, stat)
	case []int64:
		return sliceToElements[int64](elements, context, stat)
	case []float32:
		return sliceToElements[float32](elements, context, stat)
	case []float64:
		return sliceToElements[float64](elements, context, stat)
	case []string:
		return sliceToElements[string](elements, context, stat)
	case []bool:
		return sliceToElements[bool](elements, context, stat)
	case [][]byte:
		return sliceToElements[[]byte](elements, context, stat)
	case []gotime.Time:
		return sliceToElements[gotime.Time](elements, context, stat)
	case []Counter:
		return sliceToElements[Counter](elements, context, stat)
	case []Text:
		return sliceToElements[Text](elements, context, stat)
	case []Tree:
		return sliceToElements[Tree](elements, context, stat)
	case []map[string]any:
		return sliceToElements[map[string]any](elements, context, stat)
	}

	// 02. The type of elements of the given array is user defined struct or array.
	switch reflect.ValueOf(elements).Type().Elem().Kind() {
	case reflect.Struct:
		length := reflect.ValueOf(elements).Len()
		array := make([]reflect.Value, length)

		// NOTE(highcloud100): The structure cannot immediately call Interface()
		// because it can have an unexposed field. If we call Interface(), panic will occur.
		for i := 0; i < length; i++ {
			array[i] = reflect.ValueOf(elements).Index(i)
		}

		return sliceToElements[reflect.Value](array, context, stat)
	case reflect.Slice, reflect.Array, reflect.Ptr:
		length := reflect.ValueOf(elements).Len()
		array := make([]any, length)

		for i := 0; i < length; i++ {
			array[i] = reflect.ValueOf(elements).Index(i).Interface()
		}
		return sliceToElements[any](array, context, stat)
	default:
		panic("unhandled default case")
	}
}

// sliceToElements converts the given specific type array to crdt.Element array
func sliceToElements[T any](elements []T, context *change.Context, stat *buildState) []crdt.Element {
	elems := make([]crdt.Element, len(elements))

	for idx, value := range elements {
		ticket := context.IssueTimeTicket()
		elems[idx] = buildCRDTElement(context, value, ticket, stat)
	}

	return elems
}
