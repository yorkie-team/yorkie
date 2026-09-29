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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestArraySetRedoAfterPeerRemovalKeepsTheTombstoneOut pins that redoing an array
// assignment whose target a peer removed in the meantime never re-inserts
// that removed value.
//
// d1 assigns arr[0]; d2 removes the assigned value; d1 undoes, then redoes.
// The undo is an ArraySet whose own reverse was built from the value it
// displaces -- by then a tombstone, since GetByID returns removed elements --
// and executeUndoRedo gives that copy a fresh createdAt on redo while it keeps
// the older removedAt. A value whose removedAt does not follow its createdAt
// can be neither tombstoned nor purged, and when it is a container
// FromChangePack rejects it outright, so the server and every peer refuse the
// push. The Go json.Array only assigns primitives, which the wire does not
// check, so the invariant is asserted on the operation itself; a JS client
// assigning an object reaches the rejection.
func TestArraySetRedoAfterPeerRemovalKeepsTheTombstoneOut(t *testing.T) {
	d1 := document.New("array-set-redo")
	d2 := document.New("array-set-redo")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddInteger(1)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetInteger(0, 2)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(0)
		return nil
	}))
	deliverChanges(t, d2, d1)

	require.NoError(t, d1.Undo())
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Redo())

	pack := d1.CreateChangePack()
	for _, c := range pack.Changes {
		for _, op := range c.Operations() {
			set, ok := op.(*operations.ArraySet)
			if !ok {
				continue
			}
			v := set.Value()
			if removedAt := v.RemovedAt(); removedAt != nil {
				assert.True(t, removedAt.After(v.CreatedAt()),
					"redo re-inserts a value removed at %s under the later createdAt %s",
					removedAt.Key(), v.CreatedAt().Key())
			}
		}
	}

	pbPack, err := converter.ToChangePack(pack)
	require.NoError(t, err)
	_, err = converter.FromChangePack(pbPack)
	assert.NoError(t, err, "the redo change is refused at the wire boundary")

	// The peer removed the value being redone, so redo removes what the undo
	// restored rather than bringing the peer's removal back.
	deliverChanges(t, d1, d2)
	assert.Equal(t, `{"arr":[]}`, d1.Marshal())
	assert.Equal(t, d1.Marshal(), d2.Marshal())
	assertRebuildsSame(t, d1, "redo")
}
