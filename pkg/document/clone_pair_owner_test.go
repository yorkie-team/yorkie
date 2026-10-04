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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestCloneRemovedPairRecordsCRDTOwner pins what the json proxies record as
// the parent of a removed pair: the CRDT container, never the proxy wrapping
// it.
//
// Root.UnregisterRemovedElementPair matches the recorded parent by identity,
// and its one caller (operations.Set.Execute) resolves the container through
// Root.FindByCreatedAt, which answers with the *crdt.Object or *crdt.Array.
// A proxy is a fresh value per accessor call, so an entry registered under
// one matches no owner the retire can present, and the retire silently does
// nothing -- on the clone root, where the json proxies run, which is the only
// root they register against.
func TestCloneRemovedPairRecordsCRDTOwner(t *testing.T) {
	t.Run("object delete", func(t *testing.T) {
		d := document.New("pair-owner-object-delete")
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewObject("o").SetInteger("k", 1)
			return nil
		}))
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.Delete("o")
			return nil
		}, "remove o"))

		pairs := d.Root().GCElementPairMap()
		require.Len(t, pairs, 1)
		for _, pair := range pairs {
			assert.Same(t, d.Root().Object, pair.Parent(),
				"the pair names a proxy no owner lookup can produce")
		}
	})

	t.Run("object overwrite", func(t *testing.T) {
		d := document.New("pair-owner-object-overwrite")
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewObject("o").SetInteger("k", 1)
			return nil
		}))
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewObject("o").SetInteger("k", 2)
			return nil
		}))

		pairs := d.Root().GCElementPairMap()
		require.Len(t, pairs, 1)
		for _, pair := range pairs {
			assert.Same(t, d.Root().Object, pair.Parent(),
				"the pair the overwrite registered names a proxy")
		}
	})

	t.Run("array delete", func(t *testing.T) {
		d := document.New("pair-owner-array-delete")
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			a := r.SetNewArray("arr")
			a.AddNewObject().SetInteger("k", 1)
			a.AddInteger(9)
			return nil
		}))
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetArray("arr").Delete(0)
			return nil
		}, "remove arr[0]"))

		pairs := d.Root().GCElementPairMap()
		require.Len(t, pairs, 1)
		for _, pair := range pairs {
			assert.Same(t, d.Root().GetArray("arr").Array, pair.Parent(),
				"the pair names a proxy no owner lookup can produce")
		}
	})
}

// TestUndoRetiresClonePairAndSpareRestoredMember pins the consequence of the
// owner match on the root the json proxies actually register against.
//
// Undo runs Set.Execute on the clone before the root
// (Document.executeUndoRedo), and the entry it has to retire there is the one
// json.Object.Delete registered. If that entry names a proxy, the retire
// misses it, and the clone keeps a worklist entry that now resolves -- through
// the createdAt the restore took over -- to the live member. The next
// collection pass purges it from the clone alone, so the document's own
// updaters and Root() readers lose a member the root still has.
func TestUndoRetiresClonePairAndSparesRestoredMember(t *testing.T) {
	d := document.New("clone-pair-retire")

	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("o").SetInteger("k", 1)
		return nil
	}))
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("o")
		return nil
	}, "remove o"))
	require.NoError(t, d.Undo())

	assert.Empty(t, d.Root().GCElementPairMap(),
		"the clone kept the tombstone's worklist entry after the restore")

	d.GarbageCollect(helper.MaxVersionVector(d.ActorID()))

	assert.Equal(t, `{"o":{"k":1}}`, d.Marshal())
	assert.Equal(t, `{"o":{"k":1}}`, d.Root().Marshal(),
		"collection took the restored member out of the clone")

	// The clone is what the next updater reads and writes, so a member it
	// lost is a member the next change cannot address.
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetObject("o").SetInteger("x", 5)
		return nil
	}))
	assert.Equal(t, `{"o":{"k":1,"x":5}}`, d.Marshal())
}
