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
