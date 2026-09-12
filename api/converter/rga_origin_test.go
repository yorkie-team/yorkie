package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestArraySnapshotKeepsInsertionAnchors pins that a snapshot carries what the
// insertion rule reads.
//
// The anchors are written differentially -- omitted when a node's anchor is the
// node physically before it, which is the usual case -- so a decoder that gets
// the reconstruction wrong produces an array that looks right and then orders
// the next concurrent insert differently from the replica it was copied from.
// Rebuilding the list by appending, which is what restoration does, cannot
// re-derive them: the node a new element ends up after is not in general the
// node its operation named.
func TestArraySnapshotKeepsInsertionAnchors(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	tA := ctx.IssueTimeTicket()
	tT := ctx.IssueTimeTicket()
	tY := ctx.IssueTimeTicket()
	tD := ctx.IssueTimeTicket()
	tDel := ctx.IssueTimeTicket()

	arr := crdt.NewArray(crdt.NewRGATreeList(), tA)
	a, err := crdt.NewPrimitive("a", tA)
	assert.NoError(t, err)
	assert.NoError(t, arr.Add(a))
	target, err := crdt.NewPrimitive("T", tT)
	assert.NoError(t, err)
	assert.NoError(t, arr.InsertAfter(tA, target, nil))
	d, err := crdt.NewPrimitive("d", tD)
	assert.NoError(t, err)
	assert.NoError(t, arr.InsertAfter(tT, d, nil))

	bytes, err := converter.ArrayToBytes(arr)
	assert.NoError(t, err)
	clone, err := converter.BytesToArray(bytes)
	assert.NoError(t, err)
	assert.Equal(t, arr.Marshal(), clone.Marshal())

	// Same edit on both: remove and collect the middle node, then insert with a
	// ticket that sits between the collected node's and its child's.
	for _, list := range []*crdt.Array{arr, clone} {
		removed, err := list.DeleteByCreatedAt(tT, tDel)
		assert.NoError(t, err)
		assert.NoError(t, list.Purge(removed))

		y, err := crdt.NewPrimitive("y", tY)
		assert.NoError(t, err)
		assert.NoError(t, list.InsertAfter(tA, y, nil))
	}

	assert.Equal(t, `["a","y","d"]`, arr.Marshal())
	assert.Equal(t, arr.Marshal(), clone.Marshal(),
		"the restored array lost the anchors the insertion rule reads")
}
