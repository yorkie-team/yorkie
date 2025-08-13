//go:build integration

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestText(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 0, "ABCD")
			return nil
		}, "edit 0,0 ABCD by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 0, "1234")
			return nil
		}, "edit 0,0 1234 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(2, 3, "XX")
			return nil
		}, "edit 2,3 XX by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(2, 3, "YY")
			return nil
		}, "edit 2,3 YY by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(4, 5, "ZZ")
			return nil
		}, "edit 4,5 ZZ by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(2, 3, "TT")
			return nil
		}, "edit 2,3 TT by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent insertion and deletion test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "AB")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"AB"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 2, "")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(1, 1, "C")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"A"},{"val":"C"},{"val":"B"}]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"val":"C"}]}`, d1.Marshal())
	})

	t.Run("rich text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "Hello world", nil)
			return nil
		}, `set a new text with "Hello world" by c1`)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Style(0, 1, map[string]string{"b": "1"})
			return nil
		}, `set style b to "H" by c1`)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Style(0, 5, map[string]string{"i": "1"})
			return nil
		}, `set style i to "Hello" by c2`)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent block deletions test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 0, "123")
			root.GetText("k1").Edit(3, 3, "456")
			root.GetText("k1").Edit(6, 6, "789")
			return nil
		}, "set new text by c1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"val":"123"},{"val":"456"},{"val":"789"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(1, 7, "")
			return nil
		}, "delete block by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"1"},{"val":"89"}]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(2, 5, "")
			return nil
		}, "delete block by c2")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"12"},{"val":"6"},{"val":"789"}]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("new creation then concurrent deletion test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 0, "0")
			root.GetText("k1").Edit(1, 1, "0")
			root.GetText("k1").Edit(2, 2, "0")
			return nil
		}, "set new text by c1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"val":"0"},{"val":"0"},{"val":"0"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(1, 2, "1")
			root.GetText("k1").Edit(1, 2, "1")
			root.GetText("k1").Edit(1, 2, "")
			return nil
		}, "newly create then delete by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"0"},{"val":"0"}]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 3, "")
			return nil
		}, "delete the range includes above new nodes")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.True(t, d1.Root().GetText("k1").CheckWeight())
		assert.True(t, d2.Root().GetText("k1").CheckWeight())
		assert.True(t, d1.Root().GetText("k1").TreeByIndex().CheckWeight())
		assert.True(t, d2.Root().GetText("k1").TreeByIndex().CheckWeight())
	})

	// Peritext test
	t.Run("ex2. concurrent formatting and insertion test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "The fox jumped.", nil)
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"The fox jumped."}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Style(0, 15, map[string]string{"b": "1"})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"attrs":{"b":"1"},"val":"The fox jumped."}]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(4, 4, "brown ")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"The "},{"val":"brown "},{"val":"fox jumped."}]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"attrs":{"b":"1"},"val":"The "},{"val":"brown "},{"attrs":{"b":"1"},"val":"fox jumped."}]}`, d1.Marshal())

		// TODO(MoonGyu1): d1 and d2 should have the result below after applying mark operation
		// assert.Equal(t, `{"k1":[{"attrs":{"b":"1"},"val":"The "},{"attrs":{"b":"1"},"val":"brown "},{"attrs":{"b":"1"},"val":"fox jumped."}]}`, d1.Marshal())
	})

	t.Run("causal deletion preserves original timestamps", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "abcd")
			return nil
		}, "insert abcd")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(1, 3, "")
			return nil
		}, "delete bc")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 2, "")
			return nil
		}, "delete ad")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		textNode1 := d1.Root().GetText("k1")
		textNode2 := d2.Root().GetText("k1")
		nodes1 := textNode1.Nodes()
		nodes2 := textNode2.Nodes()

		assert.Equal(t, len(nodes1), len(nodes2), "Both documents should have same number of nodes")

		for i, node1 := range nodes1 {
			node2 := nodes2[i]
			assert.Equal(t, node1.Value().String(), node2.Value().String(), "Nodes should have same value")
			assert.True(t, node1.RemovedAt().Compare(node2.RemovedAt()) == 0, "Synced nodes should have same timestamp")
		}

		var bcNode, aNode, dNode *crdt.RGATreeSplitNode[*crdt.TextValue]
		for _, node := range nodes1 {
			switch node.Value().String() {
			case "bc":
				bcNode = node
			case "a":
				aNode = node
			case "d":
				dNode = node
			}
		}

		assert.NotNil(t, bcNode.RemovedAt())
		assert.NotNil(t, aNode.RemovedAt())
		assert.NotNil(t, dNode.RemovedAt())

		// These assertions FAIL - showing the bug exists
		assert.True(t, aNode.RemovedAt().After(bcNode.RemovedAt()),
			"In causal deletion, 'a' should be deleted after 'bc' (T3 > T2)")
		assert.True(t, dNode.RemovedAt().After(bcNode.RemovedAt()),
			"In causal deletion, 'd' should be deleted after 'bc' (T3 > T2)")

		assert.Equal(t, `{"k1":[]}`, d1.Marshal())
	})

	t.Run("concurrent deletion test for LWW behavior", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "abcd")
			return nil
		}, "insert abcd")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(1, 3, "")
			return nil
		}, "delete bc by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 4, "")
			return nil
		}, "delete abcd by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		textNode1 := d1.Root().GetText("k1")
		textNode2 := d2.Root().GetText("k1")

		nodes1 := textNode1.Nodes()
		nodes2 := textNode2.Nodes()

		assert.Equal(t, len(nodes1), len(nodes2), "Both documents should have same number of nodes")

		timestampSet := make(map[string]bool)
		for _, node := range append(nodes1, nodes2...) {
			assert.NotNil(t, node.RemovedAt(), "All nodes should be deleted")
			timestampSet[node.RemovedAt().ToTestString()] = true
		}
		assert.Equal(t, 1, len(timestampSet), "All nodes in both documents should have same timestamp")

		assert.Equal(t, `{"k1":[]}`, d1.Marshal())
	})
}
