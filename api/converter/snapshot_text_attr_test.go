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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// docWithRemovedTextAttr builds a document whose text node carries a
// tombstoned attribute.
//
// Undoing a Style that introduced a key issues a reverse Style carrying
// `attributesToRemove`, which is the only route that tombstones a text
// attribute: RHT.Remove keeps the node and flips `isRemoved`, stamping it
// with the removal's own ticket.
func docWithRemovedTextAttr(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("d")
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
		return nil
	}))
	assert.NoError(t, doc.Undo())

	return doc
}

// TestSnapshotKeepsTextAttrRemoval asserts that a tombstoned TEXT attribute
// stays tombstoned across a snapshot round-trip.
//
// `toTextNodes` writes each attribute's value and updatedAt but not
// `isRemoved`, while `toRHT` -- the same serialization for TREE node
// attributes -- does write it. The field defaults to false on decode, so the
// tombstone comes back as a LIVE attribute stamped with the REMOVAL's own
// ticket, which then wins LWW against anything older.
//
// That makes this a convergence bug rather than an accounting one: a replica
// served from a snapshot renders formatting that a replica which replayed the
// changes does not, and the two never reconcile.
func TestSnapshotKeepsTextAttrRemoval(t *testing.T) {
	doc := docWithRemovedTextAttr(t)

	encoded, err := converter.ObjectToBytes(doc.RootObject())
	assert.NoError(t, err)
	obj, err := converter.BytesToObject(encoded)
	assert.NoError(t, err)
	root := crdt.NewRoot(obj)

	// live    : [{"val":"abcdefghij"}]
	// snapshot: [{"attrs":{"b":"1"},"val":"abcdefghij"}]
	assert.Equal(t, doc.Root().GetText("k").Marshal(), root.Object().Get("k").Marshal(),
		"the snapshot resurrected a removed text attribute")

	// Even once the content matches, the tombstone has to be accounted for:
	// `Text.GCPairs` books a removed attribute through `TextValue.GCPairs`,
	// so a rebuilt root that dropped the flag is also short one GC pair.
	assert.Equal(t, doc.GarbageLen(), root.GarbageLen(),
		"the rebuilt root did not register the attribute tombstone for GC")
	assert.Equal(t, doc.DocSize().GC, root.DocSize().GC,
		"the rebuilt root charged the attribute tombstone to the wrong bucket")
}
