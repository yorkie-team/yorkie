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

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
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
	JSONType  api.ValueType
	ValueType crdt.ValueType
	Value     interface{} // primitive value (nil, bool, int32, int64, float64, string, []byte, time.Time)
}

// Counter represents a counter CRDT value.
type Counter struct {
	JSONType  api.ValueType
	ValueType crdt.CounterType
	Value     interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
}

// Array represents an array CRDT value.
type Array struct {
	JSONType api.ValueType
	Value    []YSON // array of CRDT elements
}

// Object represents an object CRDT value.
type Object struct {
	JSONType api.ValueType
	Value    map[string]YSON // key-value pairs of CRDT elements
}

// Tree represents a tree CRDT value.
type Tree struct {
	JSONType api.ValueType
	Value    string // tree marshal string
}

// Text represents a text CRDT value.
type Text struct {
	JSONType api.ValueType
	Value    string // text marshal string
}

func (j Primitive) isYSON() {}
func (j Counter) isYSON()   {}
func (j Array) isYSON()     {}
func (j Object) isYSON()    {}
func (j Tree) isYSON()      {}
func (j Text) isYSON()      {}

func (j Primitive) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, ValueType: %v, Value: %v}",
		j.JSONType,
		j.ValueType,
		j.Value,
	)
}
func (j Counter) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, ValueType: %v, Value: %v}",
		j.JSONType,
		j.ValueType,
		j.Value,
	)
}
func (j Array) ToTestString() string {
	var elements []string
	for _, elem := range j.Value {
		elements = append(elements, (elem).ToTestString())
	}
	return fmt.Sprintf(
		"{JSONType: %d, Value: [%s]}",
		j.JSONType,
		strings.Join(elements, ", "),
	)
}
func (j Object) ToTestString() string {
	var pairs []string
	// Get sorted keys
	keys := make([]string, 0, len(j.Value))
	for k := range j.Value {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build pairs in sorted order
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s: %s", key, (j.Value[key]).ToTestString()))
	}
	return fmt.Sprintf(
		"{JSONType: %d, Value: {%s}}",
		j.JSONType,
		strings.Join(pairs, ", "),
	)
}
func (j Tree) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, Value: %v}",
		j.JSONType,
		j.Value,
	)
}
func (j Text) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, Value: %v}",
		j.JSONType,
		j.Value,
	)
}
