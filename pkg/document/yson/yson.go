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
	"encoding/base64"
	gojson "encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

var (
	// ErrUnsupportedElement is returned when the given element is not
	// supported yet.
	ErrUnsupportedElement = errors.New("unsupported element")

	// ErrInvalidTextNode is returned when the given text node is not
	// valid.
	ErrInvalidTextNode = errors.New("invalid text node")

	// ErrInvalidTreeNode is returned when the given tree node is not
	// valid.
	ErrInvalidTreeNode = errors.New("invalid tree node")
)

const (
	// DefaultRootNodeType is the default type of root node.
	DefaultRootNodeType = "root"
)

// Element represents a serializable CRDT value.
// It includes type information along with values, enabling
// reconstruction of Document from serialized data.
type Element interface {
	isElement()

	// Marshal marshals the element into a string representation.
	Marshal() (string, error)
}

// Counter represents a counter CRDT value.
type Counter struct {
	Type  crdt.CounterType
	Value interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
}

// Array represents an array CRDT value.
type Array []interface{}

// Object represents an object CRDT value.
type Object map[string]interface{}

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

func (y Object) isElement()  {}
func (y Array) isElement()   {}
func (y Counter) isElement() {}
func (y Text) isElement()    {}
func (y Tree) isElement()    {}

// marshalElement marshals any element type
func marshalElement(elem interface{}) (string, error) {
	switch v := elem.(type) {
	case Element:
		return v.Marshal()
	default:
		return marshalPrimitive(v)
	}
}

func marshalPrimitive(v interface{}) (string, error) {
	switch v := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return fmt.Sprintf("%v", v), nil
	case float64:
		return fmt.Sprintf("%v", v), nil
	case string:
		return strconv.Quote(v), nil
	case int32:
		return fmt.Sprintf("Int(%d)", v), nil
	case int64:
		return fmt.Sprintf("Long(%d)", v), nil
	case []byte:
		encoded := base64.StdEncoding.EncodeToString(v)
		return fmt.Sprintf(`BinData("%s")`, encoded), nil
	case time.Time:
		return fmt.Sprintf(`Date("%s")`, v.Format(time.RFC3339Nano)), nil
	default:
		return "", fmt.Errorf("unsupported type: %T", v)
	}
}

func (y Object) Marshal() (string, error) {
	var pairs []string
	keys := make([]string, 0, len(y))
	for k := range y {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		marshalled, err := marshalElement(y[key])
		if err != nil {
			return "", err
		}

		pairs = append(pairs, fmt.Sprintf(`"%s":%s`, key, marshalled))
	}
	return fmt.Sprintf("{%s}", strings.Join(pairs, ",")), nil
}

func (y Array) Marshal() (string, error) {
	var elements []string
	for _, elem := range y {
		marshalled, err := marshalElement(elem)
		if err != nil {
			return "", err
		}

		elements = append(elements, marshalled)
	}
	return fmt.Sprintf("[%s]", strings.Join(elements, ",")), nil
}

func (y Counter) Marshal() (string, error) {
	switch y.Type {
	case crdt.IntegerCnt:
		return fmt.Sprintf("Counter(Int(%v))", y.Value), nil
	case crdt.LongCnt:
		return fmt.Sprintf("Counter(Long(%v))", y.Value), nil
	default:
		return "", fmt.Errorf("unsupported counter type: %v", y.Type)
	}
}

func (y Text) Marshal() (string, error) {
	var nodes []string
	for _, node := range y.Nodes {
		if len(node.Attributes) == 0 {
			nodes = append(nodes, fmt.Sprintf(`{"val":%s}`, strconv.Quote(node.Value)))
			continue
		}

		attrs := make([]string, 0, len(node.Attributes))
		for k, v := range node.Attributes {
			attrs = append(attrs, fmt.Sprintf(`%s:%s`, strconv.Quote(k), strconv.Quote(v)))
		}
		sort.Strings(attrs)
		nodes = append(nodes, fmt.Sprintf(`{"val":%s,"attrs":{%s}}`, strconv.Quote(node.Value), strings.Join(attrs, ",")))
	}
	return fmt.Sprintf("Text([%s])", strings.Join(nodes, ",")), nil
}

func (y Tree) Marshal() (string, error) {
	return fmt.Sprintf("Tree(%s)", y.Root.Marshal()), nil
}

func (n *TreeNode) Marshal() string {
	if n.Type == "text" {
		return fmt.Sprintf(`{"type":%s,"value":%s}`, strconv.Quote(n.Type), strconv.Quote(n.Value))
	}

	var children []string
	for _, child := range n.Children {
		children = append(children, child.Marshal())
	}

	if len(n.Attributes) == 0 {
		return fmt.Sprintf(`{"type":%s,"children":[%s]}`, strconv.Quote(n.Type), strings.Join(children, ","))
	}

	var attrs []string
	for k, v := range n.Attributes {
		attrs = append(attrs, fmt.Sprintf(`%s:%s`, strconv.Quote(k), strconv.Quote(v)))
	}
	sort.Strings(attrs)
	return fmt.Sprintf(`{"type":%s,"attrs":{%s},"children":[%s]}`,
		strconv.Quote(n.Type), strings.Join(attrs, ","), strings.Join(children, ","))
}

// Unmarshal parses a string representation of a YSON element into the
// corresponding Element type.
func Unmarshal(data string, elem Element) error {
	processedData, err := preprocessTypeValues(data)
	if err != nil {
		return err
	}

	// Parse the processed JSON data
	var raw interface{}
	if err := gojson.Unmarshal([]byte(processedData), &raw); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}

	// Convert the raw data into the appropriate Element type
	switch e := elem.(type) {
	case *Array:
		arr, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("expected array, got %T", raw)
		}
		parsed, err := parseArray(arr)
		if err != nil {
			return err
		}
		*e = parsed
	case *Object:
		obj, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected object, got %T", raw)
		}
		parsed, err := parseObject(obj)
		if err != nil {
			return err
		}
		*e = parsed
	case *Tree:
		tree, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected tree, got %T", raw)
		}

		if v, ok := tree["value"].(map[string]interface{}); ok {
			parsed, err := parseTree(v)
			if err != nil {
				return err
			}
			*e = parsed
		} else {
			return fmt.Errorf("expected tree, got %T", raw)
		}
	case *Text:
		text, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected text, got %T", raw)
		}

		if v, ok := text["value"].([]interface{}); ok {
			parsed, err := parseText(v)
			if err != nil {
				return err
			}
			*e = parsed
		} else {
			return fmt.Errorf("expected text, got %T", raw)
		}

	case *Counter:
		counter, err := parseCounter(raw.(map[string]interface{}))
		if err != nil {
			return err
		}
		*e = counter
	default:
		return ErrUnsupportedElement
	}

	return nil
}

func parseTypedValue(raw map[string]interface{}) (interface{}, error) {
	t, ok := raw["type"].(string)
	if !ok {
		return nil, ErrUnsupportedElement
	}

	switch t {
	case "Int":
		return int32(raw["value"].(float64)), nil
	case "Long":
		return int64(raw["value"].(float64)), nil
	case "BinData":
		val, err := base64.StdEncoding.DecodeString(raw["value"].(string))
		if err != nil {
			return nil, fmt.Errorf("decode base64: %w", err)
		}

		return val, nil
	case "Date":
		val, err := time.Parse(time.RFC3339Nano, raw["value"].(string))
		if err != nil {
			return nil, fmt.Errorf("parse date: %w", err)
		}

		return val, nil
	case "Counter":
		return parseCounter(raw)
	case "Tree":
		if value, ok := raw["value"].(map[string]interface{}); ok {
			return parseTree(value)
		}

		return nil, ErrInvalidTreeNode
	case "Text":
		if value, ok := raw["value"].([]interface{}); ok {
			return parseText(value)
		}
		return nil, ErrInvalidTextNode
	}

	return nil, ErrUnsupportedElement
}

func parseObject(raw map[string]interface{}) (Object, error) {
	obj := Object{}
	for k, v := range raw {
		switch v := v.(type) {
		case map[string]interface{}:
			if _, ok := v["type"].(string); ok {
				val, err := parseTypedValue(v)
				if err != nil {
					return nil, err
				}

				obj[k] = val
			} else {
				val, err := parseObject(v)
				if err != nil {
					return nil, err
				}

				obj[k] = val
			}
		case []interface{}:
			val, err := parseArray(v)
			if err != nil {
				return nil, err
			}

			obj[k] = val
		default:
			obj[k] = v
		}
	}
	return obj, nil
}

// Helper functions to parse specific types
func parseArray(raw []interface{}) (Array, error) {
	var arr Array
	for _, item := range raw {
		switch v := item.(type) {
		case map[string]interface{}:
			if _, ok := v["type"].(string); ok {
				val, err := parseTypedValue(v)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			} else {
				val, err := parseObject(v)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
		case []interface{}:
			val, err := parseArray(v)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		default:
			arr = append(arr, v)
		}
	}
	return arr, nil
}

func parseCounter(raw map[string]interface{}) (Counter, error) {
	counter := Counter{}
	if value, ok := raw["value"].(map[string]interface{}); ok {
		if t, ok := value["type"].(string); ok {
			switch t {
			case "Int":
				counter.Type = crdt.IntegerCnt
				counter.Value = int32(value["value"].(float64))
			case "Long":
				counter.Type = crdt.LongCnt
				counter.Value = int64(value["value"].(float64))
			default:
				return Counter{}, fmt.Errorf("unsupported counter type: %s", t)
			}
		} else {
			return Counter{}, fmt.Errorf("missing counter type")
		}
	} else {
		return Counter{}, fmt.Errorf("missing counter value")
	}
	return counter, nil
}

func parseText(raw []interface{}) (Text, error) {
	var text Text

	for _, node := range raw {
		n := node.(map[string]interface{})
		textNode := TextNode{}

		if _, ok := n["val"].(string); !ok {
			return text, errors.New("missing val field")
		}

		if attrs, ok := n["attrs"].(map[string]interface{}); ok {
			textNode.Attributes = make(map[string]string)
			for k, v := range attrs {
				textNode.Attributes[k] = v.(string)
			}
		}

		text.Nodes = append(text.Nodes, textNode)
	}
	return text, nil
}

func parseTree(raw map[string]interface{}) (Tree, error) {
	root, err := parseTreeNode(raw)
	if err != nil {
		return Tree{}, err
	}

	return Tree{Root: root}, nil
}

func parseTreeNode(raw map[string]interface{}) (TreeNode, error) {
	node := TreeNode{}
	if value, ok := raw["type"].(string); ok {
		node.Type = value
	} else {
		node.Type = DefaultRootNodeType
	}

	if value, ok := raw["value"].(string); ok {
		node.Value = value
	}

	if attrs, ok := raw["attrs"].(map[string]interface{}); ok {
		node.Attributes = make(map[string]string)
		for k, v := range attrs {
			node.Attributes[k] = v.(string)
		}
	}

	if children, ok := raw["children"].([]interface{}); ok {
		for _, child := range children {
			childNode, err := parseTreeNode(child.(map[string]interface{}))
			if err != nil {
				return TreeNode{}, err
			}

			node.Children = append(node.Children, childNode)
		}
	}

	return node, nil
}

// preprocessTypeValues replaces custom types in the YSON string with
// JSON-compatible formats.
func preprocessTypeValues(data string) (string, error) {
	type replacement struct {
		oldStr string
		newStr string
	}

	// Replace custom types with JSON-compatible formats in a specific order
	replacements := []replacement{
		// Handle empty constructors
		{`Text()`, `{"type":"Text","value":[]}`},
		{`Tree()`, `{"type":"Tree","value":{}}`},

		// Process nested types from outer to inner
		{`Counter(`, `{"type":"Counter","value":`},
		{`Text(`, `{"type":"Text","value":`},
		{`Tree(`, `{"type":"Tree","value":`},

		// Then handle inner types
		{`Int(`, `{"type":"Int","value":`},
		{`Long(`, `{"type":"Long","value":`},
		{`BinData("`, `{"type":"BinData","value":"`},
		{`Date("`, `{"type":"Date","value":"`},

		// Finally, handle closing parentheses
		{`)`, `}`},
	}

	// Replace custom types with JSON-compatible formats
	for _, r := range replacements {
		data = strings.ReplaceAll(data, r.oldStr, r.newStr)
	}

	return data, nil
}

// ParseArray parses a string representation of a YSON array into the
// corresponding Array type.
func ParseArray(data string) Array {
	var arr Array
	if err := Unmarshal(data, &arr); err != nil {
		panic("parse array" + err.Error())
	}
	return arr
}
