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

// Ordering harness for Tree.recreateFromSpan's parent check (issue #2008,
// item 1b).
//
// recreateFromSpan resolves the parent it recorded, checks that the node it
// found carries the SAME identity, and gives up when it does not -- "B1:
// parent gone". Identity is all it checks. A parent that is merely
// TOMBSTONED passes, so the purged child is recreated LIVE underneath a dead
// parent, invisible in the XML but registered in NodeMapByID. The next
// collection purges the parent with Node.RemoveChild, which clears the
// PARENT's own Index.Parent and touches none of its children -- so the
// recreated node keeps pointing at a subtree that is no longer hung off the
// root while staying registered. That breaks the invariant every position
// lookup relies on: a node registered in NodeMapByID is reachable from the
// root. A later floor lookup hands toTreePos a node in the detached subtree
// and the ascent walks off the top of it.
//
// The obvious repair -- also refuse a tombstoned parent -- swaps an
// identity test for a liveness test, and liveness is not the same KIND of
// fact. "Purged" is a decision every replica reaches identically, because a
// node is purged only once the whole cluster is past it. "Tombstoned" is
// replica-local and time-varying: a replica that applies the restore before
// it has received the parent's removal sees a live parent, one that applies
// it after sees a tombstone. Restore spans travel on the remote path too
// (operations/tree_edit.go skips only reverse CONSTRUCTION for a remote
// source, never the restore itself), so both replicas run the same branch on
// their own local answer. If the answer decides whether the content survives,
// the delivery order decides the document -- and a crash would have been
// traded for a divergence, which in a CRDT is worse.
//
// The existing unit suite cannot pose that question: it drives one replica,
// or two replicas in one order. These tests drive the SAME logical history
// into two replicas in the two possible orders and compare what they land on.
// They deliberately assert more than the XML. Two replicas that render the
// same string while holding different tombstones, different garbage
// accounting or different NodeMapByID populations have already diverged --
// the next edit, the next collection or the next snapshot rebuild is where it
// surfaces.
//
// NOTE: these tests FAIL on unpatched main. That is the finding, not a
// broken harness. See the file's closing comment for the recorded baseline.

import (
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

const restoreOrderDocKey = "tree-restore-order"

// The actors in the ordering scenarios. Named so the version vector, the
// fixture and the observers cannot drift apart: a vector that misses an
// actor leaves that actor's changes causally unstable and collection quietly
// declines to purge, which would make the whole harness measure nothing.
const (
	actorAuthorA      = 1 // removes the text, then undoes its own removal
	actorAuthorB      = 2 // removes the enclosing element
	actorRemoveFirst  = 3 // observer: removal delivered before the restore
	actorRestoreFirst = 4 // observer: restore delivered before the removal
)

// newOrderReplica returns a document with a distinct actor id, so the
// replicas in these tests are genuinely distinct peers and the changes they
// produce are genuinely concurrent. Shares newReplica with
// restore_precondition_test.go, which asks the neighbouring question about
// the object path.
func newOrderReplica(t *testing.T, n int) *document.Document {
	t.Helper()

	return newReplica(t, restoreOrderDocKey, fmt.Sprintf("0000000000000000000000%02d", n))
}

// recordChanges is takeChanges with the emptiness check these scenarios need:
// a step that silently produced nothing -- an undo that found no entry, say --
// would make both delivery orders trivially agree and the test would pass
// while measuring nothing.
func recordChanges(t *testing.T, from *document.Document, what string) []*change.Change {
	t.Helper()

	changes := takeChanges(t, from)
	require.NotEmpty(t, changes, "%s produced no change to deliver", what)

	return changes
}

// guard runs fn with panics captured. A harness that reproduces a
// reachability break is one step away from the panic that break causes, and a
// panic in a test goroutine takes the whole binary down -- every other test
// in the package loses its result. Capturing it keeps the suite usable and
// turns the crash into a reported failure with a stack, which is what the
// next reader needs anyway.
func guard(t *testing.T, what string, fn func()) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v\n%s", what, r, debug.Stack())
		}
	}()
	fn()
}

// deliverInOrder delivers changes to a replica with panics captured.
func deliverInOrder(t *testing.T, to *document.Document, what string, changes []*change.Change) {
	t.Helper()

	guard(t, what, func() {
		if err := to.ApplyChangePack(change.NewPack(
			restoreOrderDocKey, change.NewCheckpoint(0, 0), changes, time.InitialVersionVector, nil,
		)); err != nil {
			t.Errorf("%s failed to apply: %v", what, err)
		}
	})
}

// collect runs a collection pass with panics captured.
func collect(t *testing.T, doc *document.Document, what string, vector time.VersionVector) {
	t.Helper()

	guard(t, what, func() { doc.GarbageCollect(vector) })
}

// treeState is everything about a replica's tree that two peers holding the
// same history must agree on. It is deliberately wider than the XML: the XML
// hides tombstones, and the whole defect here lives in nodes the XML cannot
// show.
type treeState struct {
	// XML is the rendered content.
	XML string
	// Nodes is every node reachable from the root, tombstones included,
	// with its identity and liveness.
	Nodes []string
	// Reachable counts the nodes actually hanging off the root.
	Reachable int
	// Registered counts the entries in NodeMapByID. Reachable != Registered
	// means a registered node is not reachable from the root (or two nodes
	// share an id) -- the invariant this whole harness is about.
	Registered int
	// GarbageLen and Size are the collection state. Two replicas that render
	// the same document while charging different garbage have diverged.
	GarbageLen int
	Size       string
}

// String renders the state for a failure message. Assertions compare these
// renderings rather than the structs so a diff shows WHAT differs instead of
// reporting two opaque values as unequal.
func (s treeState) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "xml=%s\n", s.XML)
	fmt.Fprintf(&b, "reachable=%d registered=%d garbageLen=%d size=%s\n",
		s.Reachable, s.Registered, s.GarbageLen, s.Size)
	for _, n := range s.Nodes {
		fmt.Fprintf(&b, "  %s\n", n)
	}

	return b.String()
}

// liveIDs returns the sorted ids of the live nodes, the content view that
// survives a replica collecting on a different schedule than its peer.
func (s treeState) liveIDs() []string {
	var ids []string
	for _, n := range s.Nodes {
		if strings.HasSuffix(n, "removed=false") {
			ids = append(ids, strings.SplitN(n, " ", 2)[0])
		}
	}
	slices.Sort(ids)

	return ids
}

// snapshotTree reads the state of the tree under key "t" off the document's
// real root -- not the clone Document.Root hands out -- so it reflects what
// is actually synced.
func snapshotTree(t *testing.T, doc *document.Document) treeState {
	t.Helper()

	tree := treeCRDT(t, doc)
	nodes := tree.Nodes()

	state := treeState{
		XML:        tree.ToXML(),
		Reachable:  len(nodes),
		Registered: tree.NodeLen(),
		GarbageLen: doc.GarbageLen(),
		Size:       fmt.Sprintf("%+v", doc.DocSize()),
	}
	for _, node := range nodes {
		state.Nodes = append(state.Nodes, fmt.Sprintf("%s type=%s value=%q removed=%v",
			node.IDString(), node.Type(), node.Value, node.RemovedAt() != nil))
	}
	slices.Sort(state.Nodes)

	return state
}

// census returns the nodes a replica holds right now, as pointers.
//
// NodeMapByID cannot be enumerated from outside the crdt package -- llrb.Tree
// offers Floor and nothing that walks -- so the only way to NAME a node that a
// later collection stranded is to have taken hold of it while it was still
// reachable. Take a census immediately before the collection under test and
// hand it to assertNoOrphans afterwards.
func census(t *testing.T, doc *document.Document) []*crdt.TreeNode {
	t.Helper()

	return treeCRDT(t, doc).Nodes()
}

// assertNoOrphans pins the property every position lookup depends on: every
// node registered in NodeMapByID is reachable from the root. findFloorNode
// answers out of NodeMapByID and toTreePos ascends from whatever it hands
// back, so a registered node in a detached subtree is an ascent that walks
// off the top of that subtree -- the #2008 panic.
//
// Two independent signals, because either can fire alone. The counts catch
// any mismatch, including an orphan that is a childless leaf and therefore
// invisible to any traversal. The census names the culprits: a node the map
// still answers with, whose parent chain no longer terminates at the root.
func assertNoOrphans(
	t *testing.T, doc *document.Document, before []*crdt.TreeNode, label string,
) {
	t.Helper()

	tree := treeCRDT(t, doc)
	state := snapshotTree(t, doc)
	assert.Equal(t, state.Registered, state.Reachable,
		"%s: NodeMapByID holds %d nodes but only %d are reachable from the root\n%s",
		label, state.Registered, state.Reachable, state)

	root := tree.IndexTree.Root()
	var orphans []string
	for _, node := range before {
		key, held := tree.NodeMapByID.Floor(node.ID())
		if held != node || !key.Equal(node.ID()) {
			continue // purged, or the map answers this id with someone else.
		}

		reachable := false
		for cur := node.Index; cur != nil; cur = cur.Parent {
			if cur == root {
				reachable = true
				break
			}
		}
		if !reachable {
			orphans = append(orphans, fmt.Sprintf("%s (removed=%v)",
				node.IDString(), node.RemovedAt() != nil))
		}
	}
	slices.Sort(orphans)
	assert.Empty(t, orphans,
		"%s: these nodes are still registered but no longer reachable from the root: %v\n%s",
		label, orphans, state)
}

// assertConverged compares two replicas that have applied the SAME set of
// changes. Content first so a content divergence is reported as such, then
// the full state so a tombstone-only or accounting-only divergence is not
// mistaken for agreement. Both sides are dumped on every failure.
func assertConverged(t *testing.T, labelX, labelY string, x, y *document.Document) {
	t.Helper()

	sx, sy := snapshotTree(t, x), snapshotTree(t, y)

	assert.Equal(t, sx.XML, sy.XML,
		"content diverged between delivery orders\n%s:\n%s\n%s:\n%s",
		labelX, sx, labelY, sy)
	assert.Equal(t, sx.liveIDs(), sy.liveIDs(),
		"the live node sets diverged between delivery orders\n%s:\n%s\n%s:\n%s",
		labelX, sx, labelY, sy)
	assert.Equal(t, sx.String(), sy.String(),
		"replica state diverged between delivery orders (tombstones, garbage or accounting)\n%s:\n%s\n%s:\n%s",
		labelX, sx, labelY, sy)
}

// buildTree writes <r><p>abcd</p></r> under key "t".
func buildTree(t *testing.T, doc *document.Document) {
	t.Helper()

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: textNodeType, Value: "abcd"}}},
		}})
		return nil
	}))
}

// TestTreeRestoreOrphansNodeUnderTombstonedParent is the control: the
// reachability break with NO split anywhere and a correctly aligned
// top-of-stack undo.
//
// It matters that no split is involved. The split/undo hole (an edit that
// both splits and removes gets no reverse, so Undo reverts the wrong entry)
// is a separate defect, and a reproduction that leans on it proves only that
// the two interact. Here d1's undo pops exactly the entry d1's own last
// change pushed.
//
//	d1: <r><p>abcd</p></r>, then remove "bc"
//	both collect, so "bc" is PURGED everywhere -- restoring it must recreate
//	d2: remove the enclosing <p>; the change reaches d1 while uncollected
//	d1: undo its own "remove bc"
//
// recreateFromSpan finds <p> by identity, does not look at its removedAt,
// and recreates "bc" live underneath the tombstone. The next collection
// purges <p> and leaves "bc" registered in a subtree hanging off nothing.
func TestTreeRestoreOrphansNodeUnderTombstonedParent(t *testing.T) {
	d1 := newOrderReplica(t, actorAuthorA)
	d2 := newOrderReplica(t, actorAuthorB)
	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())

	buildTree(t, d1)
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(2, 4, nil, 0)
		return nil
	}, "remove bc"))
	deliverChanges(t, d1, d2)

	// Both replicas collect, so the removed text is purged, not tombstoned:
	// the undo below has to RECREATE it, which is the path under test.
	collect(t, d1, "d1 first collection", vector)
	collect(t, d2, "d2 first collection", vector)
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, d1))
	require.Equal(t, 4, treeCRDT(t, d1).NodeLen(), "the removed text should be purged, not tombstoned")

	// d2 removes the enclosing <p> and d1 hears about it before undoing.
	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}, "remove p"))
	deliverChanges(t, d2, d1)
	require.Equal(t, "<r></r>", treeXML(t, d1))

	// d1 undoes its own most recent change. <p> is a tombstone on d1 now.
	require.NoError(t, d1.Undo())

	// Already broken here, silently: "bc" is live under a tombstoned parent,
	// which is why the undo reads as a content no-op to the user.
	afterUndo := snapshotTree(t, d1)
	assert.Equal(t, "<r></r>", afterUndo.XML,
		"the undo is invisible because it landed under a tombstone\n%s", afterUndo)

	// d2 gets the undo, so both replicas hold the whole history. It is the
	// same delivery order on both -- removal, then restore -- so this is not
	// yet the ordering question; it is the reachability question.
	deliverChanges(t, d1, d2)

	// The collection that turns the tombstoned parent into a detached
	// subtree. Everything is causally stable, so <p> is purged. Census first,
	// so the stranded node can be named afterwards.
	beforeCollect1, beforeCollect2 := census(t, d1), census(t, d2)
	collect(t, d1, "d1 second collection", vector)
	collect(t, d2, "d2 second collection", vector)

	// Both replicas agree -- on a document that violates the invariant.
	assertConverged(t, "d1 (undid the removal)", "d2 (received the undo)", d1, d2)
	assertNoOrphans(t, d1, beforeCollect1, "d1 after the parent was purged")
	assertNoOrphans(t, d2, beforeCollect2, "d2 after the parent was purged")
}

// orderFixture is one logical history, recorded as change slices so it can be
// replayed into replicas in different orders. Every replica in a scenario
// receives exactly the same slices; only the order differs.
type orderFixture struct {
	// setup builds <r><p>abcd</p></r> and removes "bc" (actor A).
	setup []*change.Change
	// remove removes the enclosing <p> (actor B).
	remove []*change.Change
	// restore is A's undo of its own "remove bc", produced BEFORE A has seen
	// remove -- so the two are genuinely concurrent and a replica may
	// legitimately receive them in either order.
	restore []*change.Change
	// undoRemove is B's undo of its own removal, causally after remove.
	// Delivered last in both orders when a scenario uses it.
	undoRemove []*change.Change
	// vector covers every actor, so a collection pass purges everything that
	// is causally stable.
	vector time.VersionVector
}

// newOrderFixture produces the concurrent pair (and B's later undo of its own
// removal) once, so both delivery orders replay byte-identical changes.
//
// The concurrency is the point. If A undid AFTER receiving B's removal, the
// undo would causally follow it and no replica could ever see them in the
// other order -- there would be no ordering question to ask. A undoes first,
// so the restore and the removal are concurrent and a CRDT owes the same
// result either way round.
func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()

	a := newOrderReplica(t, actorAuthorA)
	b := newOrderReplica(t, actorAuthorB)
	// The observers do not exist yet, but their actors have to be in the
	// vector: a collection pass only purges what the whole cluster is past.
	vector := helper.MaxVersionVector(
		a.ActorID(), b.ActorID(),
		newOrderReplica(t, actorRemoveFirst).ActorID(),
		newOrderReplica(t, actorRestoreFirst).ActorID(),
	)

	buildTree(t, a)
	require.NoError(t, a.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(2, 4, nil, 0)
		return nil
	}, "remove bc"))
	setup := recordChanges(t, a, "the setup edits")

	// B needs the setup and a collection pass so "bc" is purged on B too --
	// otherwise B's removal would tombstone it and the restore would take
	// the un-tombstone path instead of the recreate path under test.
	deliverInOrder(t, b, "b applies setup", setup)
	collect(t, a, "a collects setup", vector)
	collect(t, b, "b collects setup", vector)
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, b))

	require.NoError(t, b.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}, "remove p"))
	remove := recordChanges(t, b, "b's removal of <p>")

	require.NoError(t, a.Undo())
	restore := recordChanges(t, a, "a's undo of its own text removal")

	require.NoError(t, b.Undo())
	undoRemove := recordChanges(t, b, "b's undo of its own <p> removal")

	return &orderFixture{
		setup:      setup,
		remove:     remove,
		restore:    restore,
		undoRemove: undoRemove,
		vector:     vector,
	}
}

// observer returns a replica that already holds the fixture's setup and has
// collected it, ready to receive the concurrent pair in a chosen order.
func (f *orderFixture) observer(t *testing.T, n int) *document.Document {
	t.Helper()

	doc := newOrderReplica(t, n)
	deliverInOrder(t, doc, fmt.Sprintf("observer %d applies setup", n), f.setup)
	collect(t, doc, fmt.Sprintf("observer %d collects setup", n), f.vector)
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, doc))

	return doc
}

// TestTreeRestoreConvergesUnderEitherDeliveryOrder is the ordering question
// in its simplest form.
//
// Two replicas, the same two concurrent changes, opposite delivery orders:
//
//	removeFirst:  <p> removed, THEN the restore arrives -- the restore sees a
//	              TOMBSTONED parent
//	restoreFirst: the restore arrives, THEN <p> is removed -- the restore
//	              sees a LIVE parent
//
// Whatever recreateFromSpan decides, it must decide it in a way that lands
// both replicas on the same document. Anything that keys off the parent's
// local liveness cannot, unless the removal that follows in the other order
// happens to erase the difference.
//
// Asserted before collection as well as after: collection is what converts
// the difference into an unreachable node, but the replicas have already
// disagreed by then and a fix is allowed to repair either stage.
func TestTreeRestoreConvergesUnderEitherDeliveryOrder(t *testing.T) {
	f := newOrderFixture(t)

	removeFirst := f.observer(t, actorRemoveFirst)
	deliverInOrder(t, removeFirst, "removeFirst <- remove", f.remove)
	deliverInOrder(t, removeFirst, "removeFirst <- restore", f.restore)

	restoreFirst := f.observer(t, actorRestoreFirst)
	deliverInOrder(t, restoreFirst, "restoreFirst <- restore", f.restore)
	deliverInOrder(t, restoreFirst, "restoreFirst <- remove", f.remove)

	assertConverged(t, "removeFirst", "restoreFirst", removeFirst, restoreFirst)

	beforeRemoveFirst, beforeRestoreFirst := census(t, removeFirst), census(t, restoreFirst)
	collect(t, removeFirst, "removeFirst collects", f.vector)
	collect(t, restoreFirst, "restoreFirst collects", f.vector)

	assertConverged(t, "removeFirst after collection", "restoreFirst after collection",
		removeFirst, restoreFirst)
	assertNoOrphans(t, removeFirst, beforeRemoveFirst, "removeFirst after collection")
	assertNoOrphans(t, restoreFirst, beforeRestoreFirst, "restoreFirst after collection")
}

// TestTreeRestoreConvergesWhenTheParentRemovalIsUndone is the same ordering
// question asked where the answer is VISIBLE.
//
// In the test above both replicas render "<r></r>" whichever order they used,
// because the restored text is invisible under a removed parent either way --
// the disagreement shows up only in tombstones and accounting. Here B undoes
// its own removal afterwards, which brings <p> back on both replicas and puts
// the restored text back on screen. Now the two orders have to agree about
// something a user can read.
//
// B's undo restores what B removed, and B removed only <a>, <d> and <p> --
// "bc" was already purged on B, so B's restore spans do not mention it. The
// fate of "bc" is therefore decided entirely by what each replica did when
// the concurrent restore arrived, which is exactly the question.
//
// This is also the scenario that prices the two candidate repairs. Refusing
// a tombstoned parent discards content the user explicitly asked to restore;
// whether that refusal is at least CONSISTENT across orders is what this
// measures, and consistency is the property a CRDT cannot trade away.
func TestTreeRestoreConvergesWhenTheParentRemovalIsUndone(t *testing.T) {
	f := newOrderFixture(t)

	removeFirst := f.observer(t, actorRemoveFirst)
	deliverInOrder(t, removeFirst, "removeFirst <- remove", f.remove)
	deliverInOrder(t, removeFirst, "removeFirst <- restore", f.restore)
	deliverInOrder(t, removeFirst, "removeFirst <- undoRemove", f.undoRemove)

	restoreFirst := f.observer(t, actorRestoreFirst)
	deliverInOrder(t, restoreFirst, "restoreFirst <- restore", f.restore)
	deliverInOrder(t, restoreFirst, "restoreFirst <- remove", f.remove)
	deliverInOrder(t, restoreFirst, "restoreFirst <- undoRemove", f.undoRemove)

	assertConverged(t, "removeFirst", "restoreFirst", removeFirst, restoreFirst)

	beforeRemoveFirst, beforeRestoreFirst := census(t, removeFirst), census(t, restoreFirst)
	collect(t, removeFirst, "removeFirst collects", f.vector)
	collect(t, restoreFirst, "restoreFirst collects", f.vector)

	assertConverged(t, "removeFirst after collection", "restoreFirst after collection",
		removeFirst, restoreFirst)
	assertNoOrphans(t, removeFirst, beforeRemoveFirst, "removeFirst after collection")
	assertNoOrphans(t, restoreFirst, beforeRestoreFirst, "restoreFirst after collection")
}

// Baseline on unpatched main (75e1ae61), for whoever applies a candidate on
// top of this harness:
//
// TestTreeRestoreOrphansNodeUnderTombstonedParent -- SILENTLY ORPHANS, no
// panic, and the two replicas CONVERGE on the broken state. After the undo,
// "bc" is live under the tombstoned <p>; the XML is "<r></r>" so the undo
// reads as a no-op. After the second collection BOTH replicas report
// reachable=1, registered=2 -- one registered node with no path to the root
// -- while still charging Live{Data:4,Meta:120} for content nothing can
// reach. Only the reachability assertions fail; the convergence assertion
// passes, which is the point of keeping the two separate.
//
// Both orphan signals fire: the counts (1 reachable vs 2 registered) and the
// census, which names it -- 1:3:<actor>:1 (removed=false), the recreated
// "bc".
//
// TestTreeRestoreConvergesUnderEitherDeliveryOrder -- DIVERGES, no panic, and
// it diverges BEFORE any collection. The XML agrees ("<r></r>" both ways) but
// nothing else does: removeFirst holds "bc" LIVE (garbageLen=3,
// Live{Data:4,Meta:120}, GC{Data:4,Meta:144}) while restoreFirst holds it
// TOMBSTONED (garbageLen=4, Live{Data:0,Meta:96}, GC{Data:8,Meta:192}) --
// because on restoreFirst the node existed, live, when the removal arrived,
// so the removal swept it up. After collection removeFirst is reachable=1,
// registered=2 and still charges Live{Data:4}; restoreFirst is 1/1 with
// Live{Data:0}. Worth stating plainly: main is ALREADY order-dependent here.
// The one-line refusal does not introduce a divergence into a converging
// system; the question it has to answer is whether it removes this one.
//
// TestTreeRestoreConvergesWhenTheParentRemovalIsUndone -- DIVERGES IN
// CONTENT, no panic. removeFirst renders "<r><p>abcd</p></r>" and
// restoreFirst renders "<r><p>ad</p></r>". Same changes, same count, opposite
// order, two different documents -- on main, with no fix applied. Both sides
// are internally consistent (5/5 and 4/4 after collection), so this one is a
// pure divergence with no orphan.
//
// Taken together: the orphan and the order-dependence are the same defect
// seen from two sides, and the control for any candidate is not "does it
// still converge" but "does it converge where main does not".
