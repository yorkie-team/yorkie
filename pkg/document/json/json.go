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

func buildCRDTElement(
	context *change.Context,
	value interface{},
	ticket *time.Ticket,
) crdt.Element {
	switch elem := value.(type) {
	case nil, string, int, int32, int64, float32, float64, []byte, bool, gotime.Time:
		primitive, err := crdt.NewPrimitive(elem, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	case Tree:
		crdtTree := crdt.NewTree(buildRoot(context, elem.initialRoot, ticket), ticket)
		return crdtTree
	case Text:
		return crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket)
	case Counter:
		counter, err := crdt.NewCounter(elem.valueType, elem.value, ticket)
		if err != nil {
			panic(err)
		}
		return counter
	case map[string]interface{}:
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMembers(context, elem))
		return obj
	default:
		// TODO: this is a temporary solution.
		// We can deal the array type like primitive type. ex) []int, []string, []map[string]interface{}...
		// However, we need to check the type of buildArrayElements as well.
		// So this code check only if it's an array or slice.
		// The type of specific array is handled by buildArrayElements.
		if reflect.ValueOf(elem).Kind() == reflect.Slice || reflect.ValueOf(elem).Kind() == reflect.Array {
			array := crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayElements(context, elem))
			return array
		}
		panic("unsupported type")
	}

}
