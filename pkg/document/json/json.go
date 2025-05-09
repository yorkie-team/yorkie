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
