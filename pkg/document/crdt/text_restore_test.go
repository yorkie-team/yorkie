/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Identity-preserving restore tests. These mirror the JS SDK verification
// harness (docs/design/undo-redo.md, "Overlapping Undo Content
// Duplication"): overlapping deletions that both undo must converge on the
// original content exactly once, independent of restore arrival order.

var restoreActor, _ = time.ActorIDFromHex("000000000000000000000000")

func tick(lamport int64) *time.Ticket {
	return time.NewTicket(lamport, 0, restoreActor)
}

// seededText builds "0123456789" as a single insertion (createdAt = seed)
// and returns the text plus that seed ticket. Both replicas call this with
// identical tickets, so node identities match across replicas.
func seededText(t *testing.T, seed *time.Ticket) *crdt.Text {
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), time.InitialTicket)
	from, to, err := text.CreateRange(0, 0)
	assert.NoError(t, err)
	_, _, _, err = text.Edit(from, to, "0123456789", nil, seed, nil)
	assert.NoError(t, err)
	assert.Equal(t, "0123456789", text.String())
	return text
}

// deleteRange tombstones [from, to) of the current visible text.
func deleteRange(t *testing.T, text *crdt.Text, from, to int, at *time.Ticket) {
	f, e, err := text.CreateRange(from, to)
	assert.NoError(t, err)
	_, _, _, err = text.Edit(f, e, "", nil, at, nil)
	assert.NoError(t, err)
}

func TestTextRestore(t *testing.T) {
	// d1 deletes [4,6)="45", d2 deletes [2,8)="234567" (the doc example).
	spanD1 := func(seed *time.Ticket) []*crdt.RestoreSpan {
		return []*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}}
	}
	spanD2 := func(seed *time.Ticket) []*crdt.RestoreSpan {
		return []*crdt.RestoreSpan{{CreatedAt: seed, Start: 2, End: 8, Content: "234567"}}
	}

	t.Run("overlapping undo converges in both restore orders", func(t *testing.T) {
		seed := tick(1)

		// Replica A: restore u1 then u2.
		a := seededText(t, seed)
		// Compute both ranges on the original, then apply both deletes.
		af1, af2, _ := a.CreateRange(4, 6)
		ag1, ag2, _ := a.CreateRange(2, 8)
		_, _, _, err := a.Edit(af1, af2, "", nil, tick(2), nil)
		assert.NoError(t, err)
		_, _, _, err = a.Edit(ag1, ag2, "", nil, tick(3), nil)
		assert.NoError(t, err)
		assert.Equal(t, "0189", a.String())
		a.Restore(spanD1(seed), tick(4))
		a.Restore(spanD2(seed), tick(5))

		// Replica B: identical deletes, restore u2 then u1.
		b := seededText(t, seed)
		bf1, bf2, _ := b.CreateRange(4, 6)
		bg1, bg2, _ := b.CreateRange(2, 8)
		_, _, _, err = b.Edit(bf1, bf2, "", nil, tick(2), nil)
		assert.NoError(t, err)
		_, _, _, err = b.Edit(bg1, bg2, "", nil, tick(3), nil)
		assert.NoError(t, err)
		b.Restore(spanD2(seed), tick(5))
		b.Restore(spanD1(seed), tick(4))

		assert.Equal(t, "0123456789", a.String(), "replica A")
		assert.Equal(t, "0123456789", b.String(), "replica B")
		assert.True(t, a.RGATreeSplit().CheckWeight(), "A weights")
		assert.True(t, b.RGATreeSplit().CheckWeight(), "B weights")
	})

	t.Run("only one side undone restores just its content", func(t *testing.T) {
		seed := tick(1)
		text := seededText(t, seed)
		f1, f2, _ := text.CreateRange(4, 6)
		g1, g2, _ := text.CreateRange(2, 8)
		_, _, _, _ = text.Edit(f1, f2, "", nil, tick(2), nil)
		_, _, _, _ = text.Edit(g1, g2, "", nil, tick(3), nil)

		// Undo only d1: it clears the tombstone on the "45" nodes, so "45"
		// reappears while "23"/"67" stay tombstoned by d2 (design doc's
		// "Order B": "01" + "45" + "89").
		text.Restore(spanD1(seed), tick(4))
		assert.Equal(t, "014589", text.String())
		// Undo d2 as well: the full range comes back exactly once.
		text.Restore(spanD2(seed), tick(5))
		assert.Equal(t, "0123456789", text.String())
	})

	t.Run("partial overlap [4,6) x [2,5)", func(t *testing.T) {
		seed := tick(1)
		text := seededText(t, seed)
		f1, f2, _ := text.CreateRange(4, 6)
		g1, g2, _ := text.CreateRange(2, 5)
		_, _, _, _ = text.Edit(f1, f2, "", nil, tick(2), nil)
		_, _, _, _ = text.Edit(g1, g2, "", nil, tick(3), nil)
		assert.Equal(t, "016789", text.String())

		text.Restore([]*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}}, tick(4))
		text.Restore([]*crdt.RestoreSpan{{CreatedAt: seed, Start: 2, End: 5, Content: "234"}}, tick(5))
		assert.Equal(t, "0123456789", text.String())
		assert.True(t, text.RGATreeSplit().CheckWeight())
	})

	t.Run("redo (retombstone) then undo again", func(t *testing.T) {
		seed := tick(1)
		text := seededText(t, seed)
		deleteRange(t, text, 4, 6, tick(2))
		assert.Equal(t, "01236789", text.String())

		text.Restore(spanD1(seed), tick(3))
		assert.Equal(t, "0123456789", text.String())

		text.Retombstone(spanD1(seed), tick(4))
		assert.Equal(t, "01236789", text.String())

		text.Restore(spanD1(seed), tick(5))
		assert.Equal(t, "0123456789", text.String())
		assert.True(t, text.RGATreeSplit().CheckWeight())
	})

	t.Run("duplicate restore is a no-op (idempotent)", func(t *testing.T) {
		seed := tick(1)
		text := seededText(t, seed)
		deleteRange(t, text, 4, 6, tick(2))
		text.Restore(spanD1(seed), tick(3))
		text.Restore(spanD1(seed), tick(3)) // duplicate delivery
		assert.Equal(t, "0123456789", text.String())
		assert.True(t, text.RGATreeSplit().CheckWeight())
	})
}

// TestTextRestoreExecuteAfterGC exercises the full server execution path:
// delete + restore as operations.Edit routed through Execute, with a GC
// purge between the deletes and the undos. This is the case that killed
// the earlier "resurrect" approach (purged tombstones cannot be
// un-tombstoned); identity-preserving restore recreates them from the
// carried span content instead. Both restore orders must converge.
func TestTextRestoreExecuteAfterGC(t *testing.T) {
	build := func(seed *time.Ticket) (*crdt.Root, *crdt.Text, *time.Ticket) {
		textTicket := tick(1)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), textTicket)
		from, to, err := text.CreateRange(0, 0)
		assert.NoError(t, err)
		_, _, _, err = text.Edit(from, to, "0123456789", nil, seed, nil)
		assert.NoError(t, err)

		root := helper.TestRoot()
		root.RegisterElement(text)
		return root, text, textTicket
	}

	del := func(t *testing.T, root *crdt.Root, text *crdt.Text, parent *time.Ticket, from, to int, at *time.Ticket) {
		f, e, err := text.CreateRange(from, to)
		assert.NoError(t, err)
		op := operations.NewEdit(parent, f, e, "", nil, at, nil, crdt.RestoreModeNone)
		assert.NoError(t, op.Execute(root, nil))
	}

	restore := func(t *testing.T, root *crdt.Root, parent *time.Ticket, spans []*crdt.RestoreSpan, at *time.Ticket) {
		// A restore op is identity-addressed; its from/to positions are
		// unused by the restore path except as a recreation fallback anchor.
		op := operations.NewEdit(parent, nil, nil, "", nil, at, spans, crdt.RestoreModeRestore)
		assert.NoError(t, op.Execute(root, nil))
	}

	spanD1 := func(seed *time.Ticket) []*crdt.RestoreSpan {
		return []*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}}
	}
	spanD2 := func(seed *time.Ticket) []*crdt.RestoreSpan {
		return []*crdt.RestoreSpan{{CreatedAt: seed, Start: 2, End: 8, Content: "234567"}}
	}

	run := func(t *testing.T, restoreU1First bool) resource.DocSize {
		seed := tick(1000) // distinct from ticket lamports used elsewhere
		root, text, parent := build(seed)

		// Concurrent overlapping deletes: compute both ranges on the
		// original, then apply both.
		f1, e1, _ := text.CreateRange(4, 6)
		f2, e2, _ := text.CreateRange(2, 8)
		op1 := operations.NewEdit(parent, f1, e1, "", nil, tick(1001), nil, crdt.RestoreModeNone)
		op2 := operations.NewEdit(parent, f2, e2, "", nil, tick(1002), nil, crdt.RestoreModeNone)
		assert.NoError(t, op1.Execute(root, nil))
		assert.NoError(t, op2.Execute(root, nil))
		assert.Equal(t, "0189", text.String())

		// Purge tombstones: the "234567" run is physically removed.
		n, err := root.GarbageCollect(helper.MaxVersionVector(restoreActor))
		assert.NoError(t, err)
		assert.Positive(t, n, "some tombstones should be purged")

		if restoreU1First {
			restore(t, root, parent, spanD1(seed), tick(1003))
			restore(t, root, parent, spanD2(seed), tick(1004))
		} else {
			restore(t, root, parent, spanD2(seed), tick(1003))
			restore(t, root, parent, spanD1(seed), tick(1004))
		}

		assert.Equal(t, "0123456789", text.String())
		assert.True(t, text.RGATreeSplit().CheckWeight())

		// The restores un-tombstone every remaining piece, so nothing should
		// be left registered for GC and docSize.GC must drain to zero (see
		// TestTextTombstoneSplitGC in gc_split_leak_test.go for the same
		// invariant across replicas).
		assert.Equal(t, 0, root.GarbageLen())
		assert.Equal(t, resource.DataSize{}, root.DocSize().GC)

		return root.DocSize()
	}

	var docSize1, docSize2 resource.DocSize
	t.Run("restore u1 then u2 after purge", func(t *testing.T) { docSize1 = run(t, true) })
	t.Run("restore u2 then u1 after purge", func(t *testing.T) { docSize2 = run(t, false) })

	// Restoring the smaller span first, when the larger span still fully
	// covers it as one gap, recreates fewer, larger fragments than the
	// reverse order (which recreates the larger span, then finds the
	// smaller one already live and skips it) — so Live.Meta legitimately
	// differs by fragment count between orders. Live.Data (visible content
	// bytes) is not: assert that instead, mirroring TestTextTombstoneSplitGC's
	// docSize-equality check without over-asserting on an implementation
	// detail that's allowed to vary.
	assert.Equal(t, docSize1.Live.Data, docSize2.Live.Data,
		"restore order must not affect visible content size")

	// keep del reachable for readers extending the matrix
	_ = del
}

// TestTextRestoreDocSizeAccounting pins the GC-size accounting of a partial
// restore, which splits a tombstone (isolateRange) to un-tombstone a middle
// piece. If the split-born target's GC pair is dropped instead of registered
// then un-registered, the original tombstone's size is left in docSize.GC and
// leaks. Purging every remaining tombstone must bring docSize.GC to exactly
// zero.
func TestTextRestoreDocSizeAccounting(t *testing.T) {
	seed := tick(2000)
	textTicket := tick(1)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), textTicket)
	root := helper.TestRoot()
	root.RegisterElement(text)

	exec := func(from, to int, content string, at *time.Ticket) {
		f, e, err := text.CreateRange(from, to)
		assert.NoError(t, err)
		op := operations.NewEdit(textTicket, f, e, content, nil, at, nil, crdt.RestoreModeNone)
		assert.NoError(t, op.Execute(root, nil))
	}

	exec(0, 0, "0123456789", seed) // seed through Execute so docSize tracks it
	exec(2, 8, "", tick(2001))     // delete "234567" → single tombstone [2,8)
	assert.Equal(t, "0189", text.String())

	// Partial restore of the middle "45": splits the [2,8) tombstone into
	// [2,4) + [4,6) + [6,8) and un-tombstones [4,6) (the split-born target).
	restoreOp := operations.NewEdit(textTicket, nil, nil, "", nil, tick(2002),
		[]*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}},
		crdt.RestoreModeRestore)
	assert.NoError(t, restoreOp.Execute(root, nil))
	assert.Equal(t, "014589", text.String())

	// Purge the two remaining tombstones [2,4) and [6,8). If the target's
	// pair accounting leaked, docSize.GC would retain the "45" size here.
	_, err := root.GarbageCollect(helper.MaxVersionVector(restoreActor))
	assert.NoError(t, err)
	assert.Equal(t, 0, root.GarbageLen())

	gc := root.DocSize().GC
	assert.Zero(t, gc.Data, "GC data must be zero after purging all tombstones")
	assert.Zero(t, gc.Meta, "GC meta must be zero after purging all tombstones")
	assert.Equal(t, "014589", text.String())
}
