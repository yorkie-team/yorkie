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
	"reflect"
	"strings"
	gotime "time"
	"unicode"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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

// SetNewObject sets a new Object for the given key.
func (p *Object) SetNewObject(k string, v ...any) *Object {
	value := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		if len(v) == 0 {
			return NewObject(p.context, crdt.NewObject(crdt.NewElementRHT(), ticket))
		}

		if v[0] == nil || isJSONType(v[0]) || !(isStruct(v[0]) || isMapStringInterface(v[0])) {
			panic("unsupported object type")
		}
		return toElement(p.context, buildCRDTElement(p.context, v[0], ticket, newBuildState()))
	})

	return value.(*Object)
}

// SetNewArray sets a new Array for the given key.
func (p *Object) SetNewArray(k string, v ...any) *Array {
	value := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		elements := crdt.NewRGATreeList()
		if len(v) == 0 {
			return NewArray(p.context, crdt.NewArray(elements, ticket))
		}

		if v[0] == nil || !isArrayOrSlice(v[0]) {
			panic("unsupported array type")
		}
		return toElement(p.context, buildCRDTElement(p.context, v[0], ticket, newBuildState()))
	})

	return value.(*Array)
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
		return toElement(p.context, buildCRDTElement(p.context, NewCounter(n, t), ticket, newBuildState()))
	})

	return v.(*Counter)
}

// SetNewTree sets a new Tree for the given key.
func (p *Object) SetNewTree(k string, initialRoot ...*TreeNode) *Tree {
	v := p.setInternal(k, func(ticket *time.Ticket) crdt.Element {
		var root *TreeNode
		if len(initialRoot) > 0 {
			root = initialRoot[0]
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

// buildObjectMembersFromMap constructs an object where all values from the
// user-provided object are transformed into CRDTElements.
// This function takes an object and iterates through its values,
// converting each value into a corresponding CRDTElement.
func buildObjectMembersFromMap(
	context *change.Context,
	json map[string]any,
	stat *buildState,
) map[string]crdt.Element {
	members := make(map[string]crdt.Element)

	for key, value := range json {
		if strings.Contains(key, ".") {
			panic("key must not contain the '.'.")
		}
		ticket := context.IssueTimeTicket()
		members[key] = buildCRDTElement(context, value, ticket, stat)
	}

	return members
}

// buildObjectMembersFromValue converts reflect.Value(struct) to map[string]crdt.Element{}
// except the field that has the tag "yorkie:-" or omitEmpty option and the
// field that is unexported.
// NOTE(highcloud100): This code referred to the "encoding/json" implementation.
func buildObjectMembersFromValue(
	context *change.Context,
	value reflect.Value,
	stat *buildState,
) map[string]crdt.Element {
	members := make(map[string]crdt.Element)
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := value.Type().Field(i)
		tag := fieldType.Tag.Get("yorkie")

		if !field.CanInterface() || tag == "-" {
			continue
		}

		name, options := parseTag(tag)
		if !isValidTag(name) {
			name = ""
		}

		if options.Contains("omitEmpty") && isEmptyValue(field) {
			continue
		}

		if name == "" {
			name = fieldType.Name
		}

		ticket := context.IssueTimeTicket()
		members[name] = buildCRDTElement(context, value.Field(i).Interface(), ticket, stat)
	}
	return members
}

// parseTag parses the given tag to (name, option).
// NOTE(highcloud100): This code referred to the "encoding/json/tags.go" implementation.
func parseTag(tag string) (string, tagOptions) {
	tag, opt, _ := strings.Cut(tag, ",")
	return tag, tagOptions(opt)
}

// isValidTag returns whether the given tag is valid.
// NOTE(highcloud100): This code referred to the "encoding/json" implementation.
func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		// Backslash and quote chars are reserved, but
		// otherwise any punctuation chars are allowed
		// in a tag name.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// Contains reports whether the given option is contained in the tag options.
// Blank spaces in options are ignored by Trim.
// NOTE(highcloud100): This code referred to the "encoding/json/tags.go" implementation.
func (o tagOptions) Contains(optionName string) bool {
	if len(o) == 0 {
		return false
	}
	s := string(o)
	for s != "" {
		var name string
		name, s, _ = strings.Cut(s, ",")
		if strings.Trim(name, " ") == optionName {
			return true
		}
	}
	return false
}

// isEmptyValue reports whether the given value is empty.
// NOTE(highcloud100): This code referred to the "encoding/json/encode.go" implementation.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return v.Bool() == false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !isEmptyValue(v.Field(i)) {
				return false
			}
		}
		return true
	}
	return false
}

// isStruct returns whether the given value is struct or not.
// it also returns true if the given value is a pointer to struct.
func isStruct(v any) bool {
	return (reflect.TypeOf(v).Kind() == reflect.Ptr &&
		reflect.TypeOf(v).Elem().Kind() == reflect.Struct) ||
		reflect.TypeOf(v).Kind() == reflect.Struct
}

// isMapStringInterface returns whether the given value is map[string]any or not.
// it also returns true if the given value is a pointer to map[string]any.
func isMapStringInterface(v any) bool {
	return reflect.TypeOf(v) == reflect.TypeOf(map[string]any{}) ||
		reflect.TypeOf(v) == reflect.TypeOf(&map[string]any{})
}

// isJSONType returns whether the given value is a JSON type or not.
// The json struct types should be treated differently from other structures.
// Because buildCRDTElement processes JSON struct types and other structures separately.
func isJSONType(v any) bool {
	return !(reflect.TypeOf(v) != reflect.TypeOf(Counter{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(&Counter{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(Text{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(&Text{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(Tree{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(&Tree{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(Array{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(&Array{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(Object{}) &&
		reflect.TypeOf(v) != reflect.TypeOf(&Object{}))
}

func isArrayOrSlice(v any) bool {
	return reflect.TypeOf(v).Kind() == reflect.Slice ||
		reflect.TypeOf(v).Kind() == reflect.Array
}
