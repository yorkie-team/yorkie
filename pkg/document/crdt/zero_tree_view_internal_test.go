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

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// listSchema is ProseMirror's basic schema with lists, in Zero's shape.
func listSchema() *ZeroSchema {
	return &ZeroSchema{
		TextType:  "inline-text",
		TextTypes: []string{"inline-text"},
		RootType:  "content",
		Atomic:    []string{"hard_break"},
		DefaultChild: []ZeroSchemaChildren{
			{"content", []string{"paragraph"}},
			{"paragraph", []string{"inline-text"}},
			{"bullet_list", []string{"list_item"}},
			{"list_item", []string{"paragraph"}},
		},
		Allowed: []ZeroSchemaChildren{
			{"content", []string{"paragraph", "bullet_list"}},
			{"paragraph", []string{"inline-text", "hard_break"}},
			{"bullet_list", []string{"list_item"}},
			{"list_item", []string{"paragraph", "bullet_list"}},
		},
	}
}

// tokens builds live view entries: "<type depth key=value ..." opens a
// marker with those attributes, any other string is characters.
func tokens(parts ...string) []viewEntry {
	var entries []viewEntry
	for _, p := range parts {
		if p[0] == '<' {
			fields := strings.Fields(p[1:])
			depth, err := strconv.Atoi(fields[1])
			if err != nil {
				panic(err)
			}
			attrs := newOmap[string]()
			for _, kv := range fields[2:] {
				k, v, _ := strings.Cut(kv, "=")
				attrs.set(k, v)
			}
			entries = append(entries, viewEntry{
				id:     fmt.Sprintf("m%d", len(entries)),
				marker: &viewMarker{typ: fields[0], depth: depth, attrs: attrs},
			})
			continue
		}
		for _, r := range p {
			entries = append(entries, viewEntry{id: fmt.Sprintf("c%d", len(entries)), char: string(r)})
		}
	}
	return entries
}

func modelOf(entries []viewEntry, schema *ZeroSchema) string {
	var b bytes.Buffer
	stableStringify(&b, nodeJSON(buildView(dealias(entries, schema), schema), schema))
	return b.String()
}

func node(typ string, children ...any) map[string]any {
	return map[string]any{"type": typ, "children": children}
}

func text(data string) map[string]any {
	return map[string]any{"type": "inline-text", "data": data}
}

func jsonOf(v any) string {
	var b bytes.Buffer
	stableStringify(&b, v)
	return b.String()
}

// A marker is written at the depth its author saw. When someone else wraps
// its container concurrently, it shows deeper, inside virtual parents; what
// is inserted at the start of those parents (written at the marker's depth)
// must land in them (the JS SDK's `buildView` follows the same rule).
func TestZeroTreeVirtualParents(t *testing.T) {
	schema := listSchema()
	hardBreak := map[string]any{"type": "hard_break"}

	t.Run("a wrapped marker shows in virtual parents", func(t *testing.T) {
		got := modelOf(tokens(
			"<paragraph 1", "<inline-text 2", "ab",
			"<bullet_list 1", "<list_item 2", "<paragraph 3", "<inline-text 4", "cd",
			"<hard_break 2",
		), schema)
		assert.Equal(t, jsonOf(node("content",
			node("paragraph", text("ab")),
			node("bullet_list",
				node("list_item", node("paragraph", text("cd"))),
				node("list_item", node("paragraph", hardBreak)),
			),
		)), got)
	})

	t.Run("an insert at the start of a virtual parent lands in it", func(t *testing.T) {
		got := modelOf(tokens(
			"<paragraph 1", "<inline-text 2", "ab",
			"<bullet_list 1", "<list_item 2", "<paragraph 3", "<inline-text 4", "cd",
			"<inline-text 2", "Z",
			"<hard_break 2",
		), schema)
		assert.Equal(t, jsonOf(node("content",
			node("paragraph", text("ab")),
			node("bullet_list",
				node("list_item", node("paragraph", text("cd"))),
				node("list_item", node("paragraph", text("Z"), hardBreak)),
			),
		)), got)
	})

	t.Run("a block inserted at the start of a virtual item lands in it", func(t *testing.T) {
		got := modelOf(tokens(
			"<bullet_list 1", "<list_item 2", "<paragraph 3", "<inline-text 4", "cd",
			"<paragraph 2", "<inline-text 3", "P",
			"<hard_break 2",
		), schema)
		assert.Equal(t, jsonOf(node("content",
			node("bullet_list",
				node("list_item", node("paragraph", text("cd"))),
				node("list_item", node("paragraph", text("P")), node("paragraph", hardBreak)),
			),
		)), got)
	})

	t.Run("a marker between two depths holds the wrapped marker", func(t *testing.T) {
		// the paragraph a client gave an attribute: written between the list
		// (depth 1) and the break (depth 2); the level never shows in the model
		got := modelOf(tokens(
			"<bullet_list 1", "<list_item 2", "<paragraph 3", "<inline-text 4", "cd",
			`<paragraph 2 zt.level=1.6666666666666667 attribute.align="center"`,
			"<hard_break 2",
		), schema)
		para := node("paragraph", hardBreak)
		para["attribute"] = map[string]any{"align": "center"}
		assert.Equal(t, jsonOf(node("content",
			node("bullet_list",
				node("list_item", node("paragraph", text("cd"))),
				node("list_item", para),
			),
		)), got)
	})

	t.Run("the next item of the list stays out of the virtual parents", func(t *testing.T) {
		got := modelOf(tokens(
			"<bullet_list 1", "<list_item 2", "<paragraph 3", "<inline-text 4", "ab",
			"<hard_break 2",
			"<list_item 2", "<paragraph 3", "<inline-text 4", "cd",
		), schema)
		assert.Equal(t, jsonOf(node("content",
			node("bullet_list",
				node("list_item", node("paragraph", text("ab"))),
				node("list_item", node("paragraph", hardBreak)),
				node("list_item", node("paragraph", text("cd"))),
			),
		)), got)
	})
}

// Concurrent splits of one place fold into one (JS `foldReps`). B split
// "a|b" at a later ticket than all of A's edits, not knowing them; A had split
// there too and then typed at the end of "a" (and, in the second case, split
// "a|x" and bolded "x"). What A typed left of its split stays left, and what
// A wrote on its split's node stays on "b".
func TestZeroTreeFoldWithTextBetween(t *testing.T) {
	const a1, a2 = "000000000000000000000001", "000000000000000000000002"
	id := func(lamport int, actor string, offset int) string {
		return fmt.Sprintf("%d:%s:0:%d", lamport, actor, offset)
	}
	char := func(lamport int, actor string, offset int, c string) viewEntry {
		return viewEntry{id: id(lamport, actor, offset), char: c}
	}
	// a style named "~k" is one a range write gave the marker
	marker := func(lamport int, actor string, typ string, depth int, boundary string, style ...string) viewEntry {
		attrs, regs := newOmap[string](), newOmap[zeroRegister]()
		for _, k := range style {
			v := "true"
			byRange := strings.HasPrefix(k, "~")
			k = strings.TrimPrefix(k, "~")
			attrs.set("style."+k, v)
			at := time.NewTicket(int64(lamport), 9, time.InitialActorID)
			regs.set("style."+k, zeroRegister{value: &v, at: at, byRange: byRange})
		}
		return viewEntry{id: id(lamport, actor, 0), marker: &viewMarker{
			typ: typ, depth: depth, attrs: attrs, boundary: boundary, registers: regs,
		}}
	}
	ab := id(1, a1, 2) + "|" + id(1, a1, 3)
	head := []viewEntry{
		{id: id(1, a1, 0), marker: &viewMarker{typ: "paragraph", depth: 1, attrs: newOmap[string]()}},
		{id: id(1, a1, 1), marker: &viewMarker{typ: "inline-text", depth: 2, attrs: newOmap[string]()}},
		char(1, a1, 2, "a"),
	}
	b := char(1, a1, 3, "b")
	styled := func(n map[string]any, keys ...string) map[string]any {
		style := map[string]any{}
		for _, k := range keys {
			style[k] = true
		}
		n["style"] = style
		return n
	}
	bold := func(n map[string]any) map[string]any { return styled(n, "bold") }

	t.Run("a character typed at the end of the left node stays left", func(t *testing.T) {
		entries := append(append([]viewEntry{}, head...),
			marker(20, a2, "inline-text", 2, ab),
			char(5, a1, 0, "x"),
			marker(3, a1, "inline-text", 2, ab, "bold"),
			b,
		)
		assert.Equal(t, jsonOf(node("content", node("paragraph", text("ax"), bold(text("b"))))),
			modelOf(entries, listSchema()))
	})

	t.Run("a range write over the node before a folded split stays on that node", func(t *testing.T) {
		// A, knowing all, italicized "ax": the range walked over B's split
		entries := []viewEntry{
			head[0],
			marker(1, a1, "inline-text", 2, "", "italic"),
			head[2],
			marker(20, a2, "inline-text", 2, ab, "~italic"),
			char(5, a1, 0, "x"),
			marker(3, a1, "inline-text", 2, ab, "bold"),
			b,
		}
		entries[1].id = id(1, a1, 1)
		assert.Equal(t, jsonOf(node("content", node("paragraph", styled(text("ax"), "italic"), bold(text("b"))))),
			modelOf(entries, listSchema()))
	})

	t.Run("what the author of a folded split wrote on it stays after the split", func(t *testing.T) {
		entries := append(append([]viewEntry{}, head...),
			marker(20, a2, "inline-text", 2, ab, "bold"),
			char(5, a1, 0, "x"),
			marker(3, a1, "inline-text", 2, ab),
			b,
		)
		assert.Equal(t, jsonOf(node("content", node("paragraph", text("ax"), bold(text("b"))))),
			modelOf(entries, listSchema()))
	})

	t.Run("a split between the folded ones keeps its style to itself", func(t *testing.T) {
		entries := append(append([]viewEntry{}, head...),
			marker(20, a2, "inline-text", 2, ab),
			marker(6, a1, "inline-text", 2, id(1, a1, 2)+"|"+id(5, a1, 0), "bold"),
			char(5, a1, 0, "x"),
			marker(3, a1, "inline-text", 2, ab),
			b,
		)
		assert.Equal(t, jsonOf(node("content", node("paragraph", text("a"), bold(text("x")), text("b")))),
			modelOf(entries, listSchema()))
	})
}

func TestZeroTreeEmptyObjectData(t *testing.T) {
	// Zero gives a soft break (and other `empty-data` nodes) `data: {}`, and
	// rejects such a node without data. The schema names those types; other
	// atoms with no data (a ProseMirror `hard_break`) stay without it.
	content := map[string]any{"type": "content", "children": []any{
		map[string]any{"type": "paragraph", "children": []any{
			map[string]any{"type": "inline-text", "data": "a"},
			map[string]any{"type": "inline-image", "data": map[string]any{}},
			map[string]any{"type": "inline-text", "data": "b"},
		}},
	}}
	line := func(atom string) string {
		return `{"children":[{"children":[{"data":"a","type":"inline-text"},` + atom +
			`,{"data":"b","type":"inline-text"}],"type":"paragraph"}],"type":"content"}`
	}

	schema := DefaultZeroSchema()
	schema.ObjectData = []string{"inline-image"}
	tree, err := NewZeroTreeFromContent(time.InitialTicket, content, schema)
	assert.NoError(t, err)
	assert.Equal(t, line(`{"data":{},"type":"inline-image"}`), tree.Marshal())

	plain, err := NewZeroTreeFromContent(time.InitialTicket, content, nil)
	assert.NoError(t, err)
	assert.Equal(t, line(`{"type":"inline-image"}`), plain.Marshal())
}
