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

package document

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestUndoRetiresClonePairRegisteredByProxy pins the parent the json proxies
// record when they book a removed element into GC: the CRDT container, not the
// proxy wrapping it.
//
// Root.UnregisterRemovedElementPair compares the recorded parent to its owner by
// identity, and the owner a restoring Set passes is whatever
// Root.FindByCreatedAt answers with -- always the *crdt.Object. A pair the
// proxy recorded under its own identity therefore matches no owner, and the
// retire the restore depends on is silently skipped. Only the clone root sees
// this: the live root's pairs are registered by operations.Remove, which already
// records the container. So the clone keeps a collection entry for a tombstone
// the undo has just re-pointed at live data, and its docSize keeps the charge.
func TestUndoRetiresClonePairRegisteredByProxy(t *testing.T) {
	doc := New("d1")

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "v")
		return nil
	}))

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("k")
		return nil
	}))

	// Both roots booked the removed member: the live root through
	// operations.Remove, the clone through json.Object.Delete.
	require.NotNil(t, doc.cloneRoot)
	require.Equal(t, 1, doc.cloneRoot.GarbageLen())
	require.Equal(t, 1, doc.doc.root.GarbageLen())

	require.NoError(t, doc.Undo())

	// The restore re-pointed both roots' nodeMapByCreatedAt at the restored
	// copy, so neither may keep an entry that now resolves to live data.
	assert.Equal(t, 0, doc.doc.root.GarbageLen())
	assert.Equal(t, 0, doc.cloneRoot.GarbageLen(),
		"the clone kept the tombstone's collection entry: the pair was recorded "+
			"under the proxy rather than the crdt.Object, so the owner check skipped it")
	assert.Equal(t, `{"k":"v"}`, doc.Marshal())
}
