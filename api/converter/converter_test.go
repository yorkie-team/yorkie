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

package converter_test

import (
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestConverter(t *testing.T) {
	t.Run("snapshot simple test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "A")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"A"}]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "B")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, doc.Marshal())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, obj.Marshal())
	})

	t.Run("root snapshot test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetNull("k1.0").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddNull().
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘").
				Edit(2, 2, "구름").
				Edit(2, 3, "뭉게구")

			// rich text
			root.SetNewText("k4").
				Edit(0, 0, "Hello world", nil).
				Edit(6, 11, "sky", nil).
				Style(0, 5, map[string]string{"b": "1"})

			// a counter
			root.SetNewCounter("k5", int64(0)).
				Increase(10).
				Increase(math.MaxInt64)

			// tree
			root.SetNewTree("k5").
				Edit(0, 0, &json.TreeNode{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "Hello world",
					}},
				}, 0)

			return nil
		})
		assert.NoError(t, err)

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, doc.Marshal(), obj.Marshal())
	})

	t.Run("change pack test", func(t *testing.T) {
		d1 := document.New("d1")

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			nextCreatedAt := root.GetArray("k2").Get(0).CreatedAt()
			targetCreatedAt := root.GetArray("k2").Get(1).CreatedAt()
			root.GetArray("k2").MoveBefore(nextCreatedAt, targetCreatedAt)

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")

			// rich text
			root.SetNewText("k3").
				Edit(0, 0, "Hello World", nil).
				Style(0, 5, map[string]string{"b": "1"})

			// counter
			root.SetNewCounter("k4", 0).Increase(5)

			return nil
		})
		assert.NoError(t, err)

		pbPack, err := converter.ToChangePack(d1.CreateChangePack())
		assert.NoError(t, err)

		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)

		d2 := document.New("d1")
		err = d2.ApplyChangePack(pack)
		assert.NoError(t, err)

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("change pack error test", func(t *testing.T) {
		_, err := converter.FromChangePack(nil)
		assert.ErrorIs(t, err, converter.ErrPackRequired)

		_, err = converter.FromChangePack(&api.ChangePack{})
		assert.ErrorIs(t, err, converter.ErrCheckpointRequired)
	})

	t.Run("tree converting test", func(t *testing.T) {
		root := helper.BuildTreeNode(&json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})

		pbNodes := converter.ToTreeNodes(root)
		clone, err := converter.FromTreeNodes(pbNodes)
		assert.NoError(t, err)
		assert.Equal(t, crdt.ToTreeNodeForTest(root), crdt.ToTreeNodeForTest(clone))
		assert.Equal(t, crdt.ToXML(root), crdt.ToXML(clone))
	})

	t.Run("tree converting to bytes test", func(t *testing.T) {
		root := helper.BuildTreeNode(&json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})

		tree := crdt.NewTree(root, time.InitialTicket)
		bytes, err := converter.TreeToBytes(tree)
		assert.NoError(t, err)
		clone, err := converter.BytesToTree(bytes)
		assert.NoError(t, err)

		assert.Equal(t, tree.ToXML(), clone.ToXML())
	})

	t.Run("array converting to bytes test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		treeList := crdt.NewRGATreeList()
		arr := crdt.NewArray(treeList, ctx.IssueTimeTicket())
		primitive, _ := crdt.NewPrimitive("1", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)
		primitive, _ = crdt.NewPrimitive("2", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)
		primitive, _ = crdt.NewPrimitive("3", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)

		bytes, err := converter.ArrayToBytes(arr)
		assert.NoError(t, err)
		clone, err := converter.BytesToArray(bytes)
		assert.NoError(t, err)

		assert.Equal(t, `["1","2","3"]`, arr.Marshal())
		assert.Equal(t, `["1","2","3"]`, clone.Marshal())
	})

	t.Run("empty presence converting test", func(t *testing.T) {
		change := &presence.Change{
			ChangeType: presence.Put,
			Presence:   presence.NewData(),
		}

		pbChange := converter.ToPresenceChange(change)
		clone := converter.FromPresenceChange(pbChange)
		assert.Equal(t, change, clone)
	})

	t.Run("properly encode and decode tree test", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "12"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "34"}}},
				},
			})
			assert.Equal(t, "<r><p>12</p><p>34</p></r>", root.GetTree("t").ToXML())
			root.GetTree("t").EditByPath([]int{0, 1}, []int{1, 1}, nil, 0)

			root.GetTree("t").Style(0, 1, map[string]string{"b": "t", "i": "t"})
			assert.Equal(t, `<r><p b="t" i="t">14</p></r>`, root.GetTree("t").ToXML())

			root.GetTree("t").RemoveStyle(0, 1, []string{"i"})
			return nil
		}))
		assert.Equal(t, `<r><p b="t">14</p></r>`, doc.Root().GetTree("t").ToXML())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)

		assert.Equal(t, obj.Get("t").(*crdt.Tree).NodeLen(), doc.Root().GetTree("t").NodeLen())
		assert.Equal(t, obj.Get("t").(*crdt.Tree).Root().Len(), doc.Root().GetTree("t").Len())
		assert.Equal(t, obj.Get("t").(*crdt.Tree).ToXML(), doc.Root().GetTree("t").ToXML())
	})

	// Regression test for the snapshot-roundtrip convergence bug
	// previously fixed by persisting TreeNode.MergedFrom and
	// reconstructing mergedInto / mergedChildIDs / mergedAt at load
	// time. Mirrors "contained-merge-and-insert":
	//   - docA runs a merge, then serializes via SnapshotToBytes.
	//   - docB loads the snapshot and reconstructs merge state.
	//   - docC, unaware of the merge, inserts 'c' inside original p[b].
	//   - Both docA and docB must converge to <p>acb</p>.
	t.Run("tree merge-and-insert convergence across snapshot", func(t *testing.T) {
		actorA, err := time.ActorIDFromHex("000000000000000000000001")
		assert.NoError(t, err)
		actorB, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)
		actorC, err := time.ActorIDFromHex("000000000000000000000003")
		assert.NoError(t, err)

		// 01. docA creates the tree with two paragraphs.
		docA := document.New(helper.TestKey(t))
		docA.SetActor(actorA)
		assert.NoError(t, docA.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "a"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "b"}},
				}},
			})
			return nil
		}))
		assert.Equal(t, "<root><p>a</p><p>b</p></root>", docA.Root().GetTree("t").ToXML())

		// 02. docC starts from the pre-merge state of docA.
		docC := document.New(helper.TestKey(t))
		docC.SetActor(actorC)
		initialPack := docA.CreateChangePack()
		initialPack.VersionVector.Set(docC.ActorID(), docC.VersionVector().VersionOf(docC.ActorID()))
		assert.NoError(t, docC.ApplyChangePack(initialPack))
		assert.Equal(t, "<root><p>a</p><p>b</p></root>", docC.Root().GetTree("t").ToXML())

		// 03. docA merges p[a] and p[b] -> <p>ab</p>.
		assert.NoError(t, docA.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>ab</p></root>", docA.Root().GetTree("t").ToXML())

		// 04. docC (unaware of docA's merge) inserts 'c' at position 4
		//     inside the original p[b]. The CRDT coordinates of this
		//     insert point at a node that docA has tombstoned.
		assert.NoError(t, docC.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 4, &json.TreeNode{Type: "text", Value: "c"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>a</p><p>cb</p></root>", docC.Root().GetTree("t").ToXML())

		// 05. Serialize docA's post-merge state via SnapshotToBytes -
		//     this is exactly what the server stores as a snapshot.
		snapshot, err := converter.SnapshotToBytes(docA.RootObject(), docA.AllPresences(), nil)
		assert.NoError(t, err)

		// 06. docB loads the snapshot. mergedInto/mergedChildIDs/mergedAt
		//     are reconstructed from the persisted MergedFrom field in
		//     crdt.NewTree's rebuildMergeState pass.
		docB := document.New(helper.TestKey(t))
		docB.SetActor(actorB)
		snapshotPack := change.NewPack(
			helper.TestKey(t),
			change.InitialCheckpoint,
			nil,
			docA.VersionVector().DeepCopy(),
			snapshot,
		)
		assert.NoError(t, docB.ApplyChangePack(snapshotPack))
		assert.Equal(t, "<root><p>ab</p></root>", docB.Root().GetTree("t").ToXML())

		// 07. Apply docC's concurrent insert to both docA and docB.
		//     Create fresh packs so each carries a VV scoped to its
		//     target's actor clock (mimicking a server relay).
		packForA := docC.CreateChangePack()
		packForA.VersionVector.Set(docA.ActorID(), docA.VersionVector().VersionOf(docA.ActorID()))
		assert.NoError(t, docA.ApplyChangePack(packForA))

		packForB := docC.CreateChangePack()
		packForB.VersionVector.Set(docB.ActorID(), docB.VersionVector().VersionOf(docB.ActorID()))
		assert.NoError(t, docB.ApplyChangePack(packForB))

		assert.Equal(t,
			docA.Root().GetTree("t").ToXML(),
			docB.Root().GetTree("t").ToXML(),
			"docB (loaded from snapshot) must converge to the same state as docA",
		)
		assert.Equal(t, "<root><p>acb</p></root>", docA.Root().GetTree("t").ToXML())
	})

	// Sibling regression test covering the merge-merge overlap path
	// (Fix 4 — mergedInto forwarding) across a snapshot roundtrip.
	t.Run("tree merge-and-merge convergence across snapshot", func(t *testing.T) {
		actorA, err := time.ActorIDFromHex("000000000000000000000011")
		assert.NoError(t, err)
		actorB, err := time.ActorIDFromHex("000000000000000000000012")
		assert.NoError(t, err)
		actorC, err := time.ActorIDFromHex("000000000000000000000013")
		assert.NoError(t, err)

		// 01. Three paragraphs.
		docA := document.New(helper.TestKey(t))
		docA.SetActor(actorA)
		assert.NoError(t, docA.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "a"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "b"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "c"}},
				}},
			})
			return nil
		}))

		// 02. docC starts from pre-merge state.
		docC := document.New(helper.TestKey(t))
		docC.SetActor(actorC)
		initialPack := docA.CreateChangePack()
		initialPack.VersionVector.Set(docC.ActorID(), docC.VersionVector().VersionOf(docC.ActorID()))
		assert.NoError(t, docC.ApplyChangePack(initialPack))

		// 03. docA merges p[a]+p[b]; docC (concurrently) merges p[b]+p[c].
		assert.NoError(t, docA.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>ab</p><p>c</p></root>", docA.Root().GetTree("t").ToXML())
		assert.NoError(t, docC.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(5, 7, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>a</p><p>bc</p></root>", docC.Root().GetTree("t").ToXML())

		// 04. Snapshot docA, load into docB.
		snapshot, err := converter.SnapshotToBytes(docA.RootObject(), docA.AllPresences(), nil)
		assert.NoError(t, err)
		docB := document.New(helper.TestKey(t))
		docB.SetActor(actorB)
		snapshotPack := change.NewPack(
			helper.TestKey(t),
			change.InitialCheckpoint,
			nil,
			docA.VersionVector().DeepCopy(),
			snapshot,
		)
		assert.NoError(t, docB.ApplyChangePack(snapshotPack))
		assert.Equal(t, "<root><p>ab</p><p>c</p></root>", docB.Root().GetTree("t").ToXML())

		// 05. Apply docC's concurrent merge to both docA and docB.
		packForA := docC.CreateChangePack()
		packForA.VersionVector.Set(docA.ActorID(), docA.VersionVector().VersionOf(docA.ActorID()))
		assert.NoError(t, docA.ApplyChangePack(packForA))

		packForB := docC.CreateChangePack()
		packForB.VersionVector.Set(docB.ActorID(), docB.VersionVector().VersionOf(docB.ActorID()))
		assert.NoError(t, docB.ApplyChangePack(packForB))

		assert.Equal(t,
			docA.Root().GetTree("t").ToXML(),
			docB.Root().GetTree("t").ToXML(),
			"docB (loaded from snapshot) must converge to the same state as docA",
		)
		assert.Equal(t, "<root><p>abc</p></root>", docA.Root().GetTree("t").ToXML())
	})

	t.Run("object converting to bytes with gc elements test", func(t *testing.T) {
		containsGCElement := func(gcPairMap map[string]crdt.ElementPair, value string) bool {
			for _, pair := range gcPairMap {
				if elem, ok := pair.Elem().(*crdt.Primitive); ok {
					if elem.Value() == value {
						return true
					}
				}
			}
			return false
		}

		// Set initial value "a"
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("o").SetString("1", "a")
			return nil
		}))
		assert.Equal(t, `{"o":{"1":"a"}}`, doc.Root().Marshal())

		// Update value to "b", which should mark "a" as GC element
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("o").SetString("1", "b")
			return nil
		}))
		assert.Equal(t, `{"o":{"1":"b"}}`, doc.Root().Marshal())
		assert.True(t, containsGCElement(doc.Root().GCElementPairMap(), "a"),
			"old value 'a' should exist in GC elements")

		// Serialize and deserialize
		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)

		// Verify that GC elements are preserved after deserialization
		newRoot := crdt.NewRoot(obj)
		assert.True(t, containsGCElement(newRoot.GCElementPairMap(), "a"),
			"old value 'a' should be preserved in GC elements after deserialization")
	})
}
