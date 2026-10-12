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

package document_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// The tests below cover a local change that is still buffered when a remote
// change is applied: the client pulls before it pushes, for example when
// the user edits while a PushPull is in flight. The buffered change must go
// out with the version vector its author had when it was made. Replicas use
// the vector to decide which nodes the author knew about, so an inflated one
// turns a concurrent insert into one the author deleted or styled.

// newTextReplicas returns two replicas that both hold "abcdefghij" under "t".
func newTextReplicas(t *testing.T) (*document.Document, *document.Document, time.ActorID) {
	t.Helper()
	d1, d2, _, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	feed(t, d2, grab(t, d1))
	return d1, d2, a2
}

// lastBufferedVV returns the version vector of d's last buffered change.
func lastBufferedVV(t *testing.T, d *document.Document) time.VersionVector {
	t.Helper()
	changes := d.CreateChangePack().Changes
	require.NotEmpty(t, changes)
	return changes[len(changes)-1].ID().VersionVector()
}

func TestBufferedChangeKeepsItsVersionVector(t *testing.T) {
	t.Run("remote change applied before the push", func(t *testing.T) {
		d1, d2, a2 := newTextReplicas(t)
		editText(t, d1, 2, 8, "")
		before := lastBufferedVV(t, d1).DeepCopy()

		editText(t, d2, 5, 5, "X")
		feed(t, d1, grab(t, d2))

		after := lastBufferedVV(t, d1)
		assert.Equal(t, before, after)
		assert.Equal(t, int64(0), after.VersionOf(a2))
	})

	t.Run("undo change", func(t *testing.T) {
		d1, d2, _ := newTextReplicas(t)
		editText(t, d1, 2, 8, "")
		feed(t, d2, grab(t, d1))
		require.NoError(t, d1.Undo())
		before := lastBufferedVV(t, d1).DeepCopy()

		editText(t, d2, 1, 1, "X")
		feed(t, d1, grab(t, d2))
		assert.Equal(t, before, lastBufferedVV(t, d1))
	})

	t.Run("edit followed by a presence-only update", func(t *testing.T) {
		// A presence-only Context.NextID reuses the previous ID's vector, so
		// the document must not hold the edit's vector when it does.
		d1, d2, _ := newTextReplicas(t)
		editText(t, d1, 2, 8, "")
		before := lastBufferedVV(t, d1).DeepCopy()
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("k", "v")
			return nil
		}))

		editText(t, d2, 1, 1, "X")
		feed(t, d1, grab(t, d2))
		assert.Equal(t, before, d1.CreateChangePack().Changes[0].ID().VersionVector())
	})

	t.Run("snapshot applied before the push", func(t *testing.T) {
		d1, d2, a2 := newTextReplicas(t)
		editText(t, d1, 2, 8, "")
		before := lastBufferedVV(t, d1).DeepCopy()

		editText(t, d2, 1, 1, "X")
		grab(t, d2)
		snapshot, err := converter.SnapshotToBytes(d2.RootObject(), nil)
		require.NoError(t, err)
		require.NoError(t, d1.ApplyChangePack(change.NewPack(
			d1.Key(), change.NewCheckpoint(10, 0), nil, d2.VersionVector(), snapshot,
		)))

		after := lastBufferedVV(t, d1)
		assert.Equal(t, before, after)
		assert.Equal(t, int64(0), after.VersionOf(a2))
	})
}

func TestBufferedChangeConvergesAfterPullBeforePush(t *testing.T) {
	t.Run("text delete", func(t *testing.T) {
		d1, d2, _ := newTextReplicas(t)
		editText(t, d1, 2, 8, "")
		editText(t, d2, 5, 5, "X")

		feed(t, d1, grab(t, d2))
		feed(t, d2, grab(t, d1))

		assert.Equal(t, "abXij", d1.Root().GetText("t").String())
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("text style", func(t *testing.T) {
		d1, d2, _ := newTextReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(2, 8, map[string]string{"b": "1"})
			return nil
		}))
		editText(t, d2, 5, 5, "X")

		feed(t, d1, grab(t, d2))
		feed(t, d2, grab(t, d1))

		assert.Equal(t, nodeAttrs(t, d1, "t"), nodeAttrs(t, d2, "t"))
	})

	t.Run("tree delete", func(t *testing.T) {
		d1, d2, _, _ := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}},
				}},
			})
			return nil
		}))
		feed(t, d2, grab(t, d1))

		editTree(t, d1, 3, 9, nil)
		editTree(t, d2, 6, 6, textNode("X"))

		feed(t, d1, grab(t, d2))
		feed(t, d2, grab(t, d1))

		assert.Equal(t, "<doc><p>abXij</p></doc>", treeXML(t, d1))
		assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
	})
}

// TestChangePackSerializedDuringRemoteApply serializes packs outside the
// document lock, as the sync goroutine does, while remote packs are applied.
// The pack shares each buffered change's version vector, so this fails under
// -race if anything writes that vector after the change is built.
func TestChangePackSerializedDuringRemoteApply(t *testing.T) {
	d1, d2, _ := newTextReplicas(t)
	editText(t, d1, 2, 3, "")

	var remote [][]*change.Change
	for range 200 {
		editText(t, d2, 0, 0, "x")
		remote = append(remote, grab(t, d2))
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			_, err := converter.ToChangePack(d1.CreateChangePack())
			assert.NoError(t, err)
		}
	})
	wg.Go(func() {
		for _, changes := range remote {
			assert.NoError(t, d1.ApplyChangePack(change.NewPack(
				d1.Key(), change.NewCheckpoint(0, 0), changes, time.InitialVersionVector, nil,
			)))
		}
	})
	wg.Wait()
}
