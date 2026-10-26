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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestZeroStyleRegionEncoding(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)
	last := NewRGATreeSplitNodeID(time.NewTicket(1, 1, actor), 8)
	region := &ZeroStyleRegion{
		Last: last,
		Writes: []ZeroAttrWrite{
			{Key: "style.bold", Value: "true"},
			{Key: "style.italic", Removed: true},
			{Key: "data.src", Value: `"<a&b>"`},
		},
		Ticket: time.NewTicket(9, 2, actor),
	}

	t.Run("encodes as the JS SDK does", func(t *testing.T) {
		// JSON.stringify({l: encodeNodeID(last), w: writes}) in the JS SDK
		assert.Equal(t,
			`{"l":"[\"1\",1,\"000000000000000000000003\",8]",`+
				`"w":[["style.bold","true"],["style.italic",null],["data.src","\"<a&b>\""]]}`,
			region.encode())
		assert.Equal(t, "zt.r.9:000000000000000000000003:2", region.key())
	})

	t.Run("decodes what it encodes", func(t *testing.T) {
		got, err := decodeZeroRegion(region.encode())
		require.NoError(t, err)
		assert.Equal(t, 0, got.Last.createdAt.Compare(last.createdAt))
		assert.Equal(t, last.offset, got.Last.offset)
		assert.Equal(t, region.Writes, got.Writes)
	})

	t.Run("decodes a range to the end of the document", func(t *testing.T) {
		got, err := decodeZeroRegion(`{"l":"","w":[["style.bold","true"]]}`)
		require.NoError(t, err)
		assert.Nil(t, got.Last)
	})
}

func TestZeroStyleCancels(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	at := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	value := func(m *ZeroMarker, key string) string {
		for _, attr := range m.Attrs {
			if attr.Key == key {
				if attr.Removed {
					return "<removed>"
				}
				return attr.Value
			}
		}
		return "<none>"
	}

	t.Run("an undo takes back its write and only it", func(t *testing.T) {
		cancels := &ZeroStyleCancels{}
		m := &ZeroMarker{Type: "inline-text"}
		m.Set("style.color", "red", false, at(1), cancels)
		m.Set("style.color", "blue", false, at(2), cancels)
		m.Set("style.color", "green", false, at(3), cancels)
		assert.Equal(t, "green", value(m, "style.color"))

		// cancelling blue: green stays, it is newer
		cancels.set(&ZeroStyleCancel{Write: at(2), On: true, At: at(4)})
		m.repick(cancels)
		assert.Equal(t, "green", value(m, "style.color"))

		// cancelling green too: back to red
		cancels.set(&ZeroStyleCancel{Write: at(3), On: true, At: at(5)})
		m.repick(cancels)
		assert.Equal(t, "red", value(m, "style.color"))

		// a redo of blue brings it back
		cancels.set(&ZeroStyleCancel{Write: at(2), On: false, At: at(6)})
		m.repick(cancels)
		assert.Equal(t, "blue", value(m, "style.color"))

		// an older undo of blue does not win over the redo
		assert.False(t, cancels.set(&ZeroStyleCancel{Write: at(2), On: true, At: at(5)}))
	})

	t.Run("a register keeps the newest writes and falls back past them", func(t *testing.T) {
		cancels := &ZeroStyleCancels{}
		m := &ZeroMarker{Type: "inline-text"}
		for i, c := range []string{"red", "blue", "green", "yellow", "white"} {
			m.Set("style.color", c, false, at(int64(i+1)), cancels)
		}
		require.Len(t, m.Attrs[0].History, ZeroHistoryDepth)
		assert.Equal(t, 0, m.Attrs[0].History[0].At.Compare(at(5)))
		for i := int64(2); i <= 5; i++ {
			cancels.set(&ZeroStyleCancel{
				Write: at(i), On: true, At: at(10 + i),
				Fallback: []ZeroAttrWrite{{Key: "style.color", Value: "black"}},
			})
		}
		m.repick(cancels)
		// every kept write is cancelled: what the oldest one's marker held,
		// at the initial ticket so any value it meets in a merge wins
		assert.Equal(t, "black", value(m, "style.color"))
		assert.Equal(t, 0, m.Attrs[0].UpdatedAt.Compare(time.InitialTicket))
	})

	t.Run("reserved keys stay plain registers", func(t *testing.T) {
		cancels := &ZeroStyleCancels{}
		m := &ZeroMarker{Type: "cell"}
		m.Set("zt.col", `"a"`, false, at(1), cancels)
		m.Set("zt.col", `"b"`, false, at(2), cancels)
		assert.Nil(t, m.Attrs[0].History)
		assert.Equal(t, `"b"`, value(m, "zt.col"))
	})
}

// Two people format abutting ranges at once: A bolds [0,1) of "abc", B
// underlines [1,3). Both split at 1, and B's split marker sits right after
// A's range in the token order: it opens what follows the range, so it must
// not get A's bold, whichever change arrives first (the JS SDK's
// `ZeroStyleRegion` follows the same rule).
func TestZeroStyleRangeAbutting(t *testing.T) {
	a, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	b, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)
	created := time.NewTicket(1, 0, a)
	id := func(at *time.Ticket, offset int) *RGATreeSplitNodeID { return NewRGATreeSplitNodeID(at, offset) }
	marker := func(typ string, depth int) ZeroToken {
		return ZeroToken{Marker: &ZeroMarker{Type: typ, Depth: depth, Boundary: "a|b"}}
	}
	known := func() time.VersionVector {
		vv := time.NewVersionVector()
		vv.Set(a, 1)
		return vv
	}

	newTree := func(t *testing.T) *ZeroTree {
		tree := NewZeroTree(NewRGATreeSplit(InitialZeroTreeNode()), created)
		head := NewRGATreeSplitNodePos(initialNodeID, 0)
		content := NewZeroTokens([]ZeroToken{
			{Marker: &ZeroMarker{Type: "paragraph", Depth: 1}},
			{Marker: &ZeroMarker{Type: "inline-text", Depth: 2}},
			{Char: 'a'}, {Char: 'b'}, {Char: 'c'},
		}, "")
		_, _, _, err := tree.Edit(head, head, content, created, nil)
		require.NoError(t, err)
		return tree
	}
	// both split right after "a" (token 2 of the first insertion)
	afterA := NewRGATreeSplitNodePos(id(created, 0), 3)
	splitA, styleA := time.NewTicket(2, 0, a), time.NewTicket(2, 1, a)
	splitB, styleB := time.NewTicket(3, 0, b), time.NewTicket(3, 1, b)
	applyA := func(t *testing.T, tree *ZeroTree) {
		_, _, _, err := tree.Edit(afterA, afterA, NewZeroTokens([]ZeroToken{marker("inline-text", 2)}, ""), splitA, known())
		require.NoError(t, err)
		tree.SetAttrs(id(created, 1), []ZeroAttrWrite{{Key: "style.bold", Value: "true"}}, styleA,
			&ZeroStyleRange{Last: id(created, 2)}, nil)
	}
	applyB := func(t *testing.T, tree *ZeroTree) {
		_, _, _, err := tree.Edit(afterA, afterA, NewZeroTokens([]ZeroToken{marker("inline-text", 2)}, ""), splitB, known())
		require.NoError(t, err)
		tree.SetAttrs(id(splitB, 0), []ZeroAttrWrite{{Key: "style.underline", Value: "true"}}, styleB,
			&ZeroStyleRange{Last: id(created, 4)}, nil)
	}

	want := `{"children":[{"children":[` +
		`{"data":"a","style":{"bold":true},"type":"inline-text"},` +
		`{"data":"bc","style":{"underline":true},"type":"inline-text"}],"type":"paragraph"}],"type":"content"}`
	t.Run("A first", func(t *testing.T) {
		tree := newTree(t)
		applyA(t, tree)
		applyB(t, tree)
		assert.Equal(t, want, tree.ContentJSON())
	})
	t.Run("B first", func(t *testing.T) {
		tree := newTree(t)
		applyB(t, tree)
		applyA(t, tree)
		assert.Equal(t, want, tree.ContentJSON())
	})
}
