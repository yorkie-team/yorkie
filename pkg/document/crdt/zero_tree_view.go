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

// PROTOTYPE -- the Zero Tree model on the server: the same pure function of
// the live tokens as the JS SDK's zero_tree_view.ts (`dealias`, `buildView`,
// `toNodeJSON`, `stableStringify`), so `ZeroTree.Marshal` is the Zero
// content a client shows. The event compiler stays client side.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ZeroSchemaChildren is the child types of one type, in order.
type ZeroSchemaChildren struct {
	Type     string
	Children []string
}

// ZeroSchemaGrid names the table types.
type ZeroSchemaGrid struct {
	Table, Row, Cell string
}

// ZeroSchema is the part of the editor schema the model needs (see the JS
// SDK's `ZeroSchema`). Lists keep their order: it decides which child fills
// a gap.
type ZeroSchema struct {
	TextType     string
	TextTypes    []string
	Scoped       []string
	Single       []ZeroSchemaChildren
	Grid         *ZeroSchemaGrid
	RootType     string
	Atomic       []string
	DefaultChild []ZeroSchemaChildren // one child each
	Allowed      []ZeroSchemaChildren

	compiled *compiledSchema
}

type compiledSchema struct {
	textTypes, atomic map[string]bool
	single            map[string]map[string]bool
	defaultChild      map[string]string
	allowed           map[string][]string
	allowedSet        map[string]map[string]bool
}

// DefaultZeroSchema is the JS SDK's default (Zero's own types).
func DefaultZeroSchema() *ZeroSchema {
	return &ZeroSchema{
		TextType:  "inline-text",
		TextTypes: []string{"inline-text"},
		Scoped:    []string{"table", "table-row", "table-cell", "table-cell-body"},
		Grid:      &ZeroSchemaGrid{Table: "table", Row: "table-row", Cell: "table-cell"},
		RootType:  "content",
		Atomic:    []string{"inline-image"},
		DefaultChild: []ZeroSchemaChildren{
			{"content", []string{"paragraph"}},
			{"paragraph", []string{"inline-text"}},
			{"inline-hyperlink", []string{"inline-text"}},
			{"table", []string{"table-row"}},
			{"table-row", []string{"table-cell"}},
			{"table-cell", []string{"table-cell-body"}},
			{"table-cell-body", []string{"paragraph"}},
		},
		Allowed: []ZeroSchemaChildren{
			{"content", []string{"paragraph", "table"}},
			{"paragraph", []string{"inline-text", "inline-image", "inline-hyperlink"}},
			{"inline-hyperlink", []string{"inline-text", "inline-image"}},
			{"table", []string{"table-row"}},
			{"table-row", []string{"table-cell"}},
			{"table-cell", []string{"table-cell-body"}},
			{"table-cell-body", []string{"paragraph", "table"}},
		},
	}
}

func setOf(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, v := range list {
		m[v] = true
	}
	return m
}

func (s *ZeroSchema) c() *compiledSchema {
	if s.compiled != nil {
		return s.compiled
	}
	c := &compiledSchema{
		textTypes:    setOf(s.TextTypes),
		atomic:       setOf(s.Atomic),
		single:       map[string]map[string]bool{},
		defaultChild: map[string]string{},
		allowed:      map[string][]string{},
		allowedSet:   map[string]map[string]bool{},
	}
	for _, e := range s.Single {
		c.single[e.Type] = setOf(e.Children)
	}
	for _, e := range s.DefaultChild {
		if len(e.Children) > 0 {
			c.defaultChild[e.Type] = e.Children[0]
		}
	}
	for _, e := range s.Allowed {
		c.allowed[e.Type] = e.Children
		c.allowedSet[e.Type] = setOf(e.Children)
	}
	s.compiled = c
	return c
}

func (s *ZeroSchema) isText(t string) bool {
	return t == s.TextType || s.c().textTypes[t]
}

func (s *ZeroSchema) isContainer(t string) bool {
	return !s.isText(t) && !s.c().atomic[t]
}

func (s *ZeroSchema) accepts(parent, child string) bool {
	if !s.isContainer(parent) {
		return false
	}
	set, ok := s.c().allowedSet[parent]
	return !ok || set[child]
}

func (s *ZeroSchema) textTypeFor(t string) string {
	if def := s.c().defaultChild[t]; def != "" && s.isText(def) {
		return def
	}
	for _, child := range s.c().allowed[t] {
		if s.isText(child) {
			return child
		}
	}
	return s.TextType
}

// zeroRegister is one LWW register as the model reads it (nil value: null).
type zeroRegister struct {
	value *string
	at    *time.Ticket
}

// omap is a string-keyed map that keeps insertion order, as a JS object does.
type omap[V any] struct {
	keys []string
	vals map[string]V
}

func newOmap[V any]() *omap[V] { return &omap[V]{vals: map[string]V{}} }

func (m *omap[V]) get(k string) (V, bool) {
	v, ok := m.vals[k]
	return v, ok
}

func (m *omap[V]) set(k string, v V) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *omap[V]) copy() *omap[V] {
	c := &omap[V]{keys: append([]string(nil), m.keys...), vals: make(map[string]V, len(m.vals))}
	maps.Copy(c.vals, m.vals)
	return c
}

type viewMarker struct {
	typ       string
	depth     int
	attrs     *omap[string]
	boundary  string
	registers *omap[zeroRegister]
}

type viewEntry struct {
	id      string
	char    string
	marker  *viewMarker
	aliases []string
	scope   string
}

// zeroIDString is the JS SDK's `RGATreeSplitNodeID.toIDString` (token ids
// are compared with the scope strings clients wrote).
func zeroIDString(createdAt *time.Ticket, offset int) string {
	return strconv.FormatInt(createdAt.Lamport(), 10) + ":" + createdAt.ActorID().String() + ":" +
		strconv.FormatUint(uint64(createdAt.Delimiter()), 10) + ":" + strconv.Itoa(offset)
}

func (t *ZeroTree) liveEntries() []viewEntry {
	var entries []viewEntry
	for _, node := range t.Nodes() {
		if node.removedAt != nil || node.value == nil {
			continue
		}
		base := node.id.offset
		for i, tok := range node.value.Tokens {
			id := zeroIDString(node.id.createdAt, base+i)
			if tok.Marker == nil {
				ch := string(tok.Char)
				if tok.Ghost {
					ch = "\x00"
				}
				entries = append(entries, viewEntry{id: id, char: ch, scope: node.value.Scope})
				continue
			}
			attrs, regs := newOmap[string](), newOmap[zeroRegister]()
			for _, a := range tok.Marker.Attrs {
				var v *string
				if !a.Removed {
					val := a.Value
					v = &val
					attrs.set(a.Key, val)
				}
				regs.set(a.Key, zeroRegister{value: v, at: a.UpdatedAt})
			}
			entries = append(entries, viewEntry{id: id, scope: node.value.Scope, marker: &viewMarker{
				typ: tok.Marker.Type, depth: tok.Marker.Depth, attrs: attrs,
				boundary: tok.Marker.Boundary, registers: regs,
			}})
		}
	}
	return entries
}

func mergeRegisters(into, from *omap[zeroRegister]) (*omap[zeroRegister], *omap[string]) {
	regs := newOmap[zeroRegister]()
	if into != nil {
		regs = into.copy()
	}
	if from != nil {
		for _, k := range from.keys {
			reg := from.vals[k]
			if cur, ok := regs.get(k); !ok || reg.at.After(cur.at) {
				regs.set(k, reg)
			}
		}
	}
	attrs := newOmap[string]()
	for _, k := range regs.keys {
		if v := regs.vals[k].value; v != nil {
			attrs.set(k, *v)
		}
	}
	return regs, attrs
}

func parseJSONString(raw string) (string, bool) {
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return "", false
	}
	return s, true
}

type dealiasOpen struct {
	depth int
	typ   string
	seen  map[string]bool
	attrs *omap[string]
}

// dealias folds the markers of concurrent splits of one boundary into the
// first split there, and leaves out what a gone scope marker, a deleted grid
// column or a second `single` child hides (JS `dealias`).
//
//nolint:gocyclo // PROTOTYPE: mirrors dealias in the JS SDK one to one; split once the design settles
func dealias(entries []viewEntry, schema *ZeroSchema) []viewEntry {
	var out []viewEntry
	firstRuns := map[string][]int{}
	shown := map[string]bool{}
	open := []dealiasOpen{{depth: 0, typ: schema.RootType, seen: map[string]bool{}}}
	hideDepth := math.MaxInt
	var into []int
	isFirst := false
	j := 0
	for i := range entries {
		entry := entries[i]
		if entry.scope != "" && !shown[entry.scope] {
			continue
		}
		if entry.marker != nil && entry.marker.depth <= hideDepth {
			hideDepth = math.MaxInt
		}
		if hideDepth != math.MaxInt {
			continue
		}
		if m := entry.marker; m != nil {
			depth := m.depth
			for len(open) > 1 && open[len(open)-1].depth >= depth {
				open = open[:len(open)-1]
			}
			parent := &open[len(open)-1]
			if col, ok := m.attrs.get("zt.col"); ok && schema.Grid != nil && m.typ == schema.Grid.Cell {
				hidden := false
				for k := len(open) - 1; k >= 0; k-- {
					if open[k].typ == schema.Grid.Table {
						if key, ok := parseJSONString(col); ok && open[k].attrs != nil {
							_, hidden = open[k].attrs.get("zt.del." + key)
						}
						break
					}
				}
				if hidden {
					hideDepth = depth
					continue
				}
			}
			if m.boundary == "" && schema.c().single[parent.typ][m.typ] {
				if parent.seen[m.typ] {
					hideDepth = depth
					continue
				}
				parent.seen[m.typ] = true
			}
			open = append(open, dealiasOpen{depth: depth, typ: m.typ, seen: map[string]bool{}, attrs: m.attrs})
			shown[entry.id] = true
		}
		key := ""
		if entry.marker != nil {
			key = entry.marker.boundary
		}
		if key == "" {
			out = append(out, entry)
			continue
		}
		var prev *viewMarker
		if i > 0 {
			prev = entries[i-1].marker
		}
		if prev != nil && prev.boundary == key && entry.marker.depth > prev.depth {
			j++
		} else {
			j = 0
			var ok bool
			into, ok = firstRuns[key]
			isFirst = !ok
			if isFirst {
				firstRuns[key] = []int{}
				into = nil
			}
		}
		if !isFirst && j < len(into) {
			target := out[into[j]]
			if target.marker.typ == entry.marker.typ && target.marker.depth == entry.marker.depth {
				regs, attrs := mergeRegisters(target.marker.registers, entry.marker.registers)
				nm := *target.marker
				nm.attrs, nm.registers = attrs, regs
				target.marker = &nm
				target.aliases = append(append([]string(nil), target.aliases...), entry.id)
				out[into[j]] = target
				continue
			}
		}
		if isFirst {
			firstRuns[key] = append(firstRuns[key], len(out))
		}
		out = append(out, entry)
	}
	return out
}

// zNode is a node of the model (JS `ZNode`).
type zNode struct {
	typ   string
	depth int
	// level is the node's place in the stored-depth space the markers are
	// written in (JS `ZNode.level`): a real node's marker depth, a virtual one
	// between its parent and the marker it was opened for.
	level float64
	// anchor is, for a virtual node squeezed in below the marker it was
	// opened for, that marker's depth (0: none). A later marker of that depth
	// stays in it only if the node holds that type, directly or through its
	// default children (JS `ZNode.anchor`).
	anchor    float64
	attrs     *omap[string]
	markerID  string
	virtual   bool
	children  []*zNode
	text      strings.Builder
	start     int
	end       int
	textStart int
	group     []string
	col       *string
	registers *omap[zeroRegister]
}

func contains(list []string, v string) bool {
	return slices.Contains(list, v)
}

// buildView builds the Zero model from the live tokens (JS `buildView`).
//
//nolint:gocyclo // PROTOTYPE: mirrors buildView in the JS SDK one to one; split once the design settles
func buildView(entries []viewEntry, schema *ZeroSchema) *zNode {
	root := &zNode{typ: schema.RootType, attrs: newOmap[string](), end: len(entries)}
	stack := []*zNode{root}
	top := func() *zNode { return stack[len(stack)-1] }
	pop := func() *zNode {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return n
	}

	openNode := func(typ string, depth, at int, attrs *omap[string], markerID string) *zNode {
		if attrs == nil {
			attrs = newOmap[string]()
		}
		parent := top()
		node := &zNode{typ: typ, depth: depth, level: parent.level + 1, attrs: attrs, markerID: markerID,
			virtual: markerID == "", start: at, end: at, textStart: at}
		if markerID != "" {
			node.textStart = at + 1
		}
		parent.children = append(parent.children, node)
		stack = append(stack, node)
		return node
	}

	var ensureChildren func(node *zNode, at int)
	ensureChildren = func(node *zNode, at int) {
		if !schema.isContainer(node.typ) || len(node.children) > 0 {
			return
		}
		childType := schema.c().defaultChild[node.typ]
		if childType == "" {
			return
		}
		stack = append(stack, node)
		child := openNode(childType, node.depth+1, at, nil, "")
		ensureChildren(child, at)
		child.end = at
		pop()
		pop()
	}

	closeNode := func(at int) {
		node := pop()
		node.end = max(node.end, at)
		ensureChildren(node, node.end)
	}

	var candidatesOf func(from string) []string
	var reaches func(from, typ string, budget int) bool
	candidatesOf = func(from string) []string {
		var list []string
		def := schema.c().defaultChild[from]
		if def != "" {
			list = append(list, def)
		}
		for _, c := range schema.c().allowed[from] {
			if c != def && schema.isContainer(c) {
				list = append(list, c)
			}
		}
		var out []string
		for _, c := range list {
			if schema.isContainer(c) && c != from {
				out = append(out, c)
			}
		}
		return out
	}
	reaches = func(from, typ string, budget int) bool {
		if schema.accepts(from, typ) {
			return true
		}
		if budget <= 0 || !schema.isContainer(from) {
			return false
		}
		for _, c := range candidatesOf(from) {
			if reaches(c, typ, budget-1) {
				return true
			}
		}
		return false
	}
	// holds tells whether from holds typ directly or down its default children.
	var holds func(from, typ string, budget int) bool
	holds = func(from, typ string, budget int) bool {
		if schema.accepts(from, typ) {
			return true
		}
		def := schema.c().defaultChild[from]
		return budget > 0 && def != "" && def != from && schema.isContainer(def) && holds(def, typ, budget-1)
	}
	fillFor := func(from, typ string) string {
		for _, c := range candidatesOf(from) {
			if reaches(c, typ, 5) {
				return c
			}
		}
		return ""
	}
	// anchor is the marker depth of what is being placed (+Inf for
	// characters). Virtual nodes opened for it get levels between the parent's
	// and the anchor's, so a later marker of the anchor's depth (its sibling as
	// written) does not close them (JS `settle`).
	settle := func(typ string, at int, anchor float64) {
		var fills []*zNode
		var p float64
		place := func() {
			if len(fills) == 0 {
				return
			}
			n := float64(len(fills))
			for j, f := range fills {
				// under the root, keep whole levels: a depth-1 marker must
				// close everything before it
				if p == 0 || anchor-p-1 >= n {
					f.level = p + float64(j) + 1
				} else {
					f.level = p + (float64(j+1)*(anchor-p))/(n+1)
					f.anchor = anchor
				}
			}
		}
		for range 64 {
			parent := top()
			if schema.accepts(parent.typ, typ) {
				place()
				return
			}
			fill := ""
			if schema.isContainer(parent.typ) {
				fill = fillFor(parent.typ, typ)
			}
			if fill != "" {
				if len(fills) == 0 {
					p = parent.level
				}
				fills = append(fills, openNode(fill, parent.depth+1, at, nil, ""))
				continue
			}
			if parent == root {
				place()
				return
			}
			closeNode(at)
		}
	}

	for i := range entries {
		entry := entries[i]
		if m := entry.marker; m != nil {
			depth := max(1, m.depth)
			current := top()
			if i > 0 && m.boundary != "" {
				if prev := entries[i-1]; prev.marker != nil && prev.marker.boundary == m.boundary &&
					prev.marker.typ == m.typ && prev.marker.depth == m.depth && contains(current.group, prev.id) {
					current.registers, current.attrs = mergeRegisters(current.registers, m.registers)
					current.group = append(current.group, entry.id)
					current.textStart = i + 1
					current.end = i + 1
					continue
				}
			}
			for top().level >= float64(depth) ||
				(top().anchor == float64(depth) && !holds(top().typ, m.typ, 5)) {
				closeNode(i)
			}
			settle(m.typ, i, float64(depth))
			node := openNode(m.typ, top().depth+1, i, m.attrs, entry.id)
			node.level = float64(depth)
			node.group = append([]string{entry.id}, entry.aliases...)
			node.registers = m.registers
			if col, ok := m.attrs.get("zt.col"); ok {
				if key, ok := parseJSONString(col); ok {
					node.col = &key
				}
			}
			continue
		}
		if !schema.isText(top().typ) {
			textType := schema.textTypeFor(top().typ)
			if !schema.accepts(top().typ, textType) {
				settle(schema.TextType, i, math.Inf(1))
				textType = schema.textTypeFor(top().typ)
			}
			openNode(textType, top().depth+1, i, nil, "")
		}
		holder := top()
		if holder.text.Len() == 0 && holder.virtual {
			holder.textStart = i
		}
		holder.text.WriteString(entry.char)
		for _, n := range stack {
			n.end = i + 1
		}
	}
	for len(stack) > 1 {
		closeNode(len(entries))
	}
	ensureChildren(root, len(entries))

	var padColumns func(node *zNode)
	padColumns = func(node *zNode) {
		grid := schema.Grid
		if grid == nil {
			return
		}
		for _, child := range node.children {
			padColumns(child)
		}
		if node.typ != grid.Table {
			return
		}
		var rows []*zNode
		for _, r := range node.children {
			if r.typ == grid.Row {
				rows = append(rows, r)
			}
		}
		var order []string
		indexOf := func(k string) int {
			for i, v := range order {
				if v == k {
					return i
				}
			}
			return -1
		}
		for _, row := range rows {
			at := 0
			for _, cell := range row.children {
				if cell.col == nil {
					continue
				}
				if k := indexOf(*cell.col); k < 0 {
					order = append(order[:at], append([]string{*cell.col}, order[at:]...)...)
					at++
				} else {
					at = max(at, k+1)
				}
			}
		}
		spanKeys := false
		for _, r := range rows {
			for _, c := range r.children {
				if _, ok := c.attrs.get("attribute.colspan"); ok {
					spanKeys = true
				}
			}
		}
		for _, row := range rows {
			have := map[string]bool{}
			allKeyed := true
			for _, c := range row.children {
				if c.col == nil {
					allKeyed = false
				} else {
					have[*c.col] = true
				}
			}
			if !allKeyed {
				continue
			}
			for k, key := range order {
				if have[key] {
					continue
				}
				var next *zNode
				for _, later := range order[k+1:] {
					for _, c := range row.children {
						if c.col != nil && *c.col == later {
							next = c
							break
						}
					}
					if next != nil {
						break
					}
				}
				at := row.end
				if next != nil {
					at = next.start
				}
				stack = append(stack, row)
				cell := openNode(grid.Cell, row.depth+1, at, nil, "")
				pop()
				pop()
				colKey := key
				cell.col = &colKey
				cell.attrs = newOmap[string]()
				if spanKeys {
					cell.attrs.set("attribute.colspan", "1")
					cell.attrs.set("attribute.rowspan", "1")
					cell.attrs.set("attribute.spanned", "false")
				}
				row.children = row.children[:len(row.children)-1]
				idx := len(row.children)
				if next != nil {
					for p, c := range row.children {
						if c == next {
							idx = p
							break
						}
					}
				}
				row.children = append(row.children[:idx], append([]*zNode{cell}, row.children[idx:]...)...)
				ensureChildren(cell, at)
				cell.end = at
				have[key] = true
			}
		}
	}
	padColumns(root)
	return root
}

// jsonValue parses an attribute value (JSON) keeping number literals.
func jsonValue(raw string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// nodeJSON is the Zero JSON of a model node (JS `toNodeJSON`), as a generic
// value for `stableStringify`.
func nodeJSON(node *zNode, schema *ZeroSchema) map[string]any {
	style, attribute := map[string]any{}, map[string]any{}
	var data any
	hasData := false
	for _, key := range node.attrs.keys {
		value, ok := jsonValue(node.attrs.vals[key])
		if !ok || value == nil {
			continue
		}
		group, name := key, ""
		if before, after, ok0 := strings.Cut(key, "."); ok0 {
			group, name = before, after
		}
		switch group {
		case "style":
			style[name] = value
		case "attribute":
			attribute[name] = value
		case "data":
			if name == "" {
				data, hasData = value, true
			} else {
				obj, ok := data.(map[string]any)
				if !ok {
					obj = map[string]any{}
				}
				obj[name] = value
				data, hasData = obj, true
			}
		}
	}
	out := map[string]any{"type": node.typ}
	if len(style) > 0 {
		out["style"] = style
	}
	if len(attribute) > 0 {
		out["attribute"] = attribute
	}
	if schema.isText(node.typ) {
		out["data"] = node.text.String()
	} else if hasData {
		out["data"] = data
	}
	if schema.isContainer(node.typ) {
		children := make([]any, 0, len(node.children))
		for _, c := range node.children {
			children = append(children, nodeJSON(c, schema))
		}
		out["children"] = children
	}
	return out
}

// jsQuote quotes a string as JSON.stringify does.
func jsQuote(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else if r == utf8.RuneError {
				b.WriteRune(r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func isEmptyContainer(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// stableStringify is the JS SDK's `stableStringify`: sorted keys, no `id`
// keys, no empty `style`/`attribute`.
func stableStringify(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		b.WriteString(x.String())
	case string:
		jsQuote(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			stableStringify(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			if k == "id" || ((k == "style" || k == "attribute") && isEmptyContainer(x[k])) {
				continue
			}
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
			stableStringify(b, x[k])
		}
		b.WriteByte('}')
	default:
		jsQuote(b, fmt.Sprint(x))
	}
}

// Schema returns the schema this tree builds its model with.
func (t *ZeroTree) Schema() *ZeroSchema {
	if t.schema == nil {
		return DefaultZeroSchema()
	}
	return t.schema
}

// EmbeddedSchema returns the schema the tree carries (nil: the default).
func (t *ZeroTree) EmbeddedSchema() *ZeroSchema { return t.schema }

// SetSchema sets the schema the tree carries.
func (t *ZeroTree) SetSchema(schema *ZeroSchema) { t.schema = schema }

// ContentJSON returns the Zero `content` node as the JS SDK's
// `ZeroTree.toJSON` does.
func (t *ZeroTree) ContentJSON() string {
	schema := t.Schema()
	var b bytes.Buffer
	stableStringify(&b, nodeJSON(buildView(dealias(t.liveEntries(), schema), schema), schema))
	return b.String()
}
