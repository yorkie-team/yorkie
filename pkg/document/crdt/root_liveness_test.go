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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestGarbageCollectSkipsLiveRegisteredElement pins that collection tolerates
// a registered pair whose element is not removed, instead of dereferencing a
// nil removedAt and deleting a live member.
//
// The two ways to reach that state are both out of this release:
// identity-preserving revive for Elements, which clears removedAt in place
// the way Text and Tree nodes already do, and the unconditional node return
// in RGATreeList.DeleteByCreatedAt, which registers a pair for a removal
// entry.elem.Remove declined. This test drives the state directly instead,
// because neither route is reachable from a causal change log today. It is
// the guard that makes either one land as a skipped entry rather than a panic
// inside a snapshot build.
func TestGarbageCollectSkipsLiveRegisteredElement(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	obj := crdt.NewObject(crdt.NewElementRHT(), ctx.IssueTimeTicket())
	root.Object().Set("obj", obj)
	root.RegisterElement(obj)

	removedAt := ctx.IssueTimeTicket()
	_, err := root.Object().DeleteByCreatedAt(obj.CreatedAt(), removedAt)
	require.NoError(t, err)
	root.RegisterRemovedElementPair(root.Object(), obj)
	assert.Equal(t, 1, root.GarbageLen())

	// Revive in place: the element keeps its identity and its registration,
	// and stops being a tombstone. Nothing in the Element path does this yet.
	obj.SetRemovedAt(nil)

	n, err := root.GarbageCollect(helper.MaxVersionVector())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "collection purged an element that is not removed")
	assert.Equal(t, obj, root.FindByCreatedAt(obj.CreatedAt()),
		"the revived element lost its registration")
	assert.True(t, root.Object().Has("obj"),
		"the revived element was unlinked from its key")
}
