/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package crdt

// PROTOTYPE -- a ZeroTree made from Zero content (the JS SDK's `toTokens`)
// and the JSON forms of content and schema. The server needs them to rebuild
// a document from YSON: restoring a revision, verifying a compaction.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ErrInvalidZeroContent is returned for content that is not Zero nodes.
var ErrInvalidZeroContent = errors.New("invalid zero tree content")

// ZeroJSON writes a JSON value (as encoding/json decodes it with UseNumber)
// the way JSON.stringify does, object keys sorted.
func ZeroJSON(v any) string {
	var b bytes.Buffer
	writeZeroJSON(&b, v)
	return b.String()
}

func writeZeroJSON(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		b.WriteString(x.String())
	case float64:
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	case int:
		b.WriteString(strconv.Itoa(x))
	case string:
		jsQuote(b, x)
	case []string:
		list := make([]any, len(x))
		for i, s := range x {
			list[i] = s
		}
		writeZeroJSON(b, list)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeZeroJSON(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jsQuote(b, k)
			b.WriteByte(':')
			writeZeroJSON(b, x[k])
		}
		b.WriteByte('}')
	default:
		jsQuote(b, fmt.Sprint(x))
	}
}

// ParseZeroJSON decodes JSON keeping number literals.
func ParseZeroJSON(raw string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse zero tree content: %w", err)
	}
	return v, nil
}

// zeroIn is a Zero node of input content.
type zeroIn struct {
	typ       string
	children  []*zeroIn
	data      any
	hasData   bool
	style     map[string]any
	attribute map[string]any
	col       *string
}

func parseZeroIn(v any) (*zeroIn, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalidZeroContent
	}
	typ, ok := m["type"].(string)
	if !ok {
		return nil, ErrInvalidZeroContent
	}
	n := &zeroIn{typ: typ}
	n.data, n.hasData = m["data"]
	if s, ok := m["style"].(map[string]any); ok {
		n.style = s
	}
	if a, ok := m["attribute"].(map[string]any); ok {
		n.attribute = a
	}
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			child, err := parseZeroIn(c)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, child)
		}
	}
	return n, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// flattenProps is the JS SDK's `flattenProps` (nil value: remove).
func flattenProps(n *zeroIn, isText bool) []ZeroAttrWrite {
	var writes []ZeroAttrWrite
	add := func(key string, v any) {
		if v == nil {
			writes = append(writes, ZeroAttrWrite{Key: key, Removed: true})
		} else {
			writes = append(writes, ZeroAttrWrite{Key: key, Value: ZeroJSON(v)})
		}
	}
	for _, k := range sortedKeys(n.style) {
		add("style."+k, n.style[k])
	}
	for _, k := range sortedKeys(n.attribute) {
		add("attribute."+k, n.attribute[k])
	}
	if !isText && n.hasData {
		if obj, ok := n.data.(map[string]any); ok {
			for _, k := range sortedKeys(obj) {
				add("data."+k, obj[k])
			}
		} else {
			add("data", n.data)
		}
	}
	return writes
}

func jsTicketID(t *time.Ticket) string {
	return strconv.FormatInt(t.Lamport(), 10) + ":" + t.ActorID().String() + ":" +
		strconv.FormatUint(uint64(t.Delimiter()), 10)
}

func assignColumns(n *zeroIn, base string, schema *ZeroSchema) {
	grid := schema.Grid
	if grid == nil {
		return
	}
	var rows []*zeroIn
	switch n.typ {
	case grid.Table:
		rows = n.children
	case grid.Row:
		rows = []*zeroIn{n}
	}
	for _, row := range rows {
		for c, cell := range row.children {
			if cell.typ == grid.Cell && cell.col == nil {
				key := base + "." + strconv.Itoa(c)
				cell.col = &key
			}
		}
	}
}

// zeroTokensOf is the JS SDK's `toTokens`: markers carry the creation
// ticket, table cells get column keys (`zt.col`).
func zeroTokensOf(nodes []*zeroIn, depth int, ticket *time.Ticket, schema *ZeroSchema) []ZeroToken {
	var tokens []ZeroToken
	for i, n := range nodes {
		base := jsTicketID(ticket) + "." + strconv.Itoa(depth) + "." + strconv.Itoa(i)
		assignColumns(n, base, schema)
		if schema.Grid != nil && n.typ == schema.Grid.Cell && n.col == nil {
			key := base
			n.col = &key
		}
	}
	for _, n := range nodes {
		isText := schema.isText(n.typ)
		writes := flattenProps(n, isText)
		if n.col != nil {
			writes = append(writes, ZeroAttrWrite{Key: "zt.col", Value: ZeroJSON(*n.col)})
		}
		marker := &ZeroMarker{Type: n.typ, Depth: depth}
		for _, w := range writes {
			if !w.Removed {
				marker.Attrs = append(marker.Attrs, &ZeroAttr{Key: w.Key, Value: w.Value, UpdatedAt: ticket})
			}
		}
		tokens = append(tokens, ZeroToken{Marker: marker})
		if isText {
			text := ""
			switch d := n.data.(type) {
			case string:
				text = d
			case nil:
			default:
				text = ZeroJSON(d)
			}
			for _, r := range text {
				tokens = append(tokens, ZeroToken{Char: r})
			}
		} else if len(n.children) > 0 {
			tokens = append(tokens, zeroTokensOf(n.children, depth+1, ticket, schema)...)
		}
	}
	return tokens
}

// NewZeroTreeFromContent creates a ZeroTree holding the given Zero content
// (a `content` node or its children), built with the given schema.
func NewZeroTreeFromContent(createdAt *time.Ticket, content any, schema *ZeroSchema) (*ZeroTree, error) {
	if schema == nil {
		schema = DefaultZeroSchema()
	}
	var children []*zeroIn
	switch c := content.(type) {
	case map[string]any:
		root, err := parseZeroIn(c)
		if err != nil {
			return nil, err
		}
		children = root.children
	case []any:
		for _, v := range c {
			n, err := parseZeroIn(v)
			if err != nil {
				return nil, err
			}
			children = append(children, n)
		}
	case nil:
	default:
		return nil, ErrInvalidZeroContent
	}
	rga := NewRGATreeSplit(InitialZeroTreeNode())
	if tokens := zeroTokensOf(children, 1, createdAt, schema); len(tokens) > 0 {
		node := NewRGATreeSplitNode(NewRGATreeSplitNodeID(createdAt, 0), NewZeroTokens(tokens, ""))
		rga.InsertAfter(rga.InitialHead(), node)
	}
	tree := NewZeroTree(rga, createdAt)
	tree.schema = schema
	return tree, nil
}

func childrenJSON(list []ZeroSchemaChildren, one bool) []any {
	out := make([]any, 0, len(list))
	for _, e := range list {
		if one {
			child := ""
			if len(e.Children) > 0 {
				child = e.Children[0]
			}
			out = append(out, []any{e.Type, child})
		} else {
			out = append(out, []any{e.Type, toAnyList(e.Children)})
		}
	}
	return out
}

func toAnyList(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// SchemaJSON is the JSON form of a schema (lists of pairs: order kept).
func (s *ZeroSchema) SchemaJSON() map[string]any {
	out := map[string]any{
		"textType":     s.TextType,
		"textTypes":    toAnyList(s.TextTypes),
		"scoped":       toAnyList(s.Scoped),
		"single":       childrenJSON(s.Single, false),
		"rootType":     s.RootType,
		"atomic":       toAnyList(s.Atomic),
		"defaultChild": childrenJSON(s.DefaultChild, true),
		"allowed":      childrenJSON(s.Allowed, false),
	}
	if s.Grid != nil {
		out["grid"] = map[string]any{"table": s.Grid.Table, "row": s.Grid.Row, "cell": s.Grid.Cell}
	}
	if len(s.ObjectData) > 0 {
		out["objectData"] = toAnyList(s.ObjectData)
	}
	return out
}

func stringList(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok && v != nil {
		return nil, ErrInvalidZeroContent
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, ErrInvalidZeroContent
		}
		out = append(out, s)
	}
	return out, nil
}

func pairsOf(v any, one bool) ([]ZeroSchemaChildren, error) {
	list, ok := v.([]any)
	if !ok && v != nil {
		return nil, ErrInvalidZeroContent
	}
	var out []ZeroSchemaChildren
	for _, e := range list {
		pair, ok := e.([]any)
		if !ok || len(pair) != 2 {
			return nil, ErrInvalidZeroContent
		}
		typ, ok := pair[0].(string)
		if !ok {
			return nil, ErrInvalidZeroContent
		}
		var children []string
		if one {
			child, ok := pair[1].(string)
			if !ok {
				return nil, ErrInvalidZeroContent
			}
			children = []string{child}
		} else {
			var err error
			if children, err = stringList(pair[1]); err != nil {
				return nil, err
			}
		}
		out = append(out, ZeroSchemaChildren{Type: typ, Children: children})
	}
	return out, nil
}

// ZeroSchemaFromJSON parses the JSON form of a schema.
func ZeroSchemaFromJSON(v any) (*ZeroSchema, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalidZeroContent
	}
	s := &ZeroSchema{}
	s.TextType, _ = m["textType"].(string)
	s.RootType, _ = m["rootType"].(string)
	var err error
	if s.TextTypes, err = stringList(m["textTypes"]); err != nil {
		return nil, err
	}
	if s.Scoped, err = stringList(m["scoped"]); err != nil {
		return nil, err
	}
	if s.Atomic, err = stringList(m["atomic"]); err != nil {
		return nil, err
	}
	if s.ObjectData, err = stringList(m["objectData"]); err != nil {
		return nil, err
	}
	if s.Single, err = pairsOf(m["single"], false); err != nil {
		return nil, err
	}
	if s.DefaultChild, err = pairsOf(m["defaultChild"], true); err != nil {
		return nil, err
	}
	if s.Allowed, err = pairsOf(m["allowed"], false); err != nil {
		return nil, err
	}
	if g, ok := m["grid"].(map[string]any); ok {
		s.Grid = &ZeroSchemaGrid{}
		s.Grid.Table, _ = g["table"].(string)
		s.Grid.Row, _ = g["row"].(string)
		s.Grid.Cell, _ = g["cell"].(string)
	}
	if s.TextType == "" || s.RootType == "" {
		return nil, ErrInvalidZeroContent
	}
	return s, nil
}
