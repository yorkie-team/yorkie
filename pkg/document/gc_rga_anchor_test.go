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

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// anchorServer is the same push/pull model server/packs/pushpull.go implements:
// one ordered change log, one stored VersionVector row per client taken from
// that client's push request, minVV the element-wise minimum over the rows, and
// collection run only inside ApplyChangePack with the vector the pull carried.
type anchorServer struct {
	log    []*change.Change
	actors []string
	rows   map[string]time.VersionVector
}

type anchorClient struct {
	doc    *document.Document
	id     time.ActorID
	cursor int
}

func newAnchorFixture(t *testing.T, n int) (*anchorServer, []*anchorClient) {
	t.Helper()
	srv := &anchorServer{rows: map[string]time.VersionVector{}}
	cs := make([]*anchorClient, 0, n)
	for i := range n {
		id, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		d := document.New("anchor-doc")
		d.SetActor(id)
		cs = append(cs, &anchorClient{doc: d, id: id})
	}
	return srv, cs
}

func (s *anchorServer) sync(c *anchorClient) error {
	p := c.doc.CreateChangePack()

	var lastSeq uint32
	for _, ch := range p.Changes {
		s.log = append(s.log, ch)
		s.actors = append(s.actors, ch.ID().ActorID().String())
		lastSeq = ch.ClientSeq()
	}
	s.rows[c.id.String()] = p.VersionVector.DeepCopy()

	var chs []*change.Change
	for i := c.cursor; i < len(s.log); i++ {
		if s.actors[i] == c.id.String() {
			continue
		}
		chs = append(chs, s.log[i])
	}
	c.cursor = len(s.log)

	var vectors []time.VersionVector
	for _, vv := range s.rows {
		vectors = append(vectors, vv)
	}

	return c.doc.ApplyChangePack(change.NewPack(
		c.doc.Key(), change.NewCheckpoint(0, lastSeq), chs,
		time.MinVersionVector(vectors...), nil,
	))
}

// TestArrayMoveLoserSlotOutlivesConcurrentAnchors covers the slot a move
// creates when it LOSES the position register's last-writer-wins race.
//
// That slot is born dead. Stamping it with the losing move's own ticket claims
// a death that every replica already knows about the instant the slot appears,
// so the replica that saw the winner first may collect it immediately -- while
// the replica that has not yet seen the winner still holds the element in that
// very slot and is still issuing moves anchored on it. Those moves then arrive
// at an anchor that is gone.
//
// The slot is killed by the move that beat it, so that is the ticket its death
// carries, and collection then has to wait for the winner to be known
// everywhere -- which is exactly long enough for the operations anchored on it
// to have been delivered.
func TestArrayMoveLoserSlotOutlivesConcurrentAnchors(t *testing.T) {
	srv, cs := newAnchorFixture(t, 2)
	A, B := cs[0], cs[1]

	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(B))

	// Concurrent moves of the same element. B's ticket is the newer one, so B's
	// slot wins the register and A's slot is the loser.
	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 2) // c after b
		return nil
	}))
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(2, 2) // c after itself
		return nil
	}))

	// A pushes first, so B sees A's move and discards it by LWW -- that is
	// where B materialises the loser slot, already dead.
	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(B))

	// A has not seen B's move yet, so on A the element still sits in the slot
	// B considers dead, and this move anchors on it.
	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(2, 2)
		return nil
	}))

	// B syncs twice so its row covers everything it has applied; this is the
	// moment the loser slot becomes collectable on B.
	require.NoError(t, srv.sync(B))
	require.NoError(t, srv.sync(B))

	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(B),
		"B could not apply a move anchored on a slot that was alive on A")

	for range 3 {
		require.NoError(t, srv.sync(A))
		require.NoError(t, srv.sync(B))
	}
	require.Equal(t,
		A.doc.Root().GetArray("arr").Marshal(),
		B.doc.Root().GetArray("arr").Marshal())
}

// TestArraySetAnchorsOnLiveSlot covers assignment into an array whose target
// element has been moved.
//
// The assignment inserts the replacement next to the element it replaces, so it
// has to name a slot. Naming the element's ORIGINAL slot names one the move
// already abandoned: abandoned slots are collectable, and once collected the
// assignment has to be re-pointed at whatever the element's current slot
// happens to be -- a different place in the list, and not the same different
// place on every replica.
//
// The assignment therefore carries the slot the element occupies when the
// assignment is made, which is alive by construction.
func TestArraySetAnchorsOnLiveSlot(t *testing.T) {
	srv, cs := newAnchorFixture(t, 2)
	A, B := cs[0], cs[1]

	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(B))

	// A inserts, B moves "c" up to the front. Both are delivered.
	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "x")
		return nil
	}))
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(0, 2) // c after a
		return nil
	}))
	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(B))

	// B assigns over the moved element. Its original slot is dead on both
	// replicas by now.
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetString(1, "z")
		return nil
	}))

	// A syncs twice without the assignment: minVV now covers the move, so A
	// collects the slot the assignment would have named.
	require.NoError(t, srv.sync(A))
	require.NoError(t, srv.sync(A))

	for range 4 {
		require.NoError(t, srv.sync(B))
		require.NoError(t, srv.sync(A))
	}
	require.Equal(t,
		B.doc.Root().GetArray("arr").Marshal(),
		A.doc.Root().GetArray("arr").Marshal(),
		"replicas diverged over an assignment anchored on a collected slot")
}
