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

// ysonConstructor maps a YSON constructor name to its intermediate JSON
// object type. The scanner rewrites Name(<arg>) into
// {"type":"<type>","value":<arg>} for these constructors.
type ysonConstructor struct {
	name string
	typ  string
}

// ysonConstructors lists the value-carrying YSON constructors. Longer names
// must precede any name that is a prefix of them so a prefix never shadows a
// longer name (e.g. DedupCounter before Counter). Names are matched only at a
// token boundary, so this ordering is defensive rather than strictly required.
var ysonConstructors = []ysonConstructor{
	{"Counter", "Counter"},
	{"Text", "Text"},
	{"Tree", "Tree"},
	{"Int", "Int"},
	{"Long", "Long"},
	{"BinData", "BinData"},
	{"Date", "Date"},
}

// preprocessTypeValues rewrites custom YSON constructors into JSON-compatible
// forms so the result can be decoded with encoding/json.
//
// The rewrite is a single left-to-right, string-aware scan. String literals
// are copied verbatim (honoring \" and \\ escapes) so that brackets, parens,
// or constructor-like substrings appearing inside string values are never
// interpreted as structure. Earlier revisions used global strings.ReplaceAll
// calls (notably ")" → "}"), which silently corrupted such values.
func preprocessTypeValues(data string) string {
	// DedupCounter is handled first by its precise regex. Its compound shape
	// (Int(...) plus a bare base64 string argument) does not fit the generic
	// Name(<arg>) rewrite, and the regex only matches at real DedupCounter
	// call sites. The scanner then leaves the produced JSON untouched. Prose
	// containing the literal text DedupCounter(...) inside a string value is
	// protected because the scanner copies string literals verbatim; the regex
	// pass may edit such prose, but the subsequent scanner would break on the
	// same input regardless, and the marshalers never emit it inside strings.
	data = rewriteDedupCounters(data)

	var b strings.Builder
	b.Grow(len(data) + len(data)/4)
	scanConstructors(&b, data)
	return b.String()
}

// rewriteDedupCounters substitutes DedupCounter(Int(N),"b64") occurrences that
// lie outside string literals with complete intermediate JSON. Occurrences
// inside string literals are left untouched.
func rewriteDedupCounters(data string) string {
	var b strings.Builder
	b.Grow(len(data))
	i := 0
	for i < len(data) {
		c := data[i]
		if c == '"' {
			j := scanStringLiteral(data, i)
			b.WriteString(data[i:j])
			i = j
			continue
		}
		// Gate the regex on a cheap prefix check. Only a match anchored at the
		// current position is used, so without the "DedupCounter(" prefix the
		// regex cannot contribute; skipping it keeps this pass linear instead of
		// rescanning the whole suffix at every byte.
		if strings.HasPrefix(data[i:], "DedupCounter(") {
			if loc := dedupCounterRe.FindStringSubmatchIndex(data[i:]); loc != nil && loc[0] == 0 {
				match := data[i : i+loc[1]]
				b.WriteString(dedupCounterRe.ReplaceAllString(match,
					`{"type":"DedupCounter","counterType":"Int","value":$1,"hll":"$2"}`))
				i += loc[1]
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// scanConstructors walks data left to right, copying string literals verbatim
// and rewriting known constructors at token boundaries. It writes the result
// into b. Malformed input (unbalanced parens, unterminated strings) is copied
// through best-effort; the surrounding decoder then rejects the invalid JSON.
func scanConstructors(b *strings.Builder, data string) {
	i := 0
	for i < len(data) {
		c := data[i]
		if c == '"' {
			j := scanStringLiteral(data, i)
			b.WriteString(data[i:j])
			i = j
			continue
		}

		if name, typ, argStart, ok := matchConstructor(data, i); ok {
			argEnd, found := findMatchingParen(data, argStart)
			if !found {
				// Unbalanced parens: copy the rest verbatim so the decoder
				// reports invalid YSON instead of us panicking.
				b.WriteString(data[i:])
				return
			}

			inner := strings.TrimSpace(data[argStart:argEnd])
			b.WriteString(constructorPrefix(name, typ, inner))
			scanConstructors(b, inner)
			b.WriteByte('}')
			i = argEnd + 1 // skip past ')'
			continue
		}

		b.WriteByte(c)
		i++
	}
}

// matchConstructor reports whether data at position i begins a known
// constructor name that is immediately followed by '(' and sits at a token
// boundary (the preceding char is not an identifier char). On success it
// returns the name, its intermediate type, and the index just after '('.
func matchConstructor(data string, i int) (name, typ string, argStart int, ok bool) {
	if i > 0 && isIdentChar(data[i-1]) {
		return "", "", 0, false
	}
	for _, ctor := range ysonConstructors {
		end := i + len(ctor.name)
		if end < len(data) && data[i:end] == ctor.name && data[end] == '(' {
			return ctor.name, ctor.typ, end + 1, true
		}
	}
	return "", "", 0, false
}

// constructorPrefix returns the JSON prefix emitted before a constructor's
// processed argument. The closing '}' is written separately by the caller.
// Empty Text()/Tree() collapse to a fixed empty value so that Text() and
// Tree() decode as an empty array and object respectively.
func constructorPrefix(name, typ, inner string) string {
	if inner == "" {
		switch name {
		case "Text":
			return `{"type":"Text","value":[]`
		case "Tree":
			return `{"type":"Tree","value":{}`
		}
	}
	return fmt.Sprintf(`{"type":"%s","value":`, typ)
}

// findMatchingParen returns the index of the ')' that closes the '(' whose
// argument starts at argStart, counting nested parens and skipping string
// literals. found is false if no matching paren exists.
func findMatchingParen(data string, argStart int) (int, bool) {
	depth := 1
	i := argStart
	for i < len(data) {
		switch data[i] {
		case '"':
			i = scanStringLiteral(data, i)
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
		i++
	}
	return 0, false
}

// scanStringLiteral returns the index just past the JSON string literal that
// starts at the opening quote data[start]. It honors \" and \\ escapes. If the
// string is unterminated it returns len(data).
func scanStringLiteral(data string, start int) int {
	i := start + 1
	for i < len(data) {
		switch data[i] {
		case '\\':
			i += 2
			continue
		case '"':
			return i + 1
		}
		i++
	}
	return len(data)
}

// isIdentChar reports whether c can appear in a YSON constructor identifier.
func isIdentChar(c byte) bool {
	return c == '_' ||
		('a' <= c && c <= 'z') ||
		('A' <= c && c <= 'Z') ||
		('0' <= c && c <= '9')
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
