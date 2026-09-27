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
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// canStyle decides whether a style may land on a node that has been removed.
// The answer it gives is a convergence decision, not a rendering preference,
// because a style is applied unconditionally on the replica that issues it --
// the node is still live there, or the range would not have reached it -- and
// can never be retracted afterwards. So either every replica applies it or
// the replicas hold different attributes on the same node forever. That is
// invisible while the node is a tombstone and rendered the moment the removal
// is undone.
//
// THE CONTRACT THESE TESTS PIN: a style applies to every node the styling
// change knew about, and does not ask whether that node has since been
// removed. canStyle reads createdAt and the change's vector, nothing else.
//
// Two narrower rules were tried and measured, and both diverge:
//
//   - editedAt.After(removedAt), what the server used to do, turns the
//     concurrent case on an actor-ID tie-break;
//   - "skip a removal the change had already seen" looks right on a
//     single-actor history -- it is what keeps an undo from losing the
//     formatting a deleted run carried -- but removedAt is last-writer-wins
//     and MUTABLE, so two clients deleting the same run concurrently make
//     the answer depend on which removal landed first. See
//     TestTwoConcurrentRemovalsThenStyle.
//
// The cost of reading nothing is that a style covers text the same client
// already deleted, so undoing the style and then the deletion brings the text
// back without the attributes it carried. That is deliberate; see
// TestLocalStyleLandsOnANodeItAlreadyDeleted.

// nodeAttrs dumps every node of the text under key, live and tombstoned, with
// its attributes. Two replicas are compared on this rather than on rendered
// content because the whole disagreement is invisible in the rendering until
// something revives the tombstone.
func nodeAttrs(t *testing.T, doc *document.Document, key string) []string {
	t.Helper()

	text, ok := doc.RootObject().Get(key).(*crdt.Text)
	require.True(t, ok, "%q should be a Text", key)

	var out []string
	for _, node := range text.Nodes() {
		state := ""
		if node.RemovedAt() != nil {
			state = " (removed)"
		}

		var attrs []string
		for _, attr := range node.Value().Attrs().Nodes() {
			mark := ""
			if attr.RemovedAt() != nil {
				mark = "*"
			}
			attrs = append(attrs, fmt.Sprintf("%s=%s%s", attr.Key(), attr.Value(), mark))
		}
		slices.Sort(attrs)

		out = append(out, fmt.Sprintf("%q%s %v", node.Value().Value(), state, attrs))
	}
	return out
}

// treeNodeAttrs is nodeAttrs for a tree: every node, live and tombstoned, with
// its attributes. ToXML renders live nodes only, so it cannot see a style that
// landed on a tombstone -- which is the whole thing under test.
func treeNodeAttrs(t *testing.T, doc *document.Document, key string) []string {
	t.Helper()

	tree, ok := doc.RootObject().Get(key).(*crdt.Tree)
	require.True(t, ok, "%q should be a Tree", key)

	var out []string
	for _, node := range tree.Nodes() {
		state := ""
		if node.IsRemoved() {
			state = " (removed)"
		}

		var attrs []string
		if node.Attrs != nil {
			for _, attr := range node.Attrs.Nodes() {
				mark := ""
				if attr.RemovedAt() != nil {
					mark = "*"
				}
				attrs = append(attrs, fmt.Sprintf("%s=%s%s", attr.Key(), attr.Value(), mark))
			}
		}
		slices.Sort(attrs)

		out = append(out, fmt.Sprintf("%s%s %v", node.Type(), state, attrs))
	}
	return out
}

// assertLedgerExact pins both halves of docSize against a rebuild of the same
// content, then collects and pins that nothing is left over. docSize.Live and
// docSize.GC are running accumulators that cannot detect their own drift; a
// rebuild recomputes them from the content, and collection is what turns a GC
// charge that no longer matches its node into a visible residue.
func assertLedgerExact(t *testing.T, doc *document.Document, msg string, actors ...time.ActorID) {
	t.Helper()

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live, "%s: Live", msg)
	require.Equal(t, clone.DocSize().GC, doc.DocSize().GC, "%s: GC", msg)

	doc.GarbageCollect(helper.MaxVersionVector(actors...))
	require.Equal(t, 0, doc.GarbageLen(), "%s: garbage left behind", msg)
	require.Equal(t, resourceSize{}, resourceSize{
		Data: doc.DocSize().GC.Data, Meta: doc.DocSize().GC.Meta,
	}, "%s: collection left GC residue", msg)
}

// The six operations from the issue, single actor, no sync. Step 4 styles a
// range that spans the node step 3 deleted, and it lands there -- so step 5's
// undo strips it, and step 6 brings "ef" back WITHOUT the attribute step 2
// gave it.
//
// This is the cost of canStyle not reading removedAt, and it is deliberate.
// Skipping a removal the change already knew about reads better here -- "ef"
// would come back carrying b="OLD" -- but it makes the predicate depend on a
// field that Remove overwrites, and two clients deleting the same run
// concurrently then leave replicas disagreeing for good. See
// TestTwoConcurrentRemovalsThenStyle for the case that forces the choice.
//
// The local path now reaches the tombstone, so it also exercises the GC
// routing that used to be reachable only through a concurrent removal.
func TestLocalStyleLandsOnANodeItAlreadyDeleted(t *testing.T) {
	doc := document.New("d1")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(4, 6, map[string]string{"b": "OLD"})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 8, map[string]string{"b": "NEW"})
		return nil
	}))
	require.Equal(t,
		`[{"attrs":{"b":"NEW"},"val":"abcd"},{"attrs":{"b":"NEW"},"val":"ghij"}]`,
		doc.Root().GetText("t").Marshal())
	require.Equal(t,
		[]string{`"abcd" [b=NEW]`, `"ef" (removed) [b=NEW]`, `"ghij" [b=NEW]`},
		nodeAttrs(t, doc, "t"),
		"the dead node took the style too; RHT.Set drops the value it held")

	require.NoError(t, doc.Undo())
	require.Equal(t, `[{"val":"abcd"},{"val":"ghij"}]`, doc.Root().GetText("t").Marshal())

	require.NoError(t, doc.Undo())
	require.Equal(t,
		`[{"val":"abcd"},{"val":"ef"},{"val":"ghij"}]`,
		doc.Root().GetText("t").Marshal(),
		"the restored run lost the attribute it was carrying: the cost of the contract")

	assertLedgerExact(t, doc, "after the local style reached a tombstone", doc.ActorID())
}

// A style concurrent with a removal, on both ticket orderings. The only
// difference between the two cases is which actor's ticket sorts higher,
// which must not decide whether the replicas agree.
//
// The exchange goes over the protobuf converter, not the in-process
// crossSync: change.ID shares its VersionVector map with the document's
// changeID, so handing the receiver the sender's live change objects lets
// SyncClocks mutate the delivered vector, and a causality check then reads
// the sender's post-sync state instead of what it knew when it edited.
func TestConcurrentStyleLandsOnTheTombstoneEverywhere(t *testing.T) {
	for _, tc := range []struct {
		name      string
		styleOnD1 bool
	}{
		{"style loses the ticket race", true},
		{"style wins the ticket race", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d1, d2, a1, a2 := newReplicas(t)

			require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("t").Edit(0, 0, "abcdefghij")
				return nil
			}))
			wireSync(t, d1, d2)

			styler, deleter := d1, d2
			if !tc.styleOnD1 {
				styler, deleter = d2, d1
			}
			require.NoError(t, styler.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("t").Style(4, 6, map[string]string{"b": "1"})
				return nil
			}))
			require.NoError(t, deleter.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("t").Edit(4, 6, "")
				return nil
			}))
			wireSync(t, d1, d2)

			require.Equal(t,
				[]string{`"abcd" []`, `"ef" (removed) [b=1]`, `"ghij" []`},
				nodeAttrs(t, d1, "t"))
			require.Equal(t, nodeAttrs(t, d1, "t"), nodeAttrs(t, d2, "t"),
				"the replicas disagree on the tombstoned node's attributes")

			// The style grew a node whose GC charge was taken when it was
			// removed. Without moving those bytes through GC, the replica
			// that received the style reports a different size for the same
			// document than the one that issued it, and collection then
			// subtracts more than registration added.
			assertLedgerExact(t, d1, "on d1", a1, a2)
			assertLedgerExact(t, d2, "on d2", a1, a2)
		})
	}
}

// The same, but the tombstoned node already holds the key being written and
// the incoming value is much shorter. The write has to debit the superseded
// value as well as credit the installed one, and both halves have to land in
// GC: booking either to Live walks it down by the signed difference between
// the two sizes, which is how this first went negative (#2007).
func TestConcurrentStyleShrinkingAnAttributeOnATombstone(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(4, 6, map[string]string{"b": strings.Repeat("L", 20)})
		return nil
	}))
	wireSync(t, d1, d2)

	// A much shorter value over the same key, on a range that covers the
	// node the other replica is deleting.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 8, map[string]string{"b": "x"})
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}))
	wireSync(t, d1, d2)

	// The style's range ends inside "ghij", which the boundary split cuts.
	require.Equal(t,
		[]string{`"abcd" [b=x]`, `"ef" (removed) [b=x]`, `"gh" [b=x]`, `"ij" []`},
		nodeAttrs(t, d1, "t"))
	require.Equal(t, nodeAttrs(t, d1, "t"), nodeAttrs(t, d2, "t"))
	require.GreaterOrEqual(t, d1.DocSize().Live.Data, 0, "Live went negative")
	assertLedgerExact(t, d1, "on the replica that deleted the node", a1, a2)
	assertLedgerExact(t, d2, "on the replica that issued the style", a1, a2)
}

// A tombstoned node can also have an attribute REVIVED on it: a remote
// removeStyle tombstones the key, a later remote style sets it again. The
// pair the first one registered carried zero -- the attribute's bytes were
// still inside the node's charge at that point -- but the revive replaces it
// with a live node, so the node's charge no longer covers it and the map
// entry has to give back its own size on the way out.
//
// A text removeStyle only reaches a tombstone as the reverse of a style, so
// the sequence is: style, undo, style again, all concurrent with the removal.
func TestReviveAnAttributeOnATombstone(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(4, 6, map[string]string{"b": strings.Repeat("L", 12)})
		return nil
	}))
	wireSync(t, d1, d2)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 8, map[string]string{"b": "x"})
		return nil
	}))
	require.NoError(t, d2.Undo())
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 8, map[string]string{"b": "yy"})
		return nil
	}))

	wireSync(t, d1, d2)

	require.Equal(t, nodeAttrs(t, d1, "t"), nodeAttrs(t, d2, "t"))
	assertLedgerExact(t, d1, "on the replica that deleted the node", a1, a2)
	assertLedgerExact(t, d2, "on the replica that issued the styles", a1, a2)
}

// The tree half of the same contract. Reaching it needs a remote style,
// because an index range cannot address a removed node locally: a style whose
// range was decided before a concurrent split follows InsNextID to the split
// siblings, and one of those is removed by the time the style arrives.
func TestRemoteStyleOnARemovedTreeNodeKeepsTheLedgerExact(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcdefgh"}}},
		}})
		return nil
	}))
	wireSync(t, d1, d2)

	// d2 styles a range decided before d1 splits.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 10, map[string]string{"b": "LONGLONGLONGLONG"})
		return nil
	}))

	// d1 splits the paragraph and removes the right half.
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(5, 5, nil, 1)
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(6, 11, nil, 0)
		return nil
	}))

	wireSync(t, d1, d2)

	require.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
	require.Equal(t, treeNodeAttrs(t, d1, "t"), treeNodeAttrs(t, d2, "t"),
		"the replicas disagree on the tombstoned node's attributes")
	assertLedgerExact(t, d1, "on the replica that removed the node", a1, a2)
	assertLedgerExact(t, d2, "on the replica that issued the style", a1, a2)
}

// The tree's RemoveStyle half of the same question. A remote removeStyle whose
// range was decided before a concurrent split follows InsNextID to the split
// siblings, one of which is a tombstone by the time it arrives. A live
// attribute on a tombstoned node is not in Live -- Tree.DataSize excludes the
// node -- so booking it out of Live walks Live down by the attribute's size,
// without bound and into the negative, and GC ends up over a rebuild by the
// same amount.
func TestRemoteRemoveStyleOnARemovedTreeNodeKeepsTheLedgerExact(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcdefgh"}}},
		}})
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 10, map[string]string{"b": strings.Repeat("L", 16)})
		return nil
	}))
	wireSync(t, d1, d2)

	// d2 removes the style over a range decided before d1 splits.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").RemoveStyle(0, 10, []string{"b"})
		return nil
	}))

	// d1 splits the paragraph and removes the right half.
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(5, 5, nil, 1)
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(6, 11, nil, 0)
		return nil
	}))

	wireSync(t, d1, d2)

	require.Equal(t, treeNodeAttrs(t, d1, "t"), treeNodeAttrs(t, d2, "t"),
		"the replicas disagree on the tombstoned node's attributes")
	require.GreaterOrEqual(t, d1.DocSize().Live.Data, 0, "Live went negative")
	assertLedgerExact(t, d1, "on the replica that removed the node", a1, a2)
	assertLedgerExact(t, d2, "on the replica that issued the removeStyle", a1, a2)
}

// Toggling a tree attribute on and off has to return the ledger to where it
// started, on the CLONE as well as on the root. json.Tree's RemoveStyle used
// to register its GC pairs without moving the same bytes out of Live -- that
// was a second call the caller had to remember, and this one did not -- so
// the clone's Live kept every attribute a removeStyle had tombstoned.
// Document.Update reads the clone's total against MaxSizeLimit, so a
// rich-text editor toggling one key walked into ErrDocumentSizeExceedsLimit
// on a document nowhere near the limit.
//
// The root was spared because the operation path did move them, which is why
// DocSize() alone cannot see this. RegisterGCPair now does both halves, so
// neither path can forget.
func TestTogglingATreeAttributeDoesNotDriftTheCloneLedger(t *testing.T) {
	doc := document.New("d")
	doc.MaxSizeLimit = 2000
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcd"}}},
		}})
		return nil
	}))

	val := strings.Repeat("v", 200)
	for i := range 40 {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(0, 6, map[string]string{"b": val})
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(0, 6, []string{"b"})
			return nil
		}), "toggle %d tripped MaxSizeLimit; the document itself is %+v", i, doc.DocSize())
	}
}

// Restoring a PURGED tree node recreates it from the span, whose attributes
// are a deep copy of the original RHT -- tombstones included, because a
// recreated node has to resolve a concurrent style the way a replica that
// never lost it would. Each copied tombstone is a fresh piece of garbage no
// removal path produced, and TreeNode.DataSize excludes it, so without a
// registration it sits in the RHT forever: uncounted and unpurgeable.
//
// Pre-existing; surfaced while giving the recreate path its GCOnlySize.
func TestRecreateCarriesAttributeTombstones(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		}})
		return nil
	}))
	// Style then undo it -> the reverse is a removeStyle, which tombstones
	// the attribute on <p>.
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 4, map[string]string{"bbbb": "vvvv"})
		return nil
	}))
	require.NoError(t, doc.Undo())
	t.Logf("after style+undo  gcLen=%d size=%+v", doc.GarbageLen(), doc.DocSize())

	// Remove the <p>, purge it, then undo the removal so it is RECREATED.
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))
	doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
	t.Logf("after remove+gc   gcLen=%d size=%+v", doc.GarbageLen(), doc.DocSize())

	require.NoError(t, doc.Undo())
	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	t.Logf("after undo        gcLen=%d size=%+v", doc.GarbageLen(), doc.DocSize())
	t.Logf("rebuilt           gcLen=%d size=%+v", clone.GarbageLen(), clone.DocSize())
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live, "Live")
	require.Equal(t, clone.DocSize().GC, doc.DocSize().GC, "GC")
	require.Equal(t, clone.GarbageLen(), doc.GarbageLen(), "GarbageLen")
}

// A style range that opens on a tombstone: the reverse operation's prior
// values must come from the first LIVE node, not from the dead run the user
// had already deleted. Capturing from the tombstone made the undo write
// b="OLD" onto "efgh", which never carried the attribute at any point.
func TestUndoDoesNotRestoreATombstonesAttribute(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 4, map[string]string{"b": "OLD"})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(0, 4, "")
		return nil
	}))
	require.Equal(t, `[{"val":"efghij"}]`, doc.Root().GetText("t").Marshal())

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 4, map[string]string{"b": "NEW"})
		return nil
	}))
	require.NoError(t, doc.Undo())
	require.Equal(t,
		`[{"val":"efgh"},{"val":"ij"}]`,
		doc.Root().GetText("t").Marshal(),
		"the undo restored an attribute the visible text never carried")
}

// A rebuilt root's Live must not depend on how many dead position nodes the
// array carries: Array.DataSize does not count them, so Array.GCPairs has to
// mark each pair GCOnlySize -- "add to GC, take nothing out of Live". Without
// it RegisterGCPair debits Live for bytes it never held, once per dead node.
//
// Moving the SAME element repeatedly keeps movedAt at a fixed count while the
// dead-node count grows, which is what isolates the two. The running ledger now
// carries movedAt as well (yorkie#2017), so it is asserted against the rebuild
// here rather than one rebuild against another.
//
// The local move path in json.Array marks its pairs the same way. It is not
// asserted here: it books to the CLONE, whose ledger has no observable except
// MaxSizeLimit, and the error direction under-reports size so the limit stays
// permissive. It is correct by symmetry with operations.Move, which this and
// the barrier tests do cover.
func TestArrayDeadPositionNodesAreGCOnly(t *testing.T) {
	rebuiltLive := func(moves int) (resourceSize, int) {
		doc := document.New("d")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("a").AddString("one").AddString("two").AddString("three")
			return nil
		}))
		for i := range moves {
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				a := root.GetArray("a")
				id := a.Get(0).CreatedAt()
				if i%2 == 0 {
					a.MoveLast(id)
				} else {
					a.MoveFront(id)
				}
				return nil
			}))
		}

		clone, err := doc.InternalDocument().DeepCopy()
		require.NoError(t, err)
		require.Equal(t, moves, doc.GarbageLen(), "each move leaves one dead node")
		require.Equal(t, clone.DocSize().Live, doc.DocSize().Live,
			"the running ledger has to hold what the rebuild recomputes")
		return resourceSize{
			Data: clone.DocSize().Live.Data,
			Meta: clone.DocSize().Live.Meta,
		}, doc.GarbageLen()
	}

	// Two and three moves of the same element set movedAt on the same
	// elements; only the dead-node count differs.
	two, twoLen := rebuiltLive(2)
	three, threeLen := rebuiltLive(3)
	require.NotEqual(t, twoLen, threeLen, "sanity: the dead-node count differs")
	require.Equal(t, two, three,
		"a rebuilt Live changed with the number of dead position nodes")
}
