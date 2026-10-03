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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// §7.8's marker counts tombstones, and GC unlinks them on its own schedule, so
// these run the same-boundary splits with one replica having collected a
// tombstone the other still holds. The design doc (§7.8) argues why a purge
// cannot make an edit set diverge that converged without the marker; these pin
// the shapes the two replicas have to agree on.
func TestTreeSameBoundarySplitAfterGC(t *testing.T) {
	t.Run("converges when one replica collects while a split is in flight", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "span",
						Children: []json.TreeNode{{Type: "text", Value: "abcde"}},
					}},
				}},
			})
			return nil
		}))
		crossSync(t, d1, d2)

		// d2 removes the last character. Delivering it one way and acking at
		// d2 leaves the removal covered by the element-wise min of the two
		// vectors, which is what a server would compute.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 5}, nil, 0)
			return nil
		}))
		oneWayDeliver(t, d2, d1)
		minVV := time.MinVersionVector(d1.VersionVector(), d2.VersionVector())

		// d1 splits the span, which moves the tombstone into a product d2 has
		// not seen, and d2 splits the same boundary concurrently.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))

		require.Positive(t, d1.GarbageCollect(minVV), "d1 should collect the removed character")

		crossSync(t, d1, d2)
		crossSync(t, d1, d2)

		assert.Equal(t, `<doc><p><span>abc</span><span></span><span>d</span></p></doc>`,
			d1.Root().GetTree("t").ToXML())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
		assert.Equal(t, liveTreeShape(t, d1), liveTreeShape(t, d2))

		for _, doc := range []*document.Document{d1, d2} {
			doc.GarbageCollect(helper.MaxVersionVector(a1, a2))
			assert.Equal(t, 0, doc.GarbageLen(), "tree garbage must drain on both replicas")
		}
		assert.Equal(t, treeShape(t, d1), treeShape(t, d2))
	})

	// Two same-boundary splits and a follow-up split of the newer actor's
	// product (the shape of yorkie-js-sdk#1433), at a boundary with a removed
	// character behind it, which either replica -- or neither -- collected
	// before the splits.
	for _, collector := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("a follow-up split over a removed tail, collected by replica %d", collector),
			func(t *testing.T) {
				docs := paragraphReplicas(t, 2, "abc")
				applySplitSteps(t, docs, []splitStep{{replica: 0, index: 3, to: 4}})
				exchangeInOrder(t, docs, [][]int{{1}, {0}})
				if collector >= 0 {
					minVV := time.MinVersionVector(docs[0].VersionVector(), docs[1].VersionVector())
					require.Positive(t, docs[collector].GarbageCollect(minVV))
				}

				applySplitSteps(t, docs, []splitStep{
					{replica: 0, index: 3}, {replica: 1, index: 3}, {replica: 1, index: 5},
				})
				exchangeInOrder(t, docs, [][]int{{1}, {0}})

				assert.Equal(t, "<doc><p>ab</p><p></p><p></p><p></p></doc>", docs[0].Root().GetTree("t").ToXML())
				assert.Equal(t, docs[0].Root().GetTree("t").ToXML(), docs[1].Root().GetTree("t").ToXML())
				assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[1]))
			})
	}
}

// minVVServer replays what packs.PushPull does for GC, in-process: a sync
// pushes the client's pending changes, records the vector it pushed them with,
// takes the min over every client's recorded vector, hands the client every
// change it has not seen, and the client collects with that min only after
// applying them (InternalDocument.ApplyChangePack). This is the ordering
// holdsKnownChild's GC argument rests on, so the test below drives it rather
// than calling GarbageCollect with a hand-picked vector.
type minVVServer struct {
	docs     []*document.Document
	log      []*change.Change
	author   []int
	cursor   []int
	recorded []time.VersionVector
}

func newMinVVServer(docs []*document.Document) *minVVServer {
	return &minVVServer{
		docs:     docs,
		cursor:   make([]int, len(docs)),
		recorded: make([]time.VersionVector, len(docs)),
	}
}

// sync runs one push-pull for docs[i] and returns how many nodes it collected.
func (s *minVVServer) sync(t *testing.T, i int) int {
	t.Helper()

	pb, err := converter.ToChangePack(s.docs[i].CreateChangePack())
	require.NoError(t, err)
	push, err := converter.FromChangePack(pb)
	require.NoError(t, err)

	var lastSeq uint32
	for _, c := range push.Changes {
		s.log = append(s.log, c)
		s.author = append(s.author, i)
		lastSeq = c.ClientSeq()
	}
	s.recorded[i] = push.VersionVector.DeepCopy()
	minVV := time.MinVersionVector(s.recorded...)

	var pull []*change.Change
	for ; s.cursor[i] < len(s.log); s.cursor[i]++ {
		if s.author[s.cursor[i]] != i {
			pull = append(pull, s.log[s.cursor[i]])
		}
	}
	pb, err = converter.ToChangePack(change.NewPack(push.DocumentKey, change.NewCheckpoint(0, lastSeq), pull, nil, nil))
	require.NoError(t, err)
	reply, err := converter.FromChangePack(pb)
	require.NoError(t, err)
	require.NoError(t, s.docs[i].ApplyChangePack(reply))

	return s.docs[i].GarbageCollect(minVV)
}

// Known limitation, tracked in yorkie#2099. A removal the incoming split's
// editor had seen can be collected before that split arrives. §7.8's marker
// (holdsKnownChild) counts tombstones, so the replica that collected reads the
// right half as gone and places the split differently from the others. This
// drives that schedule through the server's min-vector ordering and pins what
// happens today: the visible document matches everywhere, but the collector's
// empty paragraphs sit in a different order. §7.4, the §7.5 advance and the
// §7.8 entry gate on main read tombstones the same way. When #2099 is fixed,
// this test should assert that every replica converges.
func TestTreeSameBoundarySplitUnderServerGC(t *testing.T) {
	// d1 removes "d" and then splits after "c"; d2, which has not seen either,
	// splits "abc|d" and splits again after its "d". d3 collects as soon as the
	// min covers the removal; d4 only pulls once everything is in.
	docs := make([]*document.Document, 4)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		docs[i] = document.New("test-doc")
		docs[i].SetActor(actor)
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type:     "doc",
			Children: []json.TreeNode{{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcd"}}}},
		})
		return nil
	}))
	server := newMinVVServer(docs)
	for i := range docs {
		server.sync(t, 0)
		server.sync(t, i)
	}

	edit := func(i, from, to, splitLevel int, content *json.TreeNode) {
		require.NoError(t, docs[i].Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(from, to, content, splitLevel)
			return nil
		}))
	}

	edit(0, 4, 5, 0, nil) // d1 removes "d"
	removedAt, ok := docs[0].VersionVector().Get(docs[0].ActorID())
	require.True(t, ok)
	server.sync(t, 0)

	// d2 inserts first so its splits carry tickets newer than d1's.
	edit(1, 1, 1, 0, &json.TreeNode{Type: "text", Value: "x"})
	edit(1, 5, 5, 1, nil) // <p>xabc</p><p>d</p>
	edit(1, 8, 8, 1, nil) // <p>xabc</p><p>d</p><p></p>
	server.sync(t, 1)
	server.sync(t, 1)
	server.sync(t, 2)
	server.sync(t, 3)
	server.sync(t, 3)

	// Every vector the server holds now covers the removal, so this reply's
	// min does too, and d3 collects the removed "d" from inside d2's product.
	server.sync(t, 2)
	covered, _ := time.MinVersionVector(server.recorded...).Get(docs[0].ActorID())
	require.GreaterOrEqual(t, covered, removedAt, "the min has to cover d1's removal")
	require.Zero(t, docs[2].GarbageLen(), "d3 collects before d1's split reaches it")

	edit(0, 4, 4, 1, nil) // d1 splits after "c", not having seen d2's splits
	server.sync(t, 0)
	server.sync(t, 2)
	server.sync(t, 3)
	server.sync(t, 1)
	for range 3 {
		for i := range docs {
			server.sync(t, i)
		}
	}

	for i := 1; i < len(docs); i++ {
		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[i]), "replica %d", i)
	}
	assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[1]))
	assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[3]))
	assert.NotEqual(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[2]),
		"yorkie#2099: the replica that collected early places the split elsewhere")
}
