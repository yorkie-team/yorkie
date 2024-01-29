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

// Package json provides the JSON document implementation.
package json

import (
	"reflect"
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// startDetectingCyclesAfter is the staring depth of nested structs after which
// we start checking for cycles. This is a performance optimization to avoid
// the overhead of checking for cycles.
const startDetectingCyclesAfter = 5000

// buildState is used to track the depth of nested structs during encoding.
type buildState struct {
	visited map[any]bool
	depth   int
}

// newBuildState creates a new instance of buildState.
func newBuildState() *buildState {
	return &buildState{
		visited: make(map[any]bool),
		depth:   0,
	}
}

// tagOptions is the string following a comma in a struct field's "yorkie"
// tag, or the empty string.
type tagOptions string

func toOriginal(elem crdt.Element) crdt.Element {
	switch elem := elem.(type) {
	case *Object:
		return elem.Object
	case *Array:
		return elem.Array
	case *Text:
		return elem.Text
	case *Counter:
		return elem.Counter
	case *Tree:
		return elem.Tree
	case *crdt.Primitive:
		return elem
	}
	panic("unsupported type")
}

// toElement converts crdt.Element to the corresponding json.Element.
func toElement(ctx *change.Context, elem crdt.Element) crdt.Element {
	switch elem := elem.(type) {
	case *crdt.Object:
		return NewObject(ctx, elem)
	case *crdt.Array:
		return NewArray(ctx, elem)
	case *crdt.Text:
		text := NewText()
		return text.Initialize(ctx, elem)
	case *crdt.Counter:
		counter := NewCounter(elem.Value(), elem.ValueType())
		return counter.Initialize(ctx, elem)
	case *crdt.Tree:
		tree := NewTree()
		return tree.Initialize(ctx, elem)
	case *crdt.Primitive:
		return elem
	}
	panic("unsupported type")
}

// buildCRDTElement builds crdt.Element from the given value.
func buildCRDTElement(
	context *change.Context,
	value any,
	ticket *time.Ticket,
	stat *buildState,
) crdt.Element {
	// 01. Check the stat for detecting cycles.
	if stat.depth++; stat.depth > startDetectingCyclesAfter {
		// If the depth exceeds the `startDetectingCyclesAfter`, start checking cycles.
		// It memorizes the visited pointer of reflect.Ptr, reflect.Map, reflect.Slice.
		switch reflect.ValueOf(value).Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			ptr := reflect.ValueOf(value).Pointer()
			if stat.visited[ptr] {
				panic("cycle detected")
			}
			stat.visited[ptr] = true
			defer func() {
				delete(stat.visited, ptr)
				stat.depth--
			}()
		}
	}

	// 02. The type of the given value is one of the basic types.
	switch elem := value.(type) {
	case nil, string, int, int32, int64, float32, float64, []byte, bool, gotime.Time:
		primitive, err := crdt.NewPrimitive(elem, ticket)
		if err != nil {
			panic(err)
		}

		return primitive
	case Tree:
		return crdt.NewTree(buildRoot(context, elem.initialRoot, ticket), ticket)
	case Text:
		return crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket)
	case Counter:
		if elem.value == nil {
			elem.value = 0
		}
		counter, err := crdt.NewCounter(elem.valueType, elem.value, ticket)
		if err != nil {
			panic(err)
		}

		return counter
	case Object:
		// NOTE(highcloud100): This case only occurs when the given struct's element is json.Object.
		// For example, the given struct is `type Foo struct { Bar json.Object }`.
		// For now, we don't support this case. We need to support this case later.
		// Check the "object.set with JSON.Object" test case in `object_test.go`.
		return crdt.NewObject(crdt.NewElementRHT(), ticket, nil)
	case map[string]any:
		return crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMembersFromMap(context, elem, stat))
	case reflect.Value:
		// NOTE(highcloud100): This case only occurs when reflect.Value of struct is given.
		// BuildArrayElements only can throw the arbitrary struct as reflect.Value type to this function.
		if elem.Type().Kind() != reflect.Struct {
			break
		}
		return crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMembersFromValue(context, elem, stat))
	}

	// 03. The type of the given value is user defined struct or array.
	switch reflect.ValueOf(value).Kind() {
	case reflect.Slice:
		return crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayElements(context, value, stat))
	case reflect.Array:
		// TODO(highcloud100): For now, buildArrayElements only accepts slice type.
		// We need to support array type later to avoid copying the slice.
		length := reflect.ValueOf(value).Len()
		slice := reflect.MakeSlice(reflect.SliceOf(reflect.ValueOf(value).Type().Elem()), length, length)
		reflect.Copy(slice, reflect.ValueOf(value))
		return crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayElements(context, slice.Interface(), stat))
	case reflect.Pointer:
		val := reflect.ValueOf(value)
		if val.IsNil() || !val.Elem().CanInterface() {
			return buildCRDTElement(context, nil, ticket, stat)
		}
		return buildCRDTElement(context, val.Elem().Interface(), ticket, stat)
	case reflect.Struct:
		return buildCRDTElement(context, reflect.ValueOf(value), ticket, stat)
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64, reflect.String, reflect.Bool:
		return buildCRDTElement(context, namedTypeToPrimitive(value), ticket, stat)
	default:
		panic("unsupported type")
	}
}

// namedTypeToPrimitive converts the given named type to primitive type.
func namedTypeToPrimitive(value any) any {
	val := reflect.ValueOf(value)
	switch val.Kind() {
	case reflect.Int:
		return val.Int()
	case reflect.Int32:
		return int32(val.Int())
	case reflect.Int64:
		return val.Int()
	case reflect.Float32:
		return float32(val.Float())
	case reflect.Float64:
		return val.Float()
	case reflect.String:
		return val.String()
	case reflect.Bool:
		return val.Bool()
	default:
		panic("unsupported type")
	}
}
