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

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestCollectLeavesAPairOfALiveNode pins that a collection neither purges
// nor books anything for a pair whose child is live (yorkie#2245): the pair
// stays, GC and Live keep their sizes, and the node stays in the tree.
func TestCollectLeavesAPairOfALiveNode(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "div", nil)
	require.NoError(t, root.Append(div))
	tree := NewTree(root, time.NewTicket(3, 0, actor))

	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	size := div.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: div, GCOnlySize: &size})
	before := doc.DocSize()

	vector := time.NewVersionVector()
	vector.Set(actor, 10)
	n, err := doc.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, 1, doc.GarbageLen(), "the pair stays")
	assert.Equal(t, before, doc.DocSize())
	assert.Same(t, tree.Root(), div.Index.Parent.Value)
}

// TestChargeToRemovedElementKeepsAReleaseRecordZero pins that a write into a
// released subtree leaves release's marker alone and reaches neither ledger.
//
// release records a zero DataSize to say neither side is holding the element,
// and index retires that record only while it is still exactly zero. Charging
// a tree op onto it would make it look like a tombstone's own charge, so the
// record -- and the dead subtree behind it -- would never be retired. Leaving
// the delta in Live is the other way to get it wrong: release has already
// taken the orphan's bytes out of Live, so a delete inside it would subtract
// them a second time and drive the recorded live size below the real one.
func TestChargeToRemovedElementKeepsAReleaseRecordZero(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	createdAt := time.NewTicket(3, 0, actor)

	node := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(node, createdAt)

	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.index(tree)
	doc.release(tree)
	require.Equal(t, resource.DataSize{}, doc.sizeInGC[tree],
		"release records a zero marker, not a charge")

	liveBefore := doc.DocSize().Live
	gcBefore := doc.DocSize().GC
	end := doc.ChargeTreeOp(tree)
	doc.Acc(resource.DataSize{Data: 12})
	end()

	assert.Equal(t, resource.DataSize{}, doc.sizeInGC[tree],
		"a release record stays zero so index can still retire it")
	assert.Equal(t, gcBefore, doc.DocSize().GC, "nothing moves into GC")
	assert.Equal(t, liveBefore, doc.DocSize().Live,
		"an orphan is on neither side of the ledger, so its writes are charged to neither")

	// The shrinking direction is the one the size gate feels: CheckLiveSize
	// reads Live alone, so a delete inside the orphan that reached Live would
	// take the recorded live size below the real one -- here below zero --
	// and admit a document the limit should turn away.
	end = doc.ChargeTreeOp(tree)
	doc.Acc(resource.DataSize{Data: -40})
	end()

	assert.Equal(t, liveBefore, doc.DocSize().Live,
		"a delete inside a released subtree does not subtract from Live twice")
	assert.GreaterOrEqual(t, doc.DocSize().Live.Data, 0, "the recorded live size stays non-negative")
	assert.Equal(t, gcBefore, doc.DocSize().GC, "and nothing moves into GC either")

	// The copy restored under the same createdAt takes the slot over, which
	// is what retires the record. A topped-up record would survive this.
	copyTree := NewTree(NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "r", nil), createdAt)
	doc.index(copyTree)
	_, held := doc.sizeInGC[tree]
	assert.False(t, held, "the release record is retired once nothing can address it")
}
