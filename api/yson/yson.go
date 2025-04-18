/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package yson provides serialization and deserialization of CRDT values.
// It defines the YSON (Yorkie Serialized Object Notation) format which
// preserves type information of CRDT values for accurate reconstruction.
package yson

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

// YSON represents a serializable CRDT value.
// It includes type information along with values, enabling
// reconstruction of Document from serialized data.
type YSON interface {
	isYSON()
	ToTestString() string
}

// Primitive represents a primitive CRDT value.
type Primitive struct {
	ValueType crdt.ValueType
	Value     interface{} // primitive value (nil, bool, int32, int64, float64, string, []byte, time.Time)
}

// Counter represents a counter CRDT value.
type Counter struct {
	ValueType crdt.CounterType
	Value     interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
}

// Array represents an array CRDT value.
type Array struct {
	Value []YSON // array of CRDT elements
}

// Object represents an object CRDT value.
type Object struct {
	Value map[string]YSON // key-value pairs of CRDT elements
}

// Tree represents a tree CRDT value.
type Tree struct {
	Value string // tree marshal string
}

// Text represents a text CRDT value.
type Text struct {
	Value string // text marshal string
}

func (y Primitive) isYSON() {}
func (y Counter) isYSON()   {}
func (y Array) isYSON()     {}
func (y Object) isYSON()    {}
func (y Tree) isYSON()      {}
func (y Text) isYSON()      {}

type YSONType int

const (
	PrimitiveType YSONType = iota
	CounterType
	ArrayType
	ObjectType
	TreeType
	TextType
)

func GetYSONType(y YSON) YSONType {
	switch y.(type) {
	case *Primitive:
		return PrimitiveType
	case *Counter:
		return CounterType
	case *Array:
		return ArrayType
	case *Object:
		return ObjectType
	case *Tree:
		return TreeType
	case *Text:
		return TextType
	default:
		return -1
	}
}

func (y Primitive) ToTestString() string {
	return fmt.Sprintf("{t: %d, vt: %v, v: %v}", GetYSONType(y), y.ValueType, y.Value)
}
func (y Counter) ToTestString() string {
	return fmt.Sprintf("{t: %d, vt: %v, v: %v}", GetYSONType(y), y.ValueType, y.Value)
}
func (y Array) ToTestString() string {
	var elements []string
	for _, elem := range y.Value {
		elements = append(elements, (elem).ToTestString())
	}
	return fmt.Sprintf("{t: %d, v: [%s]}", GetYSONType(y), strings.Join(elements, ", "))
}
func (y Object) ToTestString() string {
	var pairs []string
	// Get sorted keys
	keys := make([]string, 0, len(y.Value))
	for k := range y.Value {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build pairs in sorted order
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s: %s", key, (y.Value[key]).ToTestString()))
	}
	return fmt.Sprintf("{t: %d, v: {%s}}", GetYSONType(y), strings.Join(pairs, ", "))
}
func (y Tree) ToTestString() string {
	return fmt.Sprintf("{t: %d, v: %v}", GetYSONType(y), y.Value)
}
func (y Text) ToTestString() string {
	return fmt.Sprintf("{t: %d, v: %v}", GetYSONType(y), y.Value)
}
