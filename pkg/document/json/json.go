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
		return NewText(ctx, elem)
	case *crdt.Counter:
		counter := NewCounter(elem.Value(), elem.ValueType())
		counter.Initialize(ctx, elem)
		return counter
	case *crdt.Tree:
		return NewTree(ctx, elem)
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
	case string, int, int32, int64, float32, float64, []byte, bool, gotime.Time:
		primitive, err := crdt.NewPrimitive(elem, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	case *TreeNode:
		return crdt.NewTree(buildRoot(context, elem, ticket), ticket)
	case *Text:
		return crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ticket)
	case *Counter:
		counter, err := crdt.NewCounter(elem.valueType, elem.value, ticket)
		if err != nil {
			panic(err)
		}
		return counter
	case []interface{}:
		array := crdt.NewArray(crdt.NewRGATreeList(), ticket, buildArrayMember(context, elem))
		return array
	case map[string]interface{}:
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket, buildObjectMember(context, elem))
		return obj
	case nil:
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	default:
		panic("unsupported type")
	}

}
