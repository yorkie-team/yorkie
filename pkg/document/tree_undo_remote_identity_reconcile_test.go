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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// riStep is one step of a remote identity reconcile scenario. Replica 0 is
// the peer whose identity-preserving undo/redo (restore or retombstone) goes
// remote; replica 1 holds the stacked undo/redo entry that has to be moved
// by it.
type riStep struct {
	kind    string // update, undo, sync, check
	replica int
	edits   []riOp
	orders  [][]int
	label   string
	want    []string
}

// riOp is one tree operation inside an update.
type riOp struct {
	from, to   int
	content    *json.TreeNode
	splitLevel int
}

func riText(value string) json.TreeNode {
	return json.TreeNode{Type: "text", Value: value}
}

func riElem(typ string, children ...json.TreeNode) json.TreeNode {
	return json.TreeNode{Type: typ, Children: children}
}

func riInsert(from, to int, node json.TreeNode) riOp {
	return riOp{from: from, to: to, content: &node}
}

func riDelete(from, to int) riOp { return riOp{from: from, to: to} }

func riSplit(at, level int) riOp { return riOp{from: at, to: at, splitLevel: level} }

// riUpdate runs the given operations on one replica as a single change.
func riUpdate(replica int, ops ...riOp) riStep {
	return riStep{kind: "update", replica: replica, edits: ops}
}

func riUndo(replica int) riStep { return riStep{kind: "undo", replica: replica} }

// riSync exchanges pending changes; with no orders every replica receives
// every other replica's changes in index order.
func riSync(orders ...[]int) riStep { return riStep{kind: "sync", orders: orders} }

// riCheck asserts each replica's XML at this point.
func riCheck(label string, want ...string) riStep {
	return riStep{kind: "check", label: label, want: want}
}

var seedTwoParagraphs = riElem("doc",
	riElem("p", riText("abcd")),
	riElem("p", riText("efgh")),
)

// TestTreeUndoRemoteIdentityReconcile checks that a peer's identity-preserving
// Tree undo/redo (restore or retombstone by node identity) moves the stacked,
// index-addressed undo/redo entries of the replica that receives it, the way a
// plain remote edit does (yorkie#2222). Every expected string was produced by
// running the same scenario on the JS SDK (790422ce, #1421 reports the ranges
// such an op executed), so a failure here is a cross-SDK divergence.
//
// The scenarios the conformance corpus holds to every assertion moved there
// (test/conformance/tree/corpus/ri-*.json). These stay because their corpus
// scripts do not pass in Go: five fail only in the corpus's minvv GC mode
// (known-error, known-fail), which hides their off-mode checks, and the
// jsDiverges one diverges.
func TestTreeUndoRemoteIdentityReconcile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replicas int
		seed     json.TreeNode
		steps    []riStep
		// wantFinal is each replica's XML after a final exchange.
		wantFinal []string
		// jsDiverges marks a scenario whose replicas do not converge on the
		// JS SDK either; its final state is a JS defect, so it is reported
		// rather than asserted.
		jsDiverges bool
	}{
		{
			// On both SDKs the retombstone misses the half the concurrent
			// Enter split off, so the replicas differ at "synced" until B's
			// undo merges it back.
			name: "retombstone element split by pending enter",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riInsert(6, 6, riElem("p", riText("XY")))),
				riSync(),
				riUpdate(1, riSplit(8, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>abcd</p><p>efgh</p></doc>",
					"<doc><p>abcd</p><p></p><p>efgh</p></doc>"),
				riUndo(1),
				riCheck("after undo",
					"<doc><p>abcd</p><p>efgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>abcd</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
		},
		{
			name: "restore text at pending enter",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riDelete(2, 4)),
				riSync(),
				riUpdate(1, riSplit(2, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>a</p><p>bcd</p><p>efgh</p></doc>",
					"<doc><p>a</p><p>bcd</p><p>efgh</p></doc>"),
				riUndo(1),
				riCheck("after undo",
					"<doc><p>a</p><p>bcd</p><p>efgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>abcd</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
		},
		{
			name: "restore element right of pending enter",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riDelete(6, 12)),
				riSync(),
				riUpdate(1, riSplit(3, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>ab</p><p>cd</p><p>efgh</p></doc>",
					"<doc><p>ab</p><p>cd</p><p>efgh</p></doc>"),
				riUndo(1),
				riCheck("after undo",
					"<doc><p>ab</p><p>cd</p><p>efgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>abcd</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
		},
		{
			name: "restore two separate text ranges around pending enter",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riDelete(10, 11), riDelete(2, 3)),
				riSync(),
				riUpdate(1, riSplit(7, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>"),
				riUndo(1),
				riCheck("after undo",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>abcd</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
		},
		{
			name: "restore element and text in one change around pending enter",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riDelete(0, 6), riDelete(2, 4)),
				riSync(),
				riUpdate(1, riSplit(2, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>"),
				riUndo(1),
				riCheck("after undo",
					"<doc><p>abcd</p><p>e</p><p>fgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>abcd</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
		},
		{
			// JS defect, mirrored by Go: B's undo of its paragraph delete
			// revives only the nodes B removed; the "bc" A restored under that
			// paragraph stays removed on A, so the replicas end at "ad" and
			// "abcd". The intermediate checks are JS's output too; a fix for
			// that defect may change them.
			name: "restore under a concurrently removed ancestor",
			seed: seedTwoParagraphs,
			steps: []riStep{
				riUpdate(0, riDelete(2, 4)),
				riSync(),
				riUpdate(1, riDelete(0, 4)),
				riUpdate(1, riSplit(2, 1)),
				riUndo(0),
				riSync(),
				riCheck("synced",
					"<doc><p>e</p><p>fgh</p></doc>",
					"<doc><p>e</p><p>fgh</p></doc>"),
				riUndo(1),
				riCheck("after first undo",
					"<doc><p>e</p><p>fgh</p></doc>",
					"<doc><p>efgh</p></doc>"),
				riUndo(1),
				riCheck("after second undo",
					"<doc><p>e</p><p>fgh</p></doc>",
					"<doc><p>abcd</p><p>efgh</p></doc>"),
			},
			wantFinal: []string{
				"<doc><p>ad</p><p>efgh</p></doc>",
				"<doc><p>abcd</p><p>efgh</p></doc>",
			},
			jsDiverges: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.replicas
			if n == 0 {
				n = 2
			}
			docs := make([]*document.Document, n)
			for i := range docs {
				actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
				require.NoError(t, err)
				docs[i] = document.New("test-doc")
				docs[i].SetActor(actor)
			}
			require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", tc.seed)
				return nil
			}))
			seedOrders := make([][]int, n)
			for i := 1; i < n; i++ {
				seedOrders[i] = []int{0}
			}
			exchangeInOrder(t, docs, seedOrders)
			for _, doc := range docs {
				require.NoError(t, doc.ClearHistory())
			}

			allOrders := func() [][]int {
				orders := make([][]int, n)
				for i := range orders {
					for j := range n {
						if j != i {
							orders[i] = append(orders[i], j)
						}
					}
				}
				return orders
			}
			xmls := func() []string {
				out := make([]string, n)
				for i, doc := range docs {
					out[i] = treeXML(t, doc)
				}
				return out
			}

			for i, step := range tc.steps {
				switch step.kind {
				case "update":
					require.NoError(t, docs[step.replica].Update(func(root *json.Object, p *presence.Presence) error {
						tree := root.GetTree("t")
						for _, op := range step.edits {
							tree.Edit(op.from, op.to, op.content, op.splitLevel)
						}
						return nil
					}), "step %d", i)
				case "undo":
					require.NoError(t, docs[step.replica].Undo(), "step %d", i)
				case "sync":
					orders := step.orders
					if len(orders) == 0 {
						orders = allOrders()
					}
					exchangeInOrder(t, docs, orders)
				case "check":
					assert.Equal(t, step.want, xmls(), "check %q", step.label)
				}
			}

			exchangeInOrder(t, docs, allOrders())
			if tc.jsDiverges {
				t.Logf("JS diverges here too; JS final %q, Go final %q", tc.wantFinal, xmls())
				return
			}
			assert.Equal(t, tc.wantFinal, xmls(), "final")
			for i := 1; i < n; i++ {
				assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[i]), "replica %d shape", i)
			}
		})
	}
}
