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
		return NewCounter(ctx, elem)
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
	// primitive type
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
		switch elem.valueType {
		case crdt.IntegerCnt:
			counter, err := crdt.NewCounter(crdt.IntegerCnt, elem.value, ticket)
			if err != nil {
				panic(err)
			}
			return counter
		case crdt.LongCnt:
			counter, err := crdt.NewCounter(crdt.LongCnt, elem.value, ticket)
			if err != nil {
				panic(err)
			}
			return counter
		default:
			panic("unsupported counter type ")
		}

	case []interface{}:
		array := crdt.NewArray(crdt.NewRGATreeList(), ticket)
		for _, v := range elem {
			ticket := context.IssueTimeTicket()
			value := buildCRDTElement(context, v, ticket)
			if err := array.InsertAfter(array.LastCreatedAt(), value); err != nil {
				panic(err)
			}
		}
		return array
	case map[string]interface{}:
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket)
		members := buildObjectMember(context, elem)

		for key, value := range members {
			_ = obj.Set(key, value)
		}

		return obj
	case nil:
		// Null
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	default:
		panic("unsupported type")
	}

}
