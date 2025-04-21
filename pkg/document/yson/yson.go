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
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

var (
	// ErrUnsupportedElement is returned when the given element is not
	// supported yet.
	ErrUnsupportedElement = errors.New("unsupported element")
)

// Element represents a serializable CRDT value.
// It includes type information along with values, enabling
// reconstruction of Document from serialized data.
type Element interface {
	isElement()

	// Marshal marshals the element into a string representation.
	Marshal() string

	// TODO(hackerwins): Implement Unmarshal method to deserialize the string
	// representation back into the element.
}

// Primitive represents a primitive CRDT value.
// TODO(hackerwins): Consider to use native types instead of bson.Primitive.
type Primitive struct {
	Type  crdt.ValueType
	Value interface{} // primitive value (nil, bool, int32, int64, float64, string, []byte, time.Time)
}

// Counter represents a counter CRDT value.
type Counter struct {
	Type  crdt.CounterType
	Value interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
}

// Array represents an array CRDT value.
type Array []Element

// Object represents an object CRDT value.
type Object map[string]Element

// Tree represents a tree CRDT value.
type Tree struct {
	Root TreeNode
}

// TextNode represents a text node in the tree.
type TextNode struct {
	// Value is the text content of this node.
	Value string

	// Attributes is the attributes of this node.
	Attributes map[string]string
}

// Text represents a text CRDT value.
type Text struct {
	Nodes []TextNode
}

func (y Primitive) isElement() {}
func (y Counter) isElement()   {}
func (y Array) isElement()     {}
func (y Object) isElement()    {}
func (y Tree) isElement()      {}
func (y Text) isElement()      {}

type YSONType int

const (
	PrimitiveType YSONType = iota
	CounterType
	ArrayType
	ObjectType
	TreeType
	TextType
)

func ToType(y Element) YSONType {
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

func (y Primitive) Marshal() string {
	return fmt.Sprintf("{t: %d, vt: %v, v: %v}", ToType(y), y.Type, y.Value)
}

func (y Counter) Marshal() string {
	return fmt.Sprintf("{t: %d, vt: %v, v: %v}", ToType(y), y.Type, y.Value)
}

func (y Array) Marshal() string {
	var elements []string
	for _, elem := range y {
		elements = append(elements, (elem).Marshal())
	}
	return fmt.Sprintf("{t: %d, v: [%s]}", ToType(y), strings.Join(elements, ", "))
}

func (y Object) Marshal() string {
	var pairs []string
	// Get sorted keys
	keys := make([]string, 0, len(y))
	for k := range y {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build pairs in sorted order
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s: %s", key, (y[key]).Marshal()))
	}
	return fmt.Sprintf("{t: %d, v: {%s}}", ToType(y), strings.Join(pairs, ", "))
}

func (y Tree) Marshal() string {
	return fmt.Sprintf("{t: %d, v: %v}", ToType(y), y.Root)
}

func (y Text) Marshal() string {
	var nodes []string
	for _, node := range y.Nodes {
		nodes = append(nodes, fmt.Sprintf("{value: %v, attrs: %v}", node.Value, node.Attributes))
	}
	return fmt.Sprintf("{t: %d, v: [%s]}", ToType(y), strings.Join(nodes, ", "))
}

// TreeNode is a node of Tree.
type TreeNode struct {
	// Type is the type of this node. It is used to distinguish between text
	// nodes and element nodes.
	Type string

	// Children is the children of this node. It is used to represent the
	// descendants of this node. If this node is a text node, it is nil.
	Children []TreeNode

	// Value is the value of text node. If this node is an element node, it is
	// empty string.
	Value string

	// Attributes is the attributes of this node.
	Attributes map[string]string
}
