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

package crdt_test

import (
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// InsNextID is a structural pointer only SplitElement is supposed to write,
// but the wire format carries it on every tree node, so a document rebuilt
// from stored client changes can hold a chain that loops back on itself. Every
// walk of that chain runs under the document lock, so an unbounded one hangs
// the applying goroutine — and collectBetween's cascade also appends to
// toBeRemoveds on every turn, so it burns memory while it spins.
//
// The converter now strips the field from client-supplied content
// (FromTreeNodesWhenEdit for operation content, fromElement for the element
// bytes a Set/Add/SetByIndex carries), so these chains should no longer be
// constructible. The walks stay bounded anyway: documents stored before that
// already carry whatever a client sent.

var (
	// knownActor creates the nodes the operation under test knows about.
	knownActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	// cycleActor creates the two nodes that point at each other. They have to
	// be unknown to the operation's version vector: that is the only case
	// these walks follow the chain at all.
	cycleActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	// remoteActor runs the operation.
	remoteActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}
)

// ticketer hands out tickets with increasing lamports for a given actor.
type ticketer struct{ lamport int64 }

func (s *ticketer) issue(actor time.ActorID) *time.Ticket {
	s.lamport++
	return time.NewTicket(s.lamport, 0, actor)
}

// poisonedTree builds
//
//	<r><p>ab</p><p></p><p></p><p>cd</p></r>
//
// where the two middle paragraphs were created by cycleActor and point at each
// other through InsNextID, and both the first paragraph and its text node link
// into that cycle. Every InsNextID walk reachable from the first paragraph
// runs into it.
func poisonedTree(t *testing.T) *crdt.Tree {
	t.Helper()

	var s ticketer
	node := func(actor time.ActorID, nodeType string, value ...string) *crdt.TreeNode {
		return crdt.NewTreeNode(crdt.NewTreeNodeID(s.issue(actor), 0), nodeType, nil, value...)
	}
	issue := func() *time.Ticket { return s.issue(knownActor) }

	tree := crdt.NewTree(node(knownActor, "r"), s.issue(knownActor))

	first := node(knownActor, "p")
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{first}, 0, issue(), issue)
	assert.NoError(t, err)

	text := node(knownActor, "text", "ab")
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{text}, 0, issue(), issue)
	assert.NoError(t, err)

	left := node(cycleActor, "p")
	_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{left}, 0, issue(), issue)
	assert.NoError(t, err)

	right := node(cycleActor, "p")
	_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{right}, 0, issue(), issue)
	assert.NoError(t, err)

	last := node(knownActor, "p")
	_, _, err = tree.EditT(8, 8, []*crdt.TreeNode{last}, 0, issue(), issue)
	assert.NoError(t, err)

	_, _, err = tree.EditT(9, 9, []*crdt.TreeNode{node(knownActor, "text", "cd")}, 0, issue(), issue)
	assert.NoError(t, err)

	assert.Equal(t, "<r><p>ab</p><p></p><p></p><p>cd</p></r>", tree.ToXML())

	text.InsNextID = left.ID()
	first.InsNextID = left.ID()
	left.InsNextID = right.ID()
	right.InsNextID = left.ID()

	return tree
}

// mustFinish fails instead of hanging the whole package when a walk does not
// terminate.
func mustFinish(t *testing.T, name string, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
	case <-gotime.After(10 * gotime.Second):
		t.Fatalf("%s did not terminate on a cyclic InsNextID chain", name)
	}
}

func TestTreeCyclicSplitLink(t *testing.T) {
	editedAt := time.NewTicket(time.MaxLamport, 0, remoteActor)
	// knownActor's nodes are known, cycleActor's are not.
	vector := helper.VersionVectorOf(map[time.ActorID]int64{
		knownActor:  time.MaxLamport,
		remoteActor: time.MaxLamport,
	})

	// Edit walks the chain twice: Phase 3 range narrowing follows fromLeft's
	// chain looking for a sibling under toParent, and collectBetween cascades
	// the delete to unknown split siblings of every element it removes.
	t.Run("edit over the cycle terminates", func(t *testing.T) {
		tree := poisonedTree(t)
		from, err := tree.FindPos(3)
		assert.NoError(t, err)
		to, err := tree.FindPos(11)
		assert.NoError(t, err)

		mustFinish(t, "Edit", func() {
			_, _, _, err := tree.Edit(from, to, nil, 0, editedAt, func() *time.Ticket {
				return editedAt
			}, vector, false)
			assert.NoError(t, err)
		})
	})

	// Style and RemoveStyle propagate to unknown split siblings along the
	// same chain.
	t.Run("style over the cycle terminates", func(t *testing.T) {
		tree := poisonedTree(t)
		from, err := tree.FindPos(0)
		assert.NoError(t, err)
		to, err := tree.FindPos(12)
		assert.NoError(t, err)

		mustFinish(t, "Style", func() {
			_, _, _, err := tree.Style(from, to, map[string]string{"b": "t"}, editedAt, vector)
			assert.NoError(t, err)
		})
	})

	t.Run("remove style over the cycle terminates", func(t *testing.T) {
		tree := poisonedTree(t)
		from, err := tree.FindPos(0)
		assert.NoError(t, err)
		to, err := tree.FindPos(12)
		assert.NoError(t, err)

		mustFinish(t, "RemoveStyle", func() {
			_, _, _, err := tree.RemoveStyle(from, to, []string{"b"}, editedAt, vector)
			assert.NoError(t, err)
		})
	})
}
