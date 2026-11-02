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

// buildDocWithTombstones edits an Object, an Array, a Text and a Tree, and
// removes part of each, so the document carries live and tombstoned elements
// and nodes.
func buildDocWithTombstones(t *testing.T) *document.Document {
	doc := document.New("shared-ticket-race")
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		obj := r.SetNewObject("obj")
		obj.SetString("a", "1")
		obj.SetString("b", "2")
		obj.SetNewObject("nested").SetInteger("n", 1)

		arr := r.SetNewArray("arr")
		arr.AddInteger(1, 2, 3, 4, 5)
		arr.AddNewObject().SetString("k", "v")

		text := r.SetNewText("text")
		text.Edit(0, 0, "hello world")
		text.Edit(5, 5, " yorkie", map[string]string{"b": "1"})

		r.SetNewTree("tree", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcdef"}},
			}},
		})
		r.SetNewCounter("cnt", 0)
		return nil
	}))
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetObject("obj").Delete("a")
		r.GetObject("obj").Delete("nested")
		r.GetArray("arr").Delete(1)
		r.GetArray("arr").Delete(4)
		r.GetText("text").Edit(1, 4, "")
		r.GetText("text").Style(0, 3, map[string]string{"i": "1"})
		r.GetTree("tree").Edit(2, 4, nil, 0)
		r.GetCounter("cnt").Increase(3)
		return nil
	}))
	require.NotZero(t, doc.GarbageLen())
	return doc
}

// roundTripChanges re-decodes the local changes of src through the protobuf
// converter, as the server does when it reads changes from the database, so
// every ticket in them is a fresh instance whose caches are cold.
func roundTripChanges(t *testing.T, src *document.Document) []*change.Change {
	pbPack, err := converter.ToChangePack(src.CreateChangePack())
	require.NoError(t, err)
	pack, err := converter.FromChangePack(pbPack)
	require.NoError(t, err)
	return pack.Changes
}

// fromChanges builds an InternalDocument by applying the changes of src
// decoded through the converter, as the server does when it rebuilds a
// document from the changes it stored.
func fromChanges(t *testing.T, src *document.Document) *document.InternalDocument {
	doc := document.NewInternalDocument(src.Key())
	_, _, err := doc.ApplyChanges(roundTripChanges(t, src)...)
	require.NoError(t, err)
	return doc
}

// fromSnapshot builds an InternalDocument from the snapshot of src, as the
// server does when it loads a stored snapshot.
func fromSnapshot(t *testing.T, src *document.Document) *document.InternalDocument {
	snapshot, err := converter.SnapshotToBytes(src.RootObject(), nil)
	require.NoError(t, err)
	doc, err := document.NewInternalDocumentFromSnapshot(
		src.Key(), 0, src.InternalDocumentForTest().Lamport(),
		time.NewVersionVector(), snapshot,
	)
	require.NoError(t, err)
	return doc
}

// TestSharedTicketConcurrentUse checks that using a document never writes
// to tickets or node IDs that other goroutines can reach. `go test -race`
// reports a DATA RACE here if an accessor fills a cache without
// synchronization.
func TestSharedTicketConcurrentUse(t *testing.T) {
	const workers = 8

	sources := []struct {
		name  string
		build func(*testing.T, *document.Document) *document.InternalDocument
	}{
		{"changes", fromChanges},
		{"snapshot", fromSnapshot},
	}

	for _, src := range sources {
		t.Run("deep copies of a cached document built from "+src.name+" test", func(t *testing.T) {
			// A regression guard: this passes on the lazy plain-write cache
			// too, because building the document keys every element.
			//
			// The server keeps one InternalDocument per document in its
			// snapshot cache and hands each request a DeepCopy. Copies share
			// tickets with the cached document, so requests read them
			// concurrently.
			cached := src.build(t, buildDocWithTombstones(t))
			expected := cached.Marshal()

			var wg sync.WaitGroup
			for range workers {
				wg.Go(func() {
					clone, err := cached.DeepCopy()
					assert.NoError(t, err)
					assert.Equal(t, expected, clone.Marshal())
					assert.NotZero(t, clone.GarbageLen())
					_, err = converter.SnapshotToBytes(clone.RootObject(), nil)
					assert.NoError(t, err)
				})
			}
			wg.Wait()
		})

		t.Run("concurrent GarbageLen of a document built from "+src.name+" test", func(t *testing.T) {
			// A regression guard, like the one above. GarbageLen takes only
			// the document's read lock, so several goroutines may walk the
			// same tombstones at once.
			doc := src.build(t, buildDocWithTombstones(t)).ToDocument()

			var wg sync.WaitGroup
			for range workers {
				wg.Go(func() {
					assert.NotZero(t, doc.GarbageLen())
				})
			}
			wg.Wait()
		})

		t.Run("clones of a cached document applying changes built from "+src.name+" test", func(t *testing.T) {
			// BuildInternalDocForServerSeq applies the changes stored after
			// the cached document onto its own DeepCopy. Removing a live
			// node keys it, and the node and its ticket are shared with the
			// cache and with every other request's copy.
			doc := buildDocWithTombstones(t)
			cached := src.build(t, doc)
			applied := len(doc.CreateChangePack().Changes)

			require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
				r.GetObject("obj").Delete("b")
				r.GetArray("arr").Delete(0)
				r.GetText("text").Edit(0, 6, "")
				r.GetTree("tree").Edit(1, 3, nil, 0)
				return nil
			}))
			pbPack, err := converter.ToChangePack(doc.CreateChangePack())
			require.NoError(t, err)
			expected := doc.Marshal()

			var wg sync.WaitGroup
			for range workers {
				wg.Go(func() {
					clone, err := cached.DeepCopy()
					assert.NoError(t, err)
					pack, err := converter.FromChangePack(pbPack)
					assert.NoError(t, err)
					_, _, err = clone.ApplyChanges(pack.Changes[applied:]...)
					assert.NoError(t, err)
					assert.Equal(t, expected, clone.Marshal())
				})
			}
			wg.Wait()
		})
	}
}
