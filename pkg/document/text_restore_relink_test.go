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

	"github.com/yorkie-team/yorkie/test/helper"
)

// TestTextRestoreRelink is a regression for yorkie-team/yorkie-js-sdk#1327.
//
// restore()'s gap-recreate branch inserts a recreated node with InsertAfter,
// which only maintains the physical prev/next chain -- not the separate
// insertion chain (insPrev/insNext). So after a purged interior fragment is
// recreated on undo, the surviving same-insertion neighbours still point their
// insertion pointers past the recreated node. A later edit whose boundary
// lands on that node resolves its offset through the stale insertion chain in
// findFloorNodePreferToLeft and miscomputes it -- dropping the edit or, as
// here, erroring because the offset exceeds the resolved node's length.
func TestTextRestoreRelink(t *testing.T) {
	t.Run("later boundary edit stays correct after a purged interior fragment is recreated test", func(t *testing.T) {
		// Single insertion "0123456789": one node, id (t1:0).
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "0123456789")
		assert.Equal(t, "0123456789", textOf(t, doc, "t").String())

		// Delete the interior "45" (indices [4,6)). The node splits into
		// (t1:0)"0123" - {t1:4 "45"} - (t1:6)"6789"; the middle is tombstoned.
		editText(t, doc, 4, 6, "")
		assert.Equal(t, "01236789", textOf(t, doc, "t").String())
		assert.Equal(t, 1, doc.GarbageLen())

		// Purge the tombstone so Undo cannot un-tombstone in place and must
		// recreate the "45" fragment through restore()'s gap branch.
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())

		// Undo recreates (t1:4)"45" via InsertAfter -- the physical chain is
		// repaired ("0123456789") but the insertion chain around it stays stale.
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "0123456789", textOf(t, doc, "t").String())

		// The bug bites here: an edit whose boundary (index 6) sits exactly at
		// the recreated node's right edge resolves through the stale insertion
		// chain and miscomputes its offset.
		editText(t, doc, 6, 6, "X")
		assert.Equal(t, "012345X6789", textOf(t, doc, "t").String())
	})
}
