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
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/index"
)

var (
	// ErrUnsupported is returned when the given request is not
	// supported.
	ErrUnsupported = errors.InvalidArgument("unsupported element")

	// ErrInvalidYSON is returned when the given YSON is not
	// valid.
	ErrInvalidYSON = errors.InvalidArgument("invalid YSON")

	// dedupCounterRe matches DedupCounter(Int(N),"base64") in YSON text.
	// Only Int is supported. If Long is added, extend both this regex
	// and parseDedupCounter.
	dedupCounterRe = regexp.MustCompile(`DedupCounter\(Int\((-?\d+)\),"([^"]+)"\)`)
)

const (
	// DefaultRootNodeType is the default type of root node.
	DefaultRootNodeType = "root"

	// counterTypeInt is the YSON token for 32-bit integer counters.
	counterTypeInt = "Int"

	// counterTypeLong is the YSON token for 64-bit integer counters.
	counterTypeLong = "Long"
)

var (
	// TextNodeType is the type of text node.
	TextNodeType = index.TextNodeType
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
	Type      crdt.CounterType
	Value     interface{} // counter value (int32 for IntegerCnt, int64 for LongCnt)
	Registers []byte      // HLL registers (dedup only; nil for normal counters)
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
		return "", fmt.Errorf("marshal primitive: %w", ErrUnsupported)
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
	case crdt.IntegerDedupCnt:
		encoded := base64.StdEncoding.EncodeToString(y.Registers)
		return fmt.Sprintf(`DedupCounter(Int(%v),"%s")`, y.Value, encoded), nil
	default:
		return "", fmt.Errorf("marshal counter: %w", ErrUnsupported)
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
	processedData := preprocessTypeValues(data)

	// Parse the processed JSON data. UseNumber keeps numeric lexemes as
	// json.Number so that integer typed values can be validated exactly
	// instead of being coerced through float64 (which truncates fractional
	// values, wraps out-of-range values, and loses precision beyond 2^53).
	var raw interface{}
	dec := gojson.NewDecoder(strings.NewReader(processedData))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", ErrInvalidYSON)
	}
	// Reject any trailing data after the top-level value. json.Decoder
	// tolerates trailing bytes whereas json.Unmarshal did not, and dec.More()
	// alone misses stray closing tokens such as "]" or "}". Require the stream
	// to be at EOF after the single top-level value.
	if err := dec.Decode(new(interface{})); err != io.EOF {
		return fmt.Errorf("unmarshal JSON: %w", ErrInvalidYSON)
	}

	// Convert the raw data into the appropriate Element type
	switch e := elem.(type) {
	case *Array:
		arr, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("unmarshal array: %w", ErrInvalidYSON)
		}
		parsed, err := parseArray(arr)
		if err != nil {
			return err
		}
		*e = parsed
	case *Object:
		obj, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unmarshal object: %w", ErrInvalidYSON)
		}
		parsed, err := parseObject(obj)
		if err != nil {
			return err
		}
		*e = parsed
	case *Tree:
		tree, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unmarshal tree: %w", ErrInvalidYSON)
		}

		if v, ok := tree["value"].(map[string]interface{}); ok {
			parsed, err := parseTree(v)
			if err != nil {
				return err
			}
			*e = parsed
		} else {
			return fmt.Errorf("unmarshal tree: %w", ErrInvalidYSON)
		}
	case *Text:
		text, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unmarshal text: %w", ErrInvalidYSON)
		}

		if v, ok := text["value"].([]interface{}); ok {
			parsed, err := parseText(v)
			if err != nil {
				return err
			}
			*e = parsed
		} else {
			return fmt.Errorf("unmarshal text: %w", ErrInvalidYSON)
		}

	case *Counter:
		rawMap, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unmarshal counter: %w", ErrInvalidYSON)
		}
		var counter Counter
		var err error
		if t, ok := rawMap["type"].(string); ok && t == "DedupCounter" {
			counter, err = parseDedupCounter(rawMap)
		} else {
			counter, err = parseCounter(rawMap)
		}
		if err != nil {
			return err
		}
		*e = counter
	default:
		return ErrUnsupported
	}

	return nil
}

// parseIntValue decodes a numeric YSON value into an integer of the given bit
// size (32 or 64). Because the JSON is decoded with UseNumber, the value is a
// json.Number and is parsed with strconv.ParseInt so that fractional or
// out-of-range values are rejected with ErrInvalidYSON instead of being
// silently truncated, wrapped, or rounded through float64.
func parseIntValue(v interface{}, bitSize int) (int64, error) {
	n, ok := v.(gojson.Number)
	if !ok {
		return 0, ErrInvalidYSON
	}
	i, err := strconv.ParseInt(n.String(), 10, bitSize)
	if err != nil {
		return 0, ErrInvalidYSON
	}
	return i, nil
}

func parseTypedValue(raw map[string]interface{}) (interface{}, error) {
	t, ok := raw["type"].(string)
	if !ok {
		return nil, ErrUnsupported
	}

	switch t {
	case counterTypeInt:
		v, err := parseIntValue(raw["value"], 32)
		if err != nil {
			return nil, fmt.Errorf("parse int value: %w", err)
		}
		return int32(v), nil
	case counterTypeLong:
		v, err := parseIntValue(raw["value"], 64)
		if err != nil {
			return nil, fmt.Errorf("parse long value: %w", err)
		}
		return v, nil
	case "BinData":
		s, ok := raw["value"].(string)
		if !ok {
			return nil, fmt.Errorf("parse BinData: %w", ErrInvalidYSON)
		}
		val, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("parse BinData: %w", ErrInvalidYSON)
		}

		return val, nil
	case "Date":
		s, ok := raw["value"].(string)
		if !ok {
			return nil, fmt.Errorf("parse date: %w", ErrInvalidYSON)
		}
		val, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fmt.Errorf("parse date: %w", ErrInvalidYSON)
		}

		return val, nil
	case "Counter":
		return parseCounter(raw)
	case "DedupCounter":
		return parseDedupCounter(raw)
	case "Tree":
		if value, ok := raw["value"].(map[string]interface{}); ok {
			return parseTree(value)
		}

		return nil, fmt.Errorf("parse counter: %w", ErrInvalidYSON)
	case "Text":
		if value, ok := raw["value"].([]interface{}); ok {
			return parseText(value)
		}
		return nil, fmt.Errorf("parse text: %w", ErrInvalidYSON)
	}

	return nil, ErrUnsupported
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
			val, err := parsePlainValue(v)
			if err != nil {
				return nil, err
			}
			obj[k] = val
		}
	}
	return obj, nil
}

// parsePlainValue normalizes a non-typed decoded value. Numbers decoded with
// UseNumber arrive as json.Number; untyped (non-integer-typed) numbers are
// converted to float64 to preserve the previous parsing behavior for plain
// JSON numbers.
func parsePlainValue(v interface{}) (interface{}, error) {
	if n, ok := v.(gojson.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("parse number: %w", ErrInvalidYSON)
		}
		return f, nil
	}
	return v, nil
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
			val, err := parsePlainValue(v)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
	}
	return arr, nil
}

func parseCounter(raw map[string]interface{}) (Counter, error) {
	counter := Counter{}
	if value, ok := raw["value"].(map[string]interface{}); ok {
		if t, ok := value["type"].(string); ok {
			switch t {
			case counterTypeInt:
				v, err := parseIntValue(value["value"], 32)
				if err != nil {
					return Counter{}, fmt.Errorf("parse counter value: %w", err)
				}
				counter.Type = crdt.IntegerCnt
				counter.Value = int32(v)
			case counterTypeLong:
				v, err := parseIntValue(value["value"], 64)
				if err != nil {
					return Counter{}, fmt.Errorf("parse counter value: %w", err)
				}
				counter.Type = crdt.LongCnt
				counter.Value = v
			default:
				return Counter{}, fmt.Errorf("parse counter type: %w", ErrUnsupported)
			}
		} else {
			return Counter{}, fmt.Errorf("parse counter type: %w", ErrUnsupported)
		}
	} else {
		return Counter{}, fmt.Errorf("parse counter value: %w", ErrUnsupported)
	}
	return counter, nil
}

func parseDedupCounter(raw map[string]interface{}) (Counter, error) {
	counterType, ok := raw["counterType"].(string)
	if !ok {
		return Counter{}, fmt.Errorf("parse dedup counter type: %w", ErrUnsupported)
	}
	hllStr, ok := raw["hll"].(string)
	if !ok {
		return Counter{}, fmt.Errorf("parse dedup counter hll: %w", ErrUnsupported)
	}

	registers, err := base64.StdEncoding.DecodeString(hllStr)
	if err != nil {
		return Counter{}, fmt.Errorf("parse dedup counter hll: %w", ErrInvalidYSON)
	}

	switch counterType {
	case counterTypeInt:
		v, err := parseIntValue(raw["value"], 32)
		if err != nil {
			return Counter{}, fmt.Errorf("parse dedup counter value: %w", err)
		}
		return Counter{
			Type:      crdt.IntegerDedupCnt,
			Value:     int32(v),
			Registers: registers,
		}, nil
	default:
		return Counter{}, fmt.Errorf("parse dedup counter type: %w", ErrUnsupported)
	}
}

func parseText(raw []interface{}) (Text, error) {
	var text Text

	for _, node := range raw {
		n, ok := node.(map[string]interface{})
		if !ok {
			return text, fmt.Errorf("parse text node: %w", ErrInvalidYSON)
		}
		textNode := TextNode{}

		if val, ok := n["val"].(string); !ok {
			return text, fmt.Errorf("parse text value: %w", ErrInvalidYSON)
		} else {
			textNode.Value = val
		}

		if attrsVal, present := n["attrs"]; present {
			attrs, ok := attrsVal.(map[string]interface{})
			if !ok {
				return text, fmt.Errorf("parse text attribute: %w", ErrInvalidYSON)
			}
			textNode.Attributes = make(map[string]string)
			for k, v := range attrs {
				s, ok := v.(string)
				if !ok {
					return text, fmt.Errorf("parse text attribute: %w", ErrInvalidYSON)
				}
				textNode.Attributes[k] = s
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

	if attrsVal, present := raw["attrs"]; present {
		attrs, ok := attrsVal.(map[string]interface{})
		if !ok {
			return TreeNode{}, fmt.Errorf("parse tree node attribute: %w", ErrInvalidYSON)
		}
		node.Attributes = make(map[string]string)
		for k, v := range attrs {
			s, ok := v.(string)
			if !ok {
				return TreeNode{}, fmt.Errorf("parse tree node attribute: %w", ErrInvalidYSON)
			}
			node.Attributes[k] = s
		}
	}

	if childrenVal, present := raw["children"]; present {
		children, ok := childrenVal.([]interface{})
		if !ok {
			return TreeNode{}, fmt.Errorf("parse tree node children: %w", ErrInvalidYSON)
		}
		for _, child := range children {
			childRaw, ok := child.(map[string]interface{})
			if !ok {
				return TreeNode{}, fmt.Errorf("parse tree node child: %w", ErrInvalidYSON)
			}
			childNode, err := parseTreeNode(childRaw)
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
func preprocessTypeValues(data string) string {
	// Pre-substitute DedupCounter into complete JSON before general
	// replacements. The compound structure (Int(...) + bare base64 string)
	// is incompatible with the global ')' → '}' replacement.
	data = dedupCounterRe.ReplaceAllString(data,
		`{"type":"DedupCounter","counterType":"Int","value":$1,"hll":"$2"}`)

	type replacement struct {
		oldStr string
		newStr string
	}

	// Replace custom types with JSON-compatible formats in a specific order
	replacements := []replacement{
		// Process empty constructors first
		{`Text()`, `{"type":"Text","value":[]}`},
		{`Tree()`, `{"type":"Tree","value":{}}`},

		// Process constructors with values
		{`Counter(`, `{"type":"Counter","value":`},
		{`Text(`, `{"type":"Text","value":`},
		{`Tree(`, `{"type":"Tree","value":`},
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

	return data
}

// ParseObject parses a string representation of a YSON object into the
// corresponding Object type.
func ParseObject(data string) Object {
	var obj Object
	if err := Unmarshal(data, &obj); err != nil {
		panic("parse object" + err.Error())
	}
	return obj
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
