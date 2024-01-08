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
	"strings"
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

	//case crdt.CounterType
	//case Tree
	//case ...*TreeNode:
	//case Text ->

	case map[string]interface{}:
		obj := NewObject(context, crdt.NewObject(crdt.NewElementRHT(), ticket))
		members := buildMember(context, elem)

		for key, value := range members {
			value = toOriginal(value)
			removed := obj.Set(key, value)
			obj.context.RegisterElement(value)
			if removed != nil {
				obj.context.RegisterRemovedElementPair(obj, removed)
			}
		}

		return obj

	case []interface{}: //array
		array := NewArray(context, crdt.NewArray(crdt.NewRGATreeList(), ticket))
		for _, v := range elem {
			ticket := context.IssueTimeTicket()
			value := buildCRDTElement(context, v, ticket)
			value = toOriginal(value)
			if err := array.InsertAfter(array.LastCreatedAt(), value); err != nil {
				panic(err)
			}
			array.context.RegisterElement(value)
		}
		return array
	default:
		// Null
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
	}

}

func buildMember(
	context *change.Context,
	json map[string]interface{},
) map[string]crdt.Element {

	members := make(map[string]crdt.Element)

	for key, value := range json {
		if strings.Contains(key, ".") {
			panic("key must not contain the '.'.") // error 처리 확인해야함
		}

		ticket := context.IssueTimeTicket()
		elem := buildCRDTElement(context, value, ticket)
		members[key] = elem
	}

	return members
}
