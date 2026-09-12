package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestRGATreeListSkipIsPurgeInvariant pins the property this list's insertion
// rule has to have for collection to be safe: where an insert lands must not
// depend on whether a tombstone between the anchor and the insertion point has
// already been unlinked.
//
// The shape that breaks the plain forward skip is a tombstone whose own ticket
// is OLDER than the incoming insert, holding a child whose ticket is NEWER:
//
//	a ── T(t2) ── D(t3)          insert Y(tY) anchored at a, t2 < tY < t3
//
// With T linked, the walk stops at T -- t2 is not after tY -- and Y lands
// directly after a. With T unlinked, the walk meets D first, whose t3 IS after
// tY, skips it, and Y lands after D instead. Same operation, same anchor, two
// different orders, and nothing brings the two replicas back together.
//
// D carries the ticket of the anchor it was created on, so the walk can tell
// that D hangs off something older than Y even when that something is gone.
func TestRGATreeListSkipIsPurgeInvariant(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	tA := ctx.IssueTimeTicket()
	tT := ctx.IssueTimeTicket()
	tY := ctx.IssueTimeTicket()
	tD := ctx.IssueTimeTicket()
	tDel := ctx.IssueTimeTicket()

	build := func(t *testing.T, purge bool) string {
		t.Helper()
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
		assert.Equal(t, `["a","T","d"]`, arr.Marshal())

		_, err = arr.DeleteByCreatedAt(tT, tDel)
		assert.NoError(t, err)
		if purge {
			assert.NoError(t, arr.Purge(target))
		}

		// The concurrent insert, anchored on a with a ticket that sits between
		// the tombstone's and its child's.
		y, err := crdt.NewPrimitive("y", tY)
		assert.NoError(t, err)
		assert.NoError(t, arr.InsertAfter(tA, y, nil))

		return arr.Marshal()
	}

	kept := build(t, false)
	collected := build(t, true)

	assert.Equal(t, `["a","y","d"]`, kept)
	assert.Equal(t, kept, collected,
		"the insertion point moved because a tombstone was collected")
}
