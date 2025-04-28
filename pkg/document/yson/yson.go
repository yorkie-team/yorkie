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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"time"

	"encoding/base64"

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

	// Unmarshal unmarshals the string representation back into the element.
	Unmarshal(data string) error
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
	Type string `json:"type,omitempty"`

	// Children is the children of this node. It is used to represent the
	// descendants of this node. If this node is a text node, it is nil.
	Children []TreeNode `json:"children,omitempty"`

	// Value is the value of text node. If this node is an element node, it is
	// empty string.
	Value string `json:"value,omitempty"`

	// Attributes is the attributes of this node.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Tree represents a tree CRDT value.
type Tree struct {
	Root TreeNode
}

// TextNode represents a text node in the tree.
type TextNode struct {
	// Value is the text content of this node.
	Value string `json:"val"`

	// Attributes is the attributes of this node.
	Attributes map[string]string `json:"attrs,omitempty"`
}

// Text represents a text CRDT value.
type Text struct {
	Nodes []TextNode
}

func (y Counter) isElement() {}
func (y Array) isElement()   {}
func (y Object) isElement()  {}
func (y Tree) isElement()    {}
func (y Text) isElement()    {}

type YSONType int

const (
	PrimitiveType YSONType = iota
	CounterType
	ArrayType
	ObjectType
	TreeType
	TextType
)

// marshalElement marshals any element type
func marshalElement(elem interface{}) string {
	switch v := elem.(type) {
	case Counter:
		return v.Marshal()
	case Array:
		return v.Marshal()
	case Object:
		return v.Marshal()
	case Tree:
		return v.Marshal()
	case Text:
		return v.Marshal()
	default:
		return marshalPrimitive(v)
	}
}

func getPrimitiveValueType(v interface{}) crdt.ValueType {
	switch v.(type) {
	case nil:
		return crdt.Null
	case bool:
		return crdt.Boolean
	case int32:
		return crdt.Integer
	case int64:
		return crdt.Long
	case float64:
		return crdt.Double
	case string:
		return crdt.String
	case []byte:
		return crdt.Bytes
	case time.Time:
		return crdt.Date
	default:
		return -1
	}
}

func marshalPrimitive(v interface{}) string {
	var val string
	switch v := v.(type) {
	case nil:
		val = "null"
	case bool, int32, int64, float64:
		val = fmt.Sprintf("%v", v)
	case string:
		val = strconv.Quote(v)
	case []byte:
		val = strconv.Quote(base64.StdEncoding.EncodeToString(v))
	case time.Time:
		val = strconv.Quote(v.Format(time.RFC3339Nano))
	default:
		val = strconv.Quote(fmt.Sprintf("%v", v))
	}
	return fmt.Sprintf(`{"t":%d,"vt":%d,"v":%s}`, PrimitiveType, getPrimitiveValueType(v), val)
}

func (y Counter) Marshal() string {
	return fmt.Sprintf(`{"t":%d,"vt":%v,"v":%v}`, CounterType, y.Type, y.Value)
}

func (y Array) Marshal() string {
	var elements []string
	for _, elem := range y {
		elements = append(elements, marshalElement(elem))
	}
	return fmt.Sprintf(`{"t":%d,"v":[%s]}`, ArrayType, strings.Join(elements, ","))
}

func (y Object) Marshal() string {
	var pairs []string
	keys := make([]string, 0, len(y))
	for k := range y {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf(`"%s":%s`, key, marshalElement(y[key])))
	}
	return fmt.Sprintf(`{"t":%d,"v":{%s}}`, ObjectType, strings.Join(pairs, ","))
}

func (node TreeNode) Marshal() string {
	var parts []string

	if node.Type != "" {
		parts = append(parts, fmt.Sprintf(`"type":"%s"`, node.Type))
	}

	if node.Value != "" {
		parts = append(parts, fmt.Sprintf(`"value":%s`, strconv.Quote(node.Value)))
	}

	if len(node.Children) > 0 {
		var children []string
		for _, child := range node.Children {
			children = append(children, child.Marshal())
		}
		parts = append(parts, fmt.Sprintf(`"children":[%s]`, strings.Join(children, ",")))
	}

	if len(node.Attributes) > 0 {
		attrs := make([]string, 0, len(node.Attributes))
		for k, v := range node.Attributes {
			attrs = append(attrs, fmt.Sprintf(`"%s":%s`, k, strconv.Quote(v)))
		}
		sort.Strings(attrs)
		parts = append(parts, fmt.Sprintf(`"attributes":{%s}`, strings.Join(attrs, ",")))
	}

	return fmt.Sprintf("{%s}", strings.Join(parts, ","))
}

func (y Tree) Marshal() string {
	return fmt.Sprintf(`{"t":%d,"v":%s}`, TreeType, y.Root.Marshal())
}

func (y Text) Marshal() string {
	var nodes []string
	for _, node := range y.Nodes {
		if len(node.Attributes) > 0 {
			attrs := make([]string, 0, len(node.Attributes))
			for k, v := range node.Attributes {
				attrs = append(attrs, fmt.Sprintf(`"%s":%s`, k, strconv.Quote(v)))
			}
			sort.Strings(attrs)
			nodes = append(nodes, fmt.Sprintf(`{"val":%v,"attrs":{%s}}`, strconv.Quote(node.Value), strings.Join(attrs, ",")))
		} else {
			nodes = append(nodes, fmt.Sprintf(`{"val":%v}`, strconv.Quote(node.Value)))
		}
	}
	return fmt.Sprintf(`{"t":%d,"v":[%s]}`, TextType, strings.Join(nodes, ","))
}

type jsonElement struct {
	Type      int         `json:"t"`
	Value     interface{} `json:"v"`
	ValueType int         `json:"vt,omitempty"`
}

// parseJSONElement parses a map[string]interface{} into a jsonElement
func parseJSONElement(elem map[string]interface{}) (jsonElement, error) {
	typeVal, ok := elem["t"].(float64)
	if !ok {
		return jsonElement{}, fmt.Errorf("invalid Type: %v", elem["t"])
	}
	result := jsonElement{
		Type:  int(typeVal),
		Value: elem["v"],
	}
	if valueTypeVal, ok := elem["vt"]; ok {
		valueTypeFloat, ok := valueTypeVal.(float64)
		if !ok {
			return jsonElement{}, fmt.Errorf("invalid ValueType: %v", valueTypeVal)
		}
		result.ValueType = int(valueTypeFloat)
	}
	return result, nil
}

// unmarshalYSON is a generic function that handles common YSON unmarshal logic
func unmarshalYSON[T any](data string, expectedType YSONType) (T, error) {
	var elem jsonElement
	if err := json.Unmarshal([]byte(data), &elem); err != nil {
		return *new(T), fmt.Errorf("invalid format: %w", err)
	}
	if YSONType(elem.Type) != expectedType {
		return *new(T), fmt.Errorf("invalid type: expected %v, got %v", expectedType, elem.Type)
	}

	result, err := unmarshalYSONElement(elem)
	if err != nil {
		return *new(T), fmt.Errorf("failed to unmarshal: %w", err)
	}

	typedResult, ok := result.(T)
	if !ok {
		return *new(T), fmt.Errorf("type assertion failed: expected %T, got %T", *new(T), result)
	}
	return typedResult, nil
}

func (y *Counter) Unmarshal(data string) error {
	counter, err := unmarshalYSON[Counter](data, CounterType)
	if err != nil {
		return err
	}
	*y = counter
	return nil
}

func (y *Array) Unmarshal(data string) error {
	arr, err := unmarshalYSON[Array](data, ArrayType)
	if err != nil {
		return err
	}
	*y = arr
	return nil
}

func (y *Object) Unmarshal(data string) error {
	obj, err := unmarshalYSON[Object](data, ObjectType)
	if err != nil {
		return err
	}
	*y = obj
	return nil
}

func (y *Tree) Unmarshal(data string) error {
	tree, err := unmarshalYSON[Tree](data, TreeType)
	if err != nil {
		return err
	}
	*y = tree
	return nil
}

func (y *Text) Unmarshal(data string) error {
	text, err := unmarshalYSON[Text](data, TextType)
	if err != nil {
		return err
	}
	*y = text
	return nil
}

func unmarshalYSONElement(j jsonElement) (interface{}, error) {
	switch YSONType(j.Type) {
	case CounterType:
		return unmarshalCounter(j)
	case ArrayType:
		return unmarshalArray(j)
	case ObjectType:
		return unmarshalObject(j)
	case TreeType:
		return unmarshalTree(j)
	case TextType:
		return unmarshalText(j)
	case PrimitiveType:
		return unmarshalPrimitive(j)
	default:
		return nil, fmt.Errorf("unsupported type: %d", j.Type)
	}
}

func unmarshalPrimitive(j jsonElement) (interface{}, error) {
	switch crdt.ValueType(j.ValueType) {
	case crdt.Null:
		if j.Value != nil {
			return nil, fmt.Errorf("invalid null value: %T", j.Value)
		}
		return nil, nil
	case crdt.Boolean:
		val, ok := j.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid boolean value: %T", j.Value)
		}
		return val, nil
	case crdt.Integer:
		val, ok := j.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid integer value: %T", j.Value)
		}
		return int32(val), nil
	case crdt.Long:
		val, ok := j.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid long value: %T", j.Value)
		}
		return int64(val), nil
	case crdt.Double:
		val, ok := j.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid double value: %T", j.Value)
		}
		return val, nil
	case crdt.String:
		val, ok := j.Value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid string value: %T", j.Value)
		}
		return val, nil
	case crdt.Bytes:
		val, ok := j.Value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid bytes value: %T", j.Value)
		}
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err != nil {
			return nil, fmt.Errorf("invalid bytes value: %w", err)
		}
		return decoded, nil
	case crdt.Date:
		val, ok := j.Value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid date value: %T", j.Value)
		}
		t, err := time.Parse(time.RFC3339Nano, val)
		if err != nil {
			return nil, fmt.Errorf("invalid date value: %w", err)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported primitive type: %v", j.ValueType)
	}
}

func unmarshalCounter(j jsonElement) (Counter, error) {
	counterType := crdt.CounterType(j.ValueType)
	counterValue, ok := j.Value.(float64)
	if !ok {
		return Counter{}, fmt.Errorf("invalid counter value: %T", j.Value)
	}
	switch counterType {
	case crdt.IntegerCnt:
		return Counter{
			Type:  counterType,
			Value: int32(counterValue),
		}, nil
	case crdt.LongCnt:
		return Counter{
			Type:  counterType,
			Value: int64(counterValue),
		}, nil
	default:
		return Counter{}, fmt.Errorf("unsupported counter type: %v", j.ValueType)
	}
}

func unmarshalArray(j jsonElement) (Array, error) {
	arrElem, ok := j.Value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid array value: %T", j.Value)
	}
	arr := make(Array, len(arrElem))
	for i, elem := range arrElem {
		elemMap, ok := elem.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid array element: %T", elem)
		}
		jsonElem, err := parseJSONElement(elemMap)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal array: %w", err)
		}
		subVal, err := unmarshalYSONElement(jsonElem)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal array: %w", err)
		}
		arr[i] = subVal
	}
	return arr, nil
}

func unmarshalObject(j jsonElement) (Object, error) {
	mapVal, ok := j.Value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid object value: %T", j.Value)
	}
	obj := make(Object)
	for k, v := range mapVal {
		subMap, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid object value for key %s: %T", k, v)
		}
		jsonElem, err := parseJSONElement(subMap)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal object for key %s: %w", k, err)
		}
		subVal, err := unmarshalYSONElement(jsonElem)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal object for key %s: %w", k, err)
		}
		obj[k] = subVal
	}
	return obj, nil
}

func unmarshalTree(j jsonElement) (Tree, error) {
	val, ok := j.Value.(map[string]interface{})
	if !ok {
		return Tree{}, fmt.Errorf("invalid tree value: %T", j.Value)
	}
	node, err := unmarshalTreeNode(val)
	if err != nil {
		return Tree{}, fmt.Errorf("failed to unmarshal tree node: %w", err)
	}
	return Tree{Root: node}, nil
}

func unmarshalText(j jsonElement) (Text, error) {
	val, ok := j.Value.([]interface{})
	if !ok {
		return Text{}, fmt.Errorf("invalid text value: %T", j.Value)
	}
	if len(val) == 0 {
		return Text{}, nil
	}
	text := Text{
		Nodes: make([]TextNode, len(val)),
	}
	for i, node := range val {
		textNode, err := unmarshalTextNode(node)
		if err != nil {
			return Text{}, fmt.Errorf("failed to unmarshal text node: %w", err)
		}
		text.Nodes[i] = textNode
	}
	return text, nil
}

func unmarshalTreeNode(val map[string]interface{}) (TreeNode, error) {
	var node TreeNode

	if typeStr, ok := val["type"].(string); ok {
		node.Type = typeStr
	}
	if value, ok := val["value"].(string); ok {
		node.Value = value
	}
	if childrenRaw, ok := val["children"]; ok {
		children, ok := childrenRaw.([]interface{})
		if !ok {
			return TreeNode{}, fmt.Errorf("invalid tree node format")
		}
		node.Children = make([]TreeNode, len(children))
		for i, child := range children {
			childMap, ok := child.(map[string]interface{})
			if !ok {
				return TreeNode{}, fmt.Errorf("invalid tree node format")
			}
			childNode, err := unmarshalTreeNode(childMap)
			if err != nil {
				return TreeNode{}, fmt.Errorf("failed to unmarshal child node: %w", err)
			}
			node.Children[i] = childNode
		}
	}
	if attrsRaw, ok := val["attributes"]; ok {
		attrs, ok := attrsRaw.(map[string]interface{})
		if !ok {
			return TreeNode{}, fmt.Errorf("invalid tree node format")
		}
		node.Attributes = make(map[string]string)
		for k, v := range attrs {
			str, ok := v.(string)
			if !ok {
				return TreeNode{}, fmt.Errorf("invalid tree node format")
			}
			node.Attributes[k] = str
		}
	}
	return node, nil
}

func unmarshalTextNode(val interface{}) (TextNode, error) {
	nodeMap, ok := val.(map[string]interface{})
	if !ok {
		return TextNode{}, fmt.Errorf("invalid text node format")
	}

	value, ok := nodeMap["val"].(string)
	if !ok {
		return TextNode{}, fmt.Errorf("text node requires 'val' field of type string")
	}

	node := TextNode{Value: value}

	if attrsRaw, ok := nodeMap["attrs"]; ok {
		attrs, ok := attrsRaw.(map[string]interface{})
		if !ok {
			return TextNode{}, fmt.Errorf("invalid text node format")
		}
		node.Attributes = make(map[string]string)
		for k, v := range attrs {
			str, ok := v.(string)
			if !ok {
				return TextNode{}, fmt.Errorf("invalid text node format")
			}
			node.Attributes[k] = str
		}
	}

	return node, nil
}
