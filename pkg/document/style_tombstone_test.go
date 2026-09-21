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
	"sort"
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
// the node is still live there -- and can never be retracted afterwards. So
// either every replica applies it or the replicas hold different attributes
// on the same node forever. That is invisible while the node is a tombstone
// and rendered the moment the removal is undone.
//
// The contract these tests pin:
//
//   - a removal the styling change had already SEEN wins, so a user never
//     styles text they already deleted (a local change has seen every removal
//     in its own replica, which is the whole of the local case);
//   - a removal CONCURRENT with the style does not, so the style lands on the
//     tombstone everywhere.
//
// Deciding the second case on editedAt.After(removedAt) instead -- what the
// server did -- made it turn on an actor-ID tie-break, so the same two
// operations converged or diverged depending only on who issued them.

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
		sort.Strings(attrs)

		out = append(out, fmt.Sprintf("%q%s %v", node.Value().Value(), state, attrs))
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
// range that spans the node step 3 deleted; the style must leave it alone,
// so step 6 brings the text back carrying the attribute step 2 gave it.
//
// The server used to style the dead node here -- its removal is known, but
// the style's ticket is later, which was the whole of the old test -- and
// the "OLD" value was lost.
func TestLocalStyleSkipsANodeItAlreadyDeleted(t *testing.T) {
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

	require.NoError(t, doc.Undo())
	require.Equal(t, `[{"val":"abcd"},{"val":"ghij"}]`, doc.Root().GetText("t").Marshal())

	require.NoError(t, doc.Undo())
	require.Equal(t,
		`[{"val":"abcd"},{"attrs":{"b":"OLD"},"val":"ef"},{"val":"ghij"}]`,
		doc.Root().GetText("t").Marshal(),
		"the restored run kept the attribute it was carrying when it was deleted")
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
	assertLedgerExact(t, d1, "on the replica that removed the node", a1, a2)
	assertLedgerExact(t, d2, "on the replica that issued the style", a1, a2)
}
