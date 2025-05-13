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

// Object represents an object in the document. As a proxy for the CRDT object,
// it is used when the user manipulates the object from the outside.
type Object struct {
	*crdt.Object
	context *change.Context
}

// NewObject creates a new instance of Object.
func NewObject(ctx *change.Context, root *crdt.Object) *Object {
	return &Object{
		Object:  root,
		context: ctx,
	}
}

// SetYSON sets values from the given YSON.
func (p *Object) SetYSON(value interface{}) {
	yObj, ok := value.(yson.Object)
	if !ok {
		panic(fmt.Errorf("expected JSONObjectStruct, got %T", value))
	}

	for k, v := range yObj {
		p.SetYSONElement(k, v)
	}
}

// SetNewObject sets a new Object for the given key. It accepts an optional
// yson.Object that will be used to initialize the object.
func (p *Object) SetNewObject(k string, v ...yson.Object) *Object {
	value := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		return NewObject(p.context, crdt.NewObject(crdt.NewElementRHT(), ticket))
	})
	obj := value.(*Object)

	if len(v) > 0 {
		for k, v := range v[0] {
			obj.SetYSONElement(k, v)
		}
	}

	return obj
}

// SetNewArray sets a new Array for the given key. It accepts an optional
// yson.Array that will be used to initialize the array.
func (p *Object) SetNewArray(k string, v ...yson.Array) *Array {
	value := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		return NewArray(p.context, crdt.NewArray(crdt.NewRGATreeList(), ticket))
	})
	arr := value.(*Array)

	if len(v) > 0 {
		for _, elem := range v[0] {
			arr.AddYSON(elem)
		}
	}

	return arr
}

// SetNewText sets a new Text for the given key.
func (p *Object) SetNewText(k string) *Text {
	v := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		text := NewText()
		return text.Initialize(p.context, crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket))
	})

	return v.(*Text)
}

// SetNewCounter sets a new NewCounter for the given key.
func (p *Object) SetNewCounter(k string, t crdt.CounterType, n any) *Counter {
	v := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		crdtCounter, err := crdt.NewCounter(t, n, ticket)
		if err != nil {
			panic(err)
		}

		return NewCounter(n, t).Initialize(p.context, crdtCounter)
	})

	return v.(*Counter)
}

// SetNewTree sets a new Tree for the given key.
func (p *Object) SetNewTree(k string, initialRoot ...TreeNode) *Tree {
	v := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		var root *TreeNode
		if len(initialRoot) > 0 {
			root = &initialRoot[0]
		}
		tree := NewTree(root)
		return tree.Initialize(p.context, crdt.NewTree(buildRoot(p.context, root, ticket), ticket))
	})

	return v.(*Tree)
}

// SetNull sets the null for the given key.
func (p *Object) SetNull(k string) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetBool sets the given boolean for the given key.
func (p *Object) SetBool(k string, v bool) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetInteger sets the given integer for the given key.
func (p *Object) SetInteger(k string, v int) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetLong sets the given long for the given key.
func (p *Object) SetLong(k string, v int64) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetDouble sets the given double for the given key.
func (p *Object) SetDouble(k string, v float64) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetString sets the given string for the given key.
func (p *Object) SetString(k, v string) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetBytes sets the given bytes for the given key.
func (p *Object) SetBytes(k string, v []byte) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetDate sets the given date for the given key.
func (p *Object) SetDate(k string, v gotime.Time) *Object {
	p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	})

	return p
}

// SetYSONElement sets the given YSON for the given key.
func (p *Object) SetYSONElement(k string, v interface{}) *Object {
	switch y := v.(type) {
	case yson.Counter:
		p.SetNewCounter(k, y.Type, y.Value)
	case yson.Array:
		arr := p.SetNewArray(k)
		for _, elem := range y {
			arr.AddYSON(elem)
		}
	case yson.Object:
		o := p.SetNewObject(k)
		for key, value := range y {
			o.SetYSONElement(key, value)
		}
	case yson.Text:
		text := p.SetNewText(k)
		text.EditFromYSON(y)
	case yson.Tree:
		p.SetNewTree(k, y.Root)
	default:
		switch v := v.(type) {
		case nil:
			p.SetNull(k)
		case bool:
			p.SetBool(k, v)
		case int32:
			p.SetInteger(k, int(v))
		case int64:
			p.SetLong(k, v)
		case float64:
			p.SetDouble(k, v)
		case string:
			p.SetString(k, v)
		case []byte:
			p.SetBytes(k, v)
		case gotime.Time:
			p.SetDate(k, v)
		default:
			panic(fmt.Errorf("unsupported primitive type: %v", v))
		}
	}
	return p
}

// Delete deletes the value of the given key.
func (p *Object) Delete(k string) crdt.Element {
	if !p.Object.Has(k) {
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted := p.Object.Delete(k, ticket)
	p.context.Push(operations.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted
}

// GetObject returns Object of the given key.
func (p *Object) GetObject(k string) *Object {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Object:
		return NewObject(p.context, elem)
	case *Object:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetArray returns Array of the given key.
func (p *Object) GetArray(k string) *Array {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Array:
		return NewArray(p.context, elem)
	case *Array:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetText returns Text of the given key.
func (p *Object) GetText(k string) *Text {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Text:
		text := NewText()
		return text.Initialize(p.context, elem)
	case *Text:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetCounter returns Counter of the given key.
func (p *Object) GetCounter(k string) *Counter {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Counter:
		counter := NewCounter(elem.Value(), elem.ValueType())
		return counter.Initialize(p.context, elem)
	case *Counter:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetTree returns Tree of the given key.
func (p *Object) GetTree(k string) *Tree {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Tree:
		tree := NewTree()
		return tree.Initialize(p.context, elem)
	case *Tree:
		return elem
	default:
		panic("unsupported type")
	}
}

func (p *Object) setInternal(
	k string,
	creator func(ticket *time.Ticket) crdt.Element,
) crdt.Element {
	ticket := p.context.IssueTimeTicket()
	elem := creator(ticket)
	value := toOriginal(elem)

	copiedValue, err := value.DeepCopy()
	if err != nil {
		panic(err)
	}

	removed := p.Set(k, value)
	p.context.RegisterElement(value)
	if removed != nil {
		p.context.RegisterRemovedElementPair(p, removed)
	}

	p.context.Push(operations.NewSet(
		p.CreatedAt(),
		k,
		copiedValue,
		ticket,
	))

	return elem
}
