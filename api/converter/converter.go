/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

// Package converter provides the converter for converting model to
// Protobuf, bytes and vice versa.
package converter

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

var (
	// ErrPackRequired is returned when an empty pack is passed.
	ErrPackRequired = errors.New("pack required")

	// ErrCheckpointRequired is returned when a pack with an empty checkpoint is
	// passed.
	ErrCheckpointRequired = errors.New("checkpoint required")

	// ErrUnsupportedOperation is returned when the given operation is not
	// supported yet.
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrUnsupportedElement is returned when the given element is not
	// supported yet.
	ErrUnsupportedElement = errors.New("unsupported element")

	// ErrUnsupportedEventType is returned when the given event type is not
	// supported yet.
	ErrUnsupportedEventType = errors.New("unsupported event type")

	// ErrUnsupportedValueType is returned when the given value type is not
	// supported yet.
	ErrUnsupportedValueType = errors.New("unsupported value type")

	// ErrUnsupportedCounterType is returned when the given counter type is not
	// supported yet.
	ErrUnsupportedCounterType = errors.New("unsupported counter type")
)

// JSONStruct represents a serializable CRDT value.
// It includes type information along with values, enabling
// reconstruction of Document from serialized data.
type JSONStruct interface {
	isJSONStruct()
	ToTestString() string
}

// JSONPrimitiveStruct represents a primitive CRDT value.
type JSONPrimitiveStruct struct {
	JSONType  api.ValueType
	ValueType crdt.ValueType
	Value     interface{} // primitive value (nil, bool, int32, int64, float64, string, []byte, time.Time)
}

// JSONCounterStruct represents a counter CRDT value.
type JSONCounterStruct struct {
	JSONType  api.ValueType
	ValueType crdt.CounterType
	Value     interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
}

// JSONArrayStruct represents an array CRDT value.
type JSONArrayStruct struct {
	JSONType api.ValueType
	Value    []JSONStruct // array of CRDT elements
}

// JSONObjectStruct represents an object CRDT value.
type JSONObjectStruct struct {
	JSONType api.ValueType
	Value    map[string]JSONStruct // key-value pairs of CRDT elements
}

// JSONTreeStruct represents a tree CRDT value.
type JSONTreeStruct struct {
	JSONType api.ValueType
	Value    string // tree marshal string
}

// JSONTextStruct represents a text CRDT value.
type JSONTextStruct struct {
	JSONType api.ValueType
	Value    string // text marshal string
}

func (j JSONPrimitiveStruct) isJSONStruct() {}
func (j JSONCounterStruct) isJSONStruct()   {}
func (j JSONArrayStruct) isJSONStruct()     {}
func (j JSONObjectStruct) isJSONStruct()    {}
func (j JSONTreeStruct) isJSONStruct()      {}
func (j JSONTextStruct) isJSONStruct()      {}

func (j JSONPrimitiveStruct) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, ValueType: %v, Value: %v}",
		j.JSONType,
		j.ValueType,
		j.Value,
	)
}
func (j JSONCounterStruct) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, ValueType: %v, Value: %v}",
		j.JSONType,
		j.ValueType,
		j.Value,
	)
}
func (j JSONArrayStruct) ToTestString() string {
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
func (j JSONObjectStruct) ToTestString() string {
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
func (j JSONTreeStruct) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, Value: %v}",
		j.JSONType,
		j.Value,
	)
}
func (j JSONTextStruct) ToTestString() string {
	return fmt.Sprintf(
		"{JSONType: %d, Value: %v}",
		j.JSONType,
		j.Value,
	)
}
