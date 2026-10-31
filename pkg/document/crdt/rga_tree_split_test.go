/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestRGATreeSplit(t *testing.T) {
	t.Run("compare nil id panic test", func(t *testing.T) {
		id := crdt.NewRGATreeSplitNodeID(time.InitialTicket, 0)
		assert.Panics(t, func() { id.Compare(nil) }, "ID cannot be null")
	})

	t.Run("concurrent read of the shared initial node ID test", func(t *testing.T) {
		// The head of every Text shares one node ID: `go test -race` fails
		// here if reading it is not race-free.
		key := time.InitialTicket.Key() + ":0"

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				assert.Equal(t, key, crdt.InitialTextNode().IDString())
			})
		}
		wg.Wait()
	})

	t.Run("concurrent read of a fresh node ID test", func(t *testing.T) {
		// DeepCopy of a split node shares its ID, so the first read of a cold
		// ID must be race-free: `go test -race` fails here if key fills its
		// cache with a plain write.
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		createdAt := time.NewTicket(7, 3, actorID)
		node := crdt.NewRGATreeSplitNode(
			crdt.NewRGATreeSplitNodeID(createdAt, 2),
			crdt.NewTextValue("abc", crdt.NewRHT()),
		)

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				assert.Equal(t, "7:3:"+actorID.StringBase64()+":2", node.IDString())
			})
		}
		wg.Wait()
	})

	t.Run("warmed key cache keeps deep equality test", func(t *testing.T) {
		// Node IDs built independently are compared with assert.Equal, which
		// falls through to reflect.DeepEqual. Reading the key must not make
		// two logically identical IDs differ.
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		newNode := func() *crdt.RGATreeSplitNode[*crdt.TextValue] {
			return crdt.NewRGATreeSplitNode(
				crdt.NewRGATreeSplitNodeID(time.NewTicket(7, 3, actorID), 2),
				crdt.NewTextValue("abc", crdt.NewRHT()),
			)
		}
		one, other := newNode(), newNode()

		assert.Equal(t, one.ID(), other.ID())
		assert.Equal(t, one.IDString(), other.IDString())
		assert.Equal(t, one.ID(), other.ID())
	})
}

func TestRGATreeSplitNodeRemovalEvents(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	assert.NoError(t, err)
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	assert.NoError(t, err)
	a := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actorA) }
	b := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actorB) }
	node := func(removedAt *time.Ticket) *crdt.RGATreeSplitNode[*crdt.TextValue] {
		n := crdt.NewRGATreeSplitNode(crdt.NewRGATreeSplitNodeID(a(1), 0), crdt.NewTextValue("x", crdt.NewRHT()))
		n.SetRemovedAt(removedAt)
		return n
	}

	t.Run("pairs each actor's events by place test", func(t *testing.T) {
		// A's removal holds the node; B removed it and undid that.
		n := node(a(5))
		assert.NoError(t, n.SetRemovalEvents([]*time.Ticket{b(6), b(4)}))
		_, dels, cancels := n.Ledger()
		assert.Len(t, dels, 2)
		assert.Len(t, cancels, 1)
		assert.Equal(t, 0, cancels[0].Deletion.Compare(b(4)))
		assert.Equal(t, 0, cancels[0].CancelledAt.Compare(b(6)))
		assert.Equal(t, 0, n.RemovedAt().Compare(a(5)))
		// Compared by key, not by value: a ticket caches its key lazily, so
		// two equal tickets differ structurally once one has been keyed.
		keys := func(tickets []*time.Ticket) []string {
			out := make([]string, 0, len(tickets))
			for _, t := range tickets {
				out = append(out, t.Key())
			}
			return out
		}
		assert.Equal(t, keys([]*time.Ticket{b(4), b(6)}), keys(n.RemovalEvents()))
	})

	t.Run("rejects malformed events test", func(t *testing.T) {
		for name, tc := range map[string]struct {
			removedAt *time.Ticket
			events    []*time.Ticket
		}{
			"removed_at repeated":       {a(5), []*time.Ticket{a(5)}},
			"event repeated":            {nil, []*time.Ticket{a(5), a(5)}},
			"removal without removedAt": {nil, []*time.Ticket{a(5)}},
			"removed_at cancelled":      {a(5), []*time.Ticket{a(3)}},
		} {
			assert.Error(t, node(tc.removedAt).SetRemovalEvents(tc.events), name)
		}
	})
}
