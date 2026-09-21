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

// The second half of the ordering harness for Tree.recreateFromSpan (issue
// #2008, item 1b). tree_restore_order_test.go poses the question "does a
// restore under a TOMBSTONED parent converge?"; this file poses the three
// questions that one leaves open, and it owns the helpers that need to read a
// node the XML cannot show.
//
//  1. WHICH TICKET. A candidate that recreates the node already-tombstoned has
//     to stamp it with something. The parent's removedAt makes the two obvious
//     delivery orders agree -- but removedAt is LWW-OVERWRITABLE
//     (TreeNode.remove: "Overwrite if newer tombstone. This enables LWW for
//     concurrent deletions"). So a SECOND, concurrent removal of the same
//     parent carrying a HIGHER ticket rewrites the parent's removedAt after
//     the restore already read it, and two replicas that saw the three
//     changes in different orders can be left holding different removedAt on
//     the RESTORED node. removedAt feeds canDelete, so that is a divergence in
//     when each replica collects -- the same class of defect that got the
//     GC-site candidate rejected. TestTreeRestoreAgreesOnTheTombstoneTicket...
//     drives all six orders of the three concurrent changes and compares the
//     TICKET directly, because the XML agrees while the tickets do not.
//
//  2. ELEMENT AND ATTRIBUTE SPANS. Restore's non-text branch reaches the same
//     recreateFromSpan, rebuilding an element node and deep-copying its RHT.
//     The existing scenarios only ever recreate text, so nothing measured
//     whether an element span -- and the attributes riding on it -- behaves
//     the same under a tombstoned parent.
//
//  3. DEPTH. The existing scenarios tombstone the node's IMMEDIATE parent,
//     which is also a child of the root. A grandchild parent is the shape a
//     real editor produces (a mark inside a paragraph), and it is where an
//     orphaned subtree is left hanging under a LIVE ancestor rather than under
//     nothing -- a strictly worse state for any position lookup, because the
//     ascent terminates somewhere plausible.
//
// These reuse tree_restore_order_test.go wholesale: guard, deliverInOrder,
// collect, census, snapshotTree, assertConverged and assertNoOrphans. Nothing
// here duplicates them, and nothing here applies a fix -- the harness is the
// instrument and must stay independent of every candidate.
//
// NOTE: like the rest of the harness, these tests are expected to be RED on
// unpatched main. See the file's closing comment for the recorded baseline
// under BOTH main and the born-tombstoned candidate.

import (
	"fmt"
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

// Actors for the scenarios in this file. Disjoint from
// tree_restore_order_test.go's 1-4 so a fixture that needs both cannot alias
// two replicas onto one actor id and silently make their changes causally
// ordered instead of concurrent.
const (
	actorLWWRemover       = 5 // second, CONCURRENT remover of the same parent
	actorElemRemoveFirst  = 6 // element scenario: removal before the restore
	actorElemRestoreFirst = 7 // element scenario: restore before the removal
	actorDeepAuthorA      = 8 // depth scenario: removes the text, then undoes
	actorDeepAuthorB      = 9 // depth scenario: removes the enclosing mark
	// The six permutation observers of the ticket scenario take 10..15.
	actorTicketObserverBase = 10
)

// nodeByID answers the question NodeMapByID answers, which is NOT the question
// a traversal answers.
//
// Every assertion about a restored node has to survive the node becoming
// unreachable -- that is the defect. tree.Nodes() walks the index tree and so
// loses exactly the nodes worth looking at, while findFloorNode is unexported.
// Floor is the only door in from outside the crdt package, and it is also what
// the production position lookup uses, so reading through it measures what the
// server would actually find.
func nodeByID(t *testing.T, doc *document.Document, id *crdt.TreeNodeID) *crdt.TreeNode {
	t.Helper()

	key, node := treeCRDT(t, doc).NodeMapByID.Floor(id)
	if node == nil || key == nil || !key.Equal(id) {
		return nil
	}

	return node
}

// tombstoneOf renders a node's liveness as the TICKET it carries, not as a
// boolean.
//
// The boolean is what treeState already records and it is not enough here: two
// replicas can both report removed=true while holding different removedAt, and
// canDelete compares the ticket, so the difference decides which replica
// purges the node on which pass. The three answers are deliberately distinct
// strings so a failure message says which of "never recreated", "recreated
// live" and "recreated tombstoned at T" actually happened.
func tombstoneOf(t *testing.T, doc *document.Document, id *crdt.TreeNodeID) string {
	t.Helper()

	node := nodeByID(t, doc, id)
	if node == nil {
		return "absent"
	}
	if node.RemovedAt() == nil {
		return "live"
	}

	return node.RemovedAt().ToTestString()
}

// attrsOf renders a node's attributes, or why there are none to render.
// recreateFromSpan deep-copies span.Attributes onto the rebuilt element, so an
// element span that comes back without its attributes is a silent data loss
// the XML only shows once the node is visible again.
func attrsOf(t *testing.T, doc *document.Document, id *crdt.TreeNodeID) string {
	t.Helper()

	node := nodeByID(t, doc, id)
	if node == nil {
		return "absent"
	}

	return fmt.Sprintf("{%s}", node.Attributes())
}

// firstTicket reads the executedAt of the first operation in a recorded change
// slice, so a fixture can assert the causal facts it depends on instead of
// assuming them. A scenario built on "these two concurrent removals carry
// different tickets and this one wins" measures nothing if the assumption is
// wrong, and it would fail in a way that looks like the defect.
func firstTicket(t *testing.T, changes []*change.Change, what string) *time.Ticket {
	t.Helper()

	for _, c := range changes {
		for _, op := range c.Operations() {
			return op.ExecutedAt()
		}
	}
	t.Fatalf("%s carried no operation to read a ticket from", what)

	return nil
}

// namedChanges is one deliverable step of a scenario, kept with its name so a
// permutation can label itself in a failure message.
type namedChanges struct {
	name    string
	changes []*change.Change
}

// -----------------------------------------------------------------------------
// 1. The ticket question: a parent tombstone that is LWW-overwritten AFTER the
//    restore has already read it.
// -----------------------------------------------------------------------------

// ticketFixture is one logical history with THREE concurrent changes, recorded
// so every delivery order replays byte-identical changes:
//
//	removeLow  -- B removes <p>
//	removeHigh -- C removes <p>, concurrently, with a HIGHER ticket
//	restore    -- A undoes its own earlier removal of "bc", concurrently
//
// All three are produced from the same collected setup state and none of the
// three authors sees either of the others, so a replica may legitimately
// receive them in any of the six orders and a CRDT owes the same result for
// all six.
type ticketFixture struct {
	setup      []*change.Change
	removeLow  []*change.Change
	removeHigh []*change.Change
	restore    []*change.Change

	// lowAt/highAt are the two removals' tickets, kept so the test can state
	// in its failure message which one each replica landed on.
	lowAt  *time.Ticket
	highAt *time.Ticket

	// textID names the restored text node and parentID the <p> above it.
	// Captured BEFORE the setup collection, because after the purge there is
	// nothing left to name -- and the recreated node carries exactly this id,
	// which is the whole premise of identity-preserving restore.
	textID   *crdt.TreeNodeID
	parentID *crdt.TreeNodeID

	vector time.VersionVector
}

// ticketOrders enumerates the six delivery orders of the three concurrent
// changes, as indices into ticketFixture.steps(). Written out rather than
// generated: six lines that can be read against the scenario beat a
// permutation generator whose output has to be trusted.
var ticketOrders = [][3]int{
	{0, 1, 2}, {0, 2, 1},
	{1, 0, 2}, {1, 2, 0},
	{2, 0, 1}, {2, 1, 0},
}

func (f *ticketFixture) steps() []namedChanges {
	return []namedChanges{
		{"removeLow", f.removeLow},
		{"removeHigh", f.removeHigh},
		{"restore", f.restore},
	}
}

// observer returns a replica holding the collected setup, ready to receive the
// three concurrent changes in a chosen order.
func (f *ticketFixture) observer(t *testing.T, n int) *document.Document {
	t.Helper()

	doc := newOrderReplica(t, n)
	deliverInOrder(t, doc, fmt.Sprintf("observer %d applies setup", n), f.setup)
	collect(t, doc, fmt.Sprintf("observer %d collects setup", n), f.vector)
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, doc))

	return doc
}

func newTicketFixture(t *testing.T) *ticketFixture {
	t.Helper()

	a := newOrderReplica(t, actorAuthorA)
	b := newOrderReplica(t, actorAuthorB)
	c := newOrderReplica(t, actorLWWRemover)

	// Every actor that will ever hold this document has to be in the vector,
	// observers included: a collection pass only purges what the whole cluster
	// is past, so a missing actor would silently turn every collect() in this
	// file into a no-op and the fixture would never reach the purged state the
	// recreate path needs.
	actors := []time.ActorID{a.ActorID(), b.ActorID(), c.ActorID()}
	for i := range ticketOrders {
		actors = append(actors, newOrderReplica(t, actorTicketObserverBase+i).ActorID())
	}
	vector := helper.MaxVersionVector(actors...)

	buildTree(t, a)
	require.NoError(t, a.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(2, 4, nil, 0)
		return nil
	}, "remove bc"))

	var textID, parentID *crdt.TreeNodeID
	for _, n := range census(t, a) {
		switch {
		case n.IsText() && n.Value == "bc":
			textID = n.ID()
		case n.Type() == "p":
			parentID = n.ID()
		}
	}
	require.NotNil(t, textID, `the tombstoned "bc" should be nameable before the purge`)
	require.NotNil(t, parentID, "the enclosing <p> should be nameable")

	setup := recordChanges(t, a, "the setup edits")

	// B and C both need the setup collected, so "bc" is PURGED on them too.
	// Otherwise their removal would merely tombstone it and A's restore would
	// take the un-tombstone path instead of the recreate path under test.
	deliverInOrder(t, b, "b applies setup", setup)
	deliverInOrder(t, c, "c applies setup", setup)
	collect(t, a, "a collects setup", vector)
	collect(t, b, "b collects setup", vector)
	collect(t, c, "c collects setup", vector)
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, b))
	require.Equal(t, "<r><p>ad</p></r>", treeXML(t, c))

	removeP := func(doc *document.Document, what string) []*change.Change {
		t.Helper()
		require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetTree("t").Edit(0, 4, nil, 0)
			return nil
		}, "remove p"))

		return recordChanges(t, doc, what)
	}
	// Neither remover has seen the other, so the two removals are concurrent
	// and LWW decides which tombstone survives on every replica.
	removeLow := removeP(b, "b's removal of <p>")
	removeHigh := removeP(c, "c's concurrent removal of <p>")

	require.NoError(t, a.Undo())
	restore := recordChanges(t, a, "a's undo of its own text removal")

	lowAt := firstTicket(t, removeLow, "b's removal")
	highAt := firstTicket(t, removeHigh, "c's removal")
	// The premise of the whole scenario. If the tickets came out the other way
	// round, the "later removal overwrites the parent's tombstone" step never
	// happens and the test would quietly measure nothing.
	require.True(t, highAt.After(lowAt),
		"the fixture needs c's removal to win the LWW race: low=%s high=%s",
		lowAt.ToTestString(), highAt.ToTestString())

	return &ticketFixture{
		setup:      setup,
		removeLow:  removeLow,
		removeHigh: removeHigh,
		restore:    restore,
		lowAt:      lowAt,
		highAt:     highAt,
		textID:     textID,
		parentID:   parentID,
		vector:     vector,
	}
}

// ticketOutcome is what one delivery order landed on.
type ticketOutcome struct {
	label    string
	doc      *document.Document
	before   []*crdt.TreeNode
	restored string
	parent   string
}

func (o ticketOutcome) describe(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("%s: restored=%s parent=%s\n%s",
		o.label, o.restored, o.parent, snapshotTree(t, o.doc))
}

// TestTreeRestoreAgreesOnTheTombstoneTicketAcrossDeliveryOrders is the test
// that decides WHICH TICKET a born-tombstoned recreate should stamp.
//
// The candidate stamps the restored node with the PARENT's removedAt, on the
// reasoning that a replica which recreated the node live and then had the
// removal sweep it lands on the identical tombstone. That reasoning holds for
// ONE removal. It is exactly what LWW breaks for two:
//
//	removeLow, restore, removeHigh -- the restore reads the parent at T_low,
//	                                  then T_high overwrites the parent
//	removeHigh, restore, removeLow -- the restore reads the parent at T_high,
//	                                  and T_low cannot overwrite it
//
// If the later, higher removal does not reach the already-tombstoned restored
// node, those two orders leave it at T_low and T_high respectively: same
// changes, same count, two different collection schedules. The alternative --
// plumbing the restoring operation's own executedAt through Restore, the way
// Retombstone already takes it -- is stable against LWW but has no answer at
// all on the orders where the restore arrives FIRST, because there the node is
// created live and whatever sweeps it stamps a removal ticket instead.
//
// So the test compares the ticket itself, across all six orders, before any
// collection can erase the evidence. The XML is identical in every order here
// ("<r></r>"); if the harness only compared content it would report agreement.
func TestTreeRestoreAgreesOnTheTombstoneTicketAcrossDeliveryOrders(t *testing.T) {
	f := newTicketFixture(t)
	steps := f.steps()

	var outcomes []ticketOutcome
	for i, order := range ticketOrders {
		names := make([]string, 0, len(order))
		for _, idx := range order {
			names = append(names, steps[idx].name)
		}
		label := strings.Join(names, "->")

		doc := f.observer(t, actorTicketObserverBase+i)
		for _, idx := range order {
			deliverInOrder(t, doc, fmt.Sprintf("%s <- %s", label, steps[idx].name), steps[idx].changes)
		}

		outcomes = append(outcomes, ticketOutcome{
			label:    label,
			doc:      doc,
			before:   census(t, doc),
			restored: tombstoneOf(t, doc, f.textID),
			parent:   tombstoneOf(t, doc, f.parentID),
		})
	}

	// The reference is the first order; every other order has to match it.
	// Comparing against one reference rather than pairwise keeps the failure
	// output to one line per divergent order instead of O(n^2) of them.
	// The whole table, logged unconditionally. A -v run of this test is the
	// cheapest way to see what a candidate actually does with the ticket, and
	// the assertions below only report the orders that differ from the first.
	t.Logf("removals: low=%s high=%s", f.lowAt.ToTestString(), f.highAt.ToTestString())
	for _, o := range outcomes {
		t.Logf("  %-34s restored=%-10s parent=%-10s xml=%s",
			o.label, o.restored, o.parent, snapshotTree(t, o.doc).XML)
	}

	ref := outcomes[0]
	for _, o := range outcomes[1:] {
		// The parent first. Its tombstone is pure LWW with no restore
		// involved, so a divergence here would mean the fixture -- not the
		// restore path -- is what is broken, and the next assertion's result
		// could not be trusted.
		assert.Equal(t, ref.parent, o.parent,
			"the PARENT's tombstone ticket diverged between delivery orders; LWW alone should settle it\n%s\n%s",
			ref.describe(t), o.describe(t))

		// The question this file exists to answer.
		assert.Equal(t, ref.restored, o.restored,
			"the RESTORED node's tombstone ticket diverged between delivery orders (low=%s high=%s)\n%s\n%s",
			f.lowAt.ToTestString(), f.highAt.ToTestString(), ref.describe(t), o.describe(t))

		assertConverged(t, ref.label, o.label, ref.doc, o.doc)
	}

	// Collection is where a divergent removedAt turns into a divergent
	// document: canDelete compares the ticket, so two replicas holding
	// different tickets purge on different passes.
	for _, o := range outcomes {
		collect(t, o.doc, o.label+" collects", f.vector)
	}
	for _, o := range outcomes[1:] {
		assert.Equal(t, tombstoneOf(t, ref.doc, f.textID), tombstoneOf(t, o.doc, f.textID),
			"the restored node's fate diverged after collection\n%s\n%s",
			ref.describe(t), o.describe(t))
		assertConverged(t, ref.label+" after collection", o.label+" after collection", ref.doc, o.doc)
	}
	for _, o := range outcomes {
		assertNoOrphans(t, o.doc, o.before, o.label+" after collection")
	}
}

// -----------------------------------------------------------------------------
// 2. Element and attribute spans.
// -----------------------------------------------------------------------------

// buildMarkTree writes <r><p><b bold="true">xy</b></p></r>. The mark carries an
// attribute so the element span has an RHT to deep-copy, which is the part of
// recreateFromSpan's non-text branch nothing else in the harness reaches.
func buildMarkTree(t *testing.T, doc *document.Document) {
	t.Helper()

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{
				{
					Type:       "b",
					Attributes: map[string]string{"bold": "true"},
					Children:   []json.TreeNode{{Type: textNodeType, Value: "xy"}},
				},
			}},
		}})
		return nil
	}))
}

// elementFixture is the element analogue of orderFixture: A removes the whole
// <b> mark (an ELEMENT span plus the text inside it), everyone collects so it
// is purged, B concurrently removes the enclosing <p>, and A undoes.
type elementFixture struct {
	setup      []*change.Change
	remove     []*change.Change
	restore    []*change.Change
	undoRemove []*change.Change

	markID *crdt.TreeNodeID
	textID *crdt.TreeNodeID

	vector time.VersionVector
}

func (f *elementFixture) observer(t *testing.T, n int) *document.Document {
	t.Helper()

	doc := newOrderReplica(t, n)
	deliverInOrder(t, doc, fmt.Sprintf("observer %d applies setup", n), f.setup)
	collect(t, doc, fmt.Sprintf("observer %d collects setup", n), f.vector)
	require.Equal(t, "<r><p></p></r>", treeXML(t, doc))

	return doc
}

func newElementFixture(t *testing.T) *elementFixture {
	t.Helper()

	a := newOrderReplica(t, actorAuthorA)
	b := newOrderReplica(t, actorAuthorB)
	vector := helper.MaxVersionVector(
		a.ActorID(), b.ActorID(),
		newOrderReplica(t, actorElemRemoveFirst).ActorID(),
		newOrderReplica(t, actorElemRestoreFirst).ActorID(),
	)

	buildMarkTree(t, a)
	require.Equal(t, `<r><p><b bold="true">xy</b></p></r>`, treeXML(t, a))

	var markID, textID *crdt.TreeNodeID
	for _, n := range census(t, a) {
		switch {
		case n.Type() == "b":
			markID = n.ID()
		case n.IsText() && n.Value == "xy":
			textID = n.ID()
		}
	}
	require.NotNil(t, markID, "the <b> mark should be nameable before the purge")
	require.NotNil(t, textID, "the text inside <b> should be nameable before the purge")

	// Remove the whole mark: <b> opens at 1 and closes at 5 in <r><p><b>xy</b></p></r>.
	require.NoError(t, a.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(1, 5, nil, 0)
		return nil
	}, "remove the mark"))
	require.Equal(t, "<r><p></p></r>", treeXML(t, a))
	setup := recordChanges(t, a, "the setup edits")

	deliverInOrder(t, b, "b applies setup", setup)
	collect(t, a, "a collects setup", vector)
	collect(t, b, "b collects setup", vector)
	require.Nil(t, nodeByID(t, b, markID), "the removed mark should be purged, not tombstoned")

	require.NoError(t, b.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(0, 2, nil, 0)
		return nil
	}, "remove p"))
	remove := recordChanges(t, b, "b's removal of <p>")

	require.NoError(t, a.Undo())
	restore := recordChanges(t, a, "a's undo of its own mark removal")

	require.NoError(t, b.Undo())
	undoRemove := recordChanges(t, b, "b's undo of its own <p> removal")

	return &elementFixture{
		setup:      setup,
		remove:     remove,
		restore:    restore,
		undoRemove: undoRemove,
		markID:     markID,
		textID:     textID,
		vector:     vector,
	}
}

// TestTreeRestoreConvergesForAnElementSpanUnderATombstonedParent runs the
// existing two-order question through Restore's NON-TEXT branch.
//
// It is not a duplicate of the text scenarios. The element branch rebuilds the
// node from span.ID directly rather than from a computed sub-range, and it
// deep-copies span.Attributes onto the rebuilt node -- so an element span can
// come back with its identity intact and its attributes gone, which no text
// scenario can detect. It also recreates a SUBTREE: the mark and the text
// inside it are two spans in parent-before-child order, so a candidate that
// refuses the mark has to leave the text unplaced too, and a candidate that
// tombstones the mark has to account for both.
//
// The mark's tombstone ticket and its attributes are compared directly, off
// NodeMapByID, because under a removed <p> neither is visible in the XML.
func TestTreeRestoreConvergesForAnElementSpanUnderATombstonedParent(t *testing.T) {
	f := newElementFixture(t)

	removeFirst := f.observer(t, actorElemRemoveFirst)
	deliverInOrder(t, removeFirst, "removeFirst <- remove", f.remove)
	deliverInOrder(t, removeFirst, "removeFirst <- restore", f.restore)

	restoreFirst := f.observer(t, actorElemRestoreFirst)
	deliverInOrder(t, restoreFirst, "restoreFirst <- restore", f.restore)
	deliverInOrder(t, restoreFirst, "restoreFirst <- remove", f.remove)

	// The recreated element, before anything can hide the evidence: same
	// tombstone ticket and same attributes on both replicas, or the two orders
	// have already parted ways.
	assert.Equal(t, tombstoneOf(t, removeFirst, f.markID), tombstoneOf(t, restoreFirst, f.markID),
		"the recreated <b> mark's tombstone ticket diverged between delivery orders\nremoveFirst:\n%s\nrestoreFirst:\n%s",
		snapshotTree(t, removeFirst), snapshotTree(t, restoreFirst))
	assert.Equal(t, attrsOf(t, removeFirst, f.markID), attrsOf(t, restoreFirst, f.markID),
		"the recreated <b> mark's attributes diverged between delivery orders\nremoveFirst:\n%s\nrestoreFirst:\n%s",
		snapshotTree(t, removeFirst), snapshotTree(t, restoreFirst))
	assertConverged(t, "removeFirst", "restoreFirst", removeFirst, restoreFirst)

	// The collection that turns a tombstoned <p> into a detached subtree. An
	// element span strands MORE than a text span does -- the mark and the text
	// under it -- so both have to be named, which is what the census is for.
	beforeRemoveFirst, beforeRestoreFirst := census(t, removeFirst), census(t, restoreFirst)
	collect(t, removeFirst, "removeFirst collects", f.vector)
	collect(t, restoreFirst, "restoreFirst collects", f.vector)

	assertConverged(t, "removeFirst after collection", "restoreFirst after collection",
		removeFirst, restoreFirst)
	assertNoOrphans(t, removeFirst, beforeRemoveFirst, "removeFirst after collection")
	assertNoOrphans(t, restoreFirst, beforeRestoreFirst, "restoreFirst after collection")
}

// TestTreeRestoreConvergesWhenAnElementParentRemovalIsUndone asks the element
// question where the answer is VISIBLE, exactly as
// TestTreeRestoreConvergesWhenTheParentRemovalIsUndone does for text.
//
// B undoes its own removal of <p> last in both orders, which brings the
// paragraph back and puts whatever each replica decided about the mark on
// screen -- including the mark's attributes, which the XML renders. B removed
// only <p> (the mark was already purged on B, so B's restore spans cannot
// mention it), so the mark's fate is decided entirely by what each replica did
// when the concurrent restore arrived.
func TestTreeRestoreConvergesWhenAnElementParentRemovalIsUndone(t *testing.T) {
	f := newElementFixture(t)

	removeFirst := f.observer(t, actorElemRemoveFirst)
	deliverInOrder(t, removeFirst, "removeFirst <- remove", f.remove)
	deliverInOrder(t, removeFirst, "removeFirst <- restore", f.restore)
	deliverInOrder(t, removeFirst, "removeFirst <- undoRemove", f.undoRemove)

	restoreFirst := f.observer(t, actorElemRestoreFirst)
	deliverInOrder(t, restoreFirst, "restoreFirst <- restore", f.restore)
	deliverInOrder(t, restoreFirst, "restoreFirst <- remove", f.remove)
	deliverInOrder(t, restoreFirst, "restoreFirst <- undoRemove", f.undoRemove)

	assert.Equal(t, attrsOf(t, removeFirst, f.markID), attrsOf(t, restoreFirst, f.markID),
		"the recreated <b> mark's attributes diverged between delivery orders\nremoveFirst:\n%s\nrestoreFirst:\n%s",
		snapshotTree(t, removeFirst), snapshotTree(t, restoreFirst))
	assertConverged(t, "removeFirst", "restoreFirst", removeFirst, restoreFirst)

	beforeRemoveFirst, beforeRestoreFirst := census(t, removeFirst), census(t, restoreFirst)
	collect(t, removeFirst, "removeFirst collects", f.vector)
	collect(t, restoreFirst, "restoreFirst collects", f.vector)

	assertConverged(t, "removeFirst after collection", "restoreFirst after collection",
		removeFirst, restoreFirst)
	assertNoOrphans(t, removeFirst, beforeRemoveFirst, "removeFirst after collection")
	assertNoOrphans(t, restoreFirst, beforeRestoreFirst, "restoreFirst after collection")
}

// -----------------------------------------------------------------------------
// 3. Depth: the tombstoned parent is a grandchild of the root.
// -----------------------------------------------------------------------------

// buildDeepTree writes <r><p><b>abcd</b></r>, the shape a real editor produces:
// a mark nested inside a paragraph, with the text two levels down.
func buildDeepTree(t *testing.T, doc *document.Document) {
	t.Helper()

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{
				{Type: "b", Children: []json.TreeNode{{Type: textNodeType, Value: "abcd"}}},
			}},
		}})
		return nil
	}))
}

// TestTreeRestoreOrphansDeeplyNestedNodeUnderTombstonedParent is the control
// test one level deeper, and it is the worse shape.
//
// In the shallow control the purged parent is a child of the root, so the
// stranded node ends up in a subtree hanging off nothing -- an ascent from it
// terminates at a node with no parent, which is at least recognisable as
// broken. Here the tombstoned parent is <b> INSIDE a live <p>: when <b> is
// purged, index.Node.RemoveChild nils <b>'s own parent pointer and leaves the
// restored text pointing at <b>, so an ascent from the restored node reaches
// <b> and stops, while <p> and the root are still alive and still rendering.
// The document looks entirely healthy from the root, and the only witness is
// NodeMapByID, which still answers with the stranded node.
//
//	d1: <r><p><b>abcd</b></p></r>, then remove "bc" (inside <b>)
//	both collect, so "bc" is PURGED everywhere
//	d2: remove <b>; the change reaches d1 while uncollected
//	d1: undo its own "remove bc"
//
// Both replicas are asserted, because the undo is delivered to d2 as well:
// this is the reachability question, not yet the ordering question, so a
// correct system leaves BOTH of them consistent.
func TestTreeRestoreOrphansDeeplyNestedNodeUnderTombstonedParent(t *testing.T) {
	d1 := newOrderReplica(t, actorDeepAuthorA)
	d2 := newOrderReplica(t, actorDeepAuthorB)
	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())

	buildDeepTree(t, d1)

	var textID *crdt.TreeNodeID
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		// <r><p><b>abcd</b></p></r>: "a" starts at 2, so "bc" is [3,5).
		r.GetTree("t").Edit(3, 5, nil, 0)
		return nil
	}, "remove bc"))
	require.Equal(t, "<r><p><b>ad</b></p></r>", treeXML(t, d1))
	for _, n := range census(t, d1) {
		if n.IsText() && n.Value == "bc" {
			textID = n.ID()
		}
	}
	require.NotNil(t, textID, `the tombstoned "bc" should be nameable before the purge`)
	deliverChanges(t, d1, d2)

	collect(t, d1, "d1 first collection", vector)
	collect(t, d2, "d2 first collection", vector)
	require.Nil(t, nodeByID(t, d1, textID), "the removed text should be purged, not tombstoned")

	// d2 removes the enclosing <b> -- not <p>, so a LIVE ancestor survives
	// above the tombstone and the document keeps rendering normally.
	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(1, 5, nil, 0)
		return nil
	}, "remove b"))
	deliverChanges(t, d2, d1)
	require.Equal(t, "<r><p></p></r>", treeXML(t, d1))

	require.NoError(t, d1.Undo())

	afterUndo := snapshotTree(t, d1)
	assert.Equal(t, "<r><p></p></r>", afterUndo.XML,
		"the undo is invisible because it landed under a tombstoned mark\n%s", afterUndo)

	deliverChanges(t, d1, d2)

	beforeCollect1, beforeCollect2 := census(t, d1), census(t, d2)
	collect(t, d1, "d1 second collection", vector)
	collect(t, d2, "d2 second collection", vector)

	assertConverged(t, "d1 (undid the removal)", "d2 (received the undo)", d1, d2)
	assertNoOrphans(t, d1, beforeCollect1, "d1 after the mark was purged")
	assertNoOrphans(t, d2, beforeCollect2, "d2 after the mark was purged")
}

// Baseline, measured at 96dedb46 under THREE variants, because each column
// answers a different question: unpatched main says whether the scenario is
// real at all, the born-tombstoned candidate (patch-born-tombstoned.diff,
// stamping parent.RemovedAt) says whether the leading fix already handles it,
// and the executedAt variant (patch-executed-at.diff, stamping the restoring
// operation's own ticket) is what makes these tests worth writing -- it is the
// alternative the three original scenarios cannot tell apart.
//
//	                                        main   parent.RemovedAt   executedAt
//	...OrphansNodeUnderTombstonedParent      RED         GREEN            GREEN
//	...ConvergesUnderEitherDeliveryOrder     RED         GREEN            GREEN
//	...ConvergesWhenTheParentRemovalIsUndone RED         GREEN            GREEN
//	...AgreesOnTheTombstoneTicket...         RED         GREEN            RED
//	...ForAnElementSpanUnderATombstoned...   RED         GREEN            RED
//	...WhenAnElementParentRemovalIsUndone    RED         GREEN            GREEN
//	...OrphansDeeplyNestedNode...            RED         GREEN            GREEN
//
// The two RED cells in the last column are the whole point: every scenario
// that existed before this file is blind to the ticket choice, and two of the
// scenarios here are not.
//
// TestTreeRestoreAgreesOnTheTombstoneTicketAcrossDeliveryOrders. The two
// removals come out as low=4:1:AC and high=4:1:AF, and the PARENT lands on
// 4:1:AF in all six orders under every variant -- LWW settles the parent by
// itself, so a failure on the parent assertion would mean the fixture broke,
// not the restore path. The restored node is a different story:
//
//	order                             main      parent.RemovedAt   executedAt
//	removeLow->removeHigh->restore    live          4:1:AF           3:1:AB
//	removeLow->restore->removeHigh    4:1:AF        4:1:AF           4:1:AF
//	removeHigh->removeLow->restore    live          4:1:AF           3:1:AB
//	removeHigh->restore->removeLow    4:1:AC        4:1:AF           4:1:AC
//	restore->removeLow->removeHigh    4:1:AF        4:1:AF           4:1:AF
//	restore->removeHigh->removeLow    4:1:AF        4:1:AF           4:1:AF
//
// Main lands on three different answers and orphans on the two orders where
// the restore arrives last (registered=2, reachable=1). The LWW worry that
// prompted this test turns out NOT to bite parent.RemovedAt: the restored node
// participates in ordinary tombstone LWW after the fact, so the later, higher
// removal reaches the already-tombstoned node and overwrites 4:1:AC to 4:1:AF
// -- every order converges on the removal that wins the race, which is the
// same answer the node would have got had it never been purged. executedAt
// does not converge, because on the orders where the restore is delivered LAST
// there is no subsequent removal to correct its lower ticket.
//
// TestTreeRestoreConvergesForAnElementSpanUnderATombstonedParent. On main this
// diverges before any collection (removeFirst keeps <b> and its text LIVE,
// garbageLen=1; restoreFirst has both tombstoned, garbageLen=3) and then
// strands BOTH of them: registered=3, reachable=1, census names 1:3:...:0 and
// 1:4:...:0. An element span orphans a whole subtree where a text span orphans
// one leaf. Under executedAt it fails on the TICKET ALONE -- removeFirst
// stamps 3:1:AB, restoreFirst is swept at 4:1:AC -- while assertConverged
// passes, because treeState records removed as a boolean. That is the single
// clearest argument for comparing the ticket directly: with only the existing
// instruments this variant reads as green.
//
// TestTreeRestoreConvergesWhenAnElementParentRemovalIsUndone. On main this is
// a user-visible content divergence: removeFirst renders
// <r><p><b bold="true">xy</b></p></r> and restoreFirst renders <r><p></p></r>.
// The attributes survive the recreate on the side that keeps the mark, so the
// element branch's RHT deep copy is not itself broken -- what diverges is
// whether the mark exists. Under parent.RemovedAt both orders render
// <r><p></p></r> with the mark tombstoned at 4:1:AC and its attributes intact,
// then purge cleanly to reachable=registered=2. Worth stating plainly: the
// candidate converges by DISCARDING the restored content, consistently.
//
// TestTreeRestoreOrphansDeeplyNestedNodeUnderTombstonedParent. Confirms the
// claim it was written for: on main BOTH replicas end at registered=3,
// reachable=2 with 1:4:...:1 (removed=false) named by the census, while the
// XML reads <r><p></p></r> -- a perfectly healthy-looking document with a live
// registered node hanging off a purged <b> underneath a live <p>. This is the
// worse shape: an ascent from the stranded node terminates at <b> rather than
// at a parentless root, so nothing about the walk looks wrong.
