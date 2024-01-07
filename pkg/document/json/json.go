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
	case string:
		primitive, err := crdt.NewPrimitive(elem, ticket)
		if err != nil {
			panic(err)
		}
		return primitive
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
	default:
		panic("unsupported type")
	}

}

func buildMember(
	context *change.Context,
	json map[string]interface{},
) map[string]crdt.Element {

	members := make(map[string]crdt.Element)

	for key, value := range json {
		ticket := context.IssueTimeTicket()
		elem := buildCRDTElement(context, value, ticket)
		members[key] = elem
	}

	return members
}
