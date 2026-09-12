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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// A collecting replica must not reorder the elements that survive collection.
// The four tests below are the same defect seen from four angles: an insert
// whose RGA forward skip (RGATreeList.findNextBeforeExecutedAt) stopped at a
// node on one replica runs past that node on a replica that has already
// collected it, and the two orders never reconverge.
//
// Every version vector that authorises a purge here is one the server could
// genuinely compute: the element-wise min of the replicas' own vectors, as
// UpdateMinVersionVector does. helper.MaxVersionVector is used only at the end,
// to assert that nothing is retained forever.

// oneWayDeliver hands one replica's pending local changes to the other and acks
// them at the sender, i.e. a push followed by the other side's pull. crossSync
// always exchanges both ways, which cannot express "A pushed, B pulled, A has
// since gone on editing without pulling" -- the state in which a sound minVV
// covers A's older change while A holds a newer, unpushed one.
func oneWayDeliver(t *testing.T, from, to *document.Document) {
	t.Helper()

	p := from.CreateChangePack()
	require.NoError(t, to.ApplyChangePack(change.NewPack(
		p.DocumentKey, change.NewCheckpoint(0, 0), p.Changes, time.InitialVersionVector, nil,
	)))

	var lastSeq uint32
	if len(p.Changes) > 0 {
		lastSeq = p.Changes[len(p.Changes)-1].ClientSeq()
	}
	require.NoError(t, from.ApplyChangePack(change.NewPack(
		p.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))
}

// dumpRGA renders every position node of the array, dead ones included, so the
// two replicas can be compared by structure and not only by Marshal.
func dumpRGA(t *testing.T, d *document.Document, label string) string {
	t.Helper()

	arr, ok := d.RootObject().Get("arr").(*crdt.Array)
	require.True(t, ok)

	var sb strings.Builder
	sb.WriteString(label)
	sb.WriteString(": ")
	for _, n := range arr.AllRGANodes() {
		if n.Element() == nil {
			sb.WriteString(fmt.Sprintf("[dead pos=%s] ", n.PositionCreatedAt().Key()))
			continue
		}
		removed := ""
		if n.Element().RemovedAt() != nil {
			removed = " removed"
		}
		sb.WriteString(fmt.Sprintf("%s(pos=%s posAt=%s%s) ",
			n.Element().Marshal(), n.PositionCreatedAt().Key(), n.PositionedAt().Key(), removed))
	}
	return sb.String()
}

// removedAtOf returns the removal ticket of the array element with the given
// value.
func removedAtOf(t *testing.T, d *document.Document, value string) *time.Ticket {
	t.Helper()

	at := func() *time.Ticket {
		arr, ok := d.RootObject().Get("arr").(*crdt.Array)
		require.True(t, ok)
		for _, n := range arr.AllRGANodes() {
			if n.Element() != nil && n.Element().Marshal() == fmt.Sprintf("%q", value) {
				return n.Element().RemovedAt()
			}
		}
		return nil
	}()
	require.NotNil(t, at, "element %q is not removed", value)
	return at
}

// movedPositionTicket returns the position ticket of the only position node in
// "arr" that no insert created, i.e. the one a move stamped.
func movedPositionTicket(t *testing.T, d *document.Document) *time.Ticket {
	t.Helper()

	arr, ok := d.RootObject().Get("arr").(*crdt.Array)
	require.True(t, ok)
	for _, n := range arr.AllRGANodes() {
		if n.Element() != nil && n.PositionMovedAt() != nil {
			return n.PositionMovedAt()
		}
	}
	require.Fail(t, "no moved position node found")
	return nil
}

// createdAtOf returns the createdAt of the array element with the given value,
// tombstoned or not.
func createdAtOf(t *testing.T, d *document.Document, value string) *time.Ticket {
	t.Helper()

	arr, ok := d.RootObject().Get("arr").(*crdt.Array)
	require.True(t, ok)
	for _, n := range arr.AllRGANodes() {
		if n.Element() != nil && n.Element().Marshal() == fmt.Sprintf("%q", value) {
			return n.Element().CreatedAt()
		}
	}
	require.Failf(t, "element not found", "value=%s", value)
	return nil
}

// TestConcurrentRemoveAndMoveThenGCKeepsInsertOrder pins that collecting an
// element which was removed and concurrently moved must not move a later
// concurrent insert.
//
// A moved element no longer lives in the position node its insert created: the
// move builds a new position node stamped with the move's own ticket, and that
// node is what the forward skip reads. Purging the element unlinks that node,
// but the purge is gated only on the element's removedAt. When the remove and
// the move are concurrent, a version vector can cover the remove while the move
// is still in flight, so the node disappears on one replica and not on the
// other -- and a concurrent insert anchored just before it lands on different
// sides of the following element.
func TestConcurrentRemoveAndMoveThenGCKeepsInsertOrder(t *testing.T) {
	remover, mover, removerID, moverID := newReplicas(t)

	// Base state on both replicas: ["a","w"].
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("w")
		return nil
	}))
	crossSync(t, remover, mover)
	require.Equal(t, `{"arr":["a","w"]}`, remover.Marshal())
	require.Equal(t, `{"arr":["a","w"]}`, mover.Marshal())

	// Concurrent branch 1 (remover): remove "a".
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(0)
		return nil
	}))
	require.Equal(t, `{"arr":["w"]}`, remover.Marshal())

	// Concurrent branch 2 (mover): move "a" after "w", then -- after a few
	// unrelated changes, so its ticket outruns the insert below -- append "s"
	// anchored on "a"'s new position.
	require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 0)
		return nil
	}))
	require.Equal(t, `{"arr":["w","a"]}`, mover.Marshal())
	for i := range 3 {
		require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("mover-%d", i), i)
			return nil
		}))
	}
	require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(1, "s")
		return nil
	}))
	require.Equal(t, `["w","a","s"]`, mover.Root().GetArray("arr").Marshal())

	// The remover pushes only the removal. Both replicas have now seen it, so
	// it is causally stable; the move and the append are not.
	oneWayDeliver(t, remover, mover)
	require.Equal(t, `["w","s"]`, mover.Root().GetArray("arr").Marshal())

	// The version vectors the server would have on file at this point: each
	// replica's vector as of its last push.
	minVV := time.MinVersionVector(remover.VersionVector(), mover.VersionVector())

	// The remover keeps editing without pulling: one filler change, then an
	// insert anchored on "w". Its ticket sits strictly between the move and
	// the mover's append, which is the window in which the move's position
	// node decides where the insert goes.
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("remover-0", 0)
		return nil
	}))
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "x")
		return nil
	}))
	require.Equal(t, `["w","x"]`, remover.Root().GetArray("arr").Marshal())

	moveAt := movedPositionTicket(t, mover)
	insertAt := createdAtOf(t, remover, "x")
	appendAt := createdAtOf(t, mover, "s")
	t.Logf("minVV=%s move=%s insert=%s append=%s",
		minVV.Marshal(), moveAt.Key(), insertAt.Key(), appendAt.Key())
	require.True(t, insertAt.After(moveAt), "the insert must be newer than the move")
	require.True(t, appendAt.After(insertAt), "the append must be newer than the insert")

	removedAt := removedAtOf(t, remover, "a")
	require.True(t, minVV.EqualToOrAfter(removedAt),
		"the removal %s must be covered by the min version vector %s",
		removedAt.Key(), minVV.Marshal())
	require.False(t, minVV.EqualToOrAfter(moveAt),
		"the move %s must NOT be covered by the min version vector %s",
		moveAt.Key(), minVV.Marshal())

	// The mover collects with that vector. This is the only difference
	// between the two replicas.
	t.Log(dumpRGA(t, mover, "mover before GC"))
	mover.GarbageCollect(minVV)
	t.Log(dumpRGA(t, mover, "mover after GC "))
	t.Logf("RETAINED after deferred GC: garbageLen=%d docSize=%+v", mover.GarbageLen(), mover.DocSize())

	// Now the remover's insert arrives, and everything is exchanged.
	oneWayDeliver(t, remover, mover)
	crossSync(t, remover, mover)

	t.Logf("remover=%s", remover.Root().GetArray("arr").Marshal())
	t.Logf("mover  =%s", mover.Root().GetArray("arr").Marshal())
	require.Equal(t,
		remover.Root().GetArray("arr").Marshal(),
		mover.Root().GetArray("arr").Marshal(),
		"collecting an element whose position node was created by a concurrent move "+
			"reordered a later concurrent insert")

	// Holding the purge back must be a delay, not a leak: once the vector
	// covers the move as well, both replicas collect everything and land on
	// the same list.
	remover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	mover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	t.Logf("RETAINED after catch-up: remover=%d/%+v mover=%d/%+v",
		remover.GarbageLen(), remover.DocSize(), mover.GarbageLen(), mover.DocSize())
	require.Equal(t, 0, remover.GarbageLen(), "remover leaked garbage")
	require.Equal(t, 0, mover.GarbageLen(), "mover leaked garbage")
	crossSync(t, remover, mover)
	require.Equal(t,
		remover.Root().GetArray("arr").Marshal(),
		mover.Root().GetArray("arr").Marshal(),
		"the replicas did not reconverge")
	t.Log(dumpRGA(t, remover, "remover final"))
	t.Log(dumpRGA(t, mover, "mover   final"))
}

// vpush models a real PushPull *push*: the client hands its pending changes to
// the server and the server stores reqPack.VersionVector as that client's row
// in `versionvectors`. The changes are returned so another client can pull them
// later, exactly as the server would serve them.
func vpush(t *testing.T, d *document.Document, rows map[string]time.VersionVector) []*change.Change {
	t.Helper()

	// The row the server writes is the VV the client had BEFORE this round
	// trip's pull. Nothing is pulled here, so it is simply the current VV.
	rows[d.ActorID().String()] = d.VersionVector().DeepCopy()

	p := d.CreateChangePack()
	var lastSeq uint32
	if len(p.Changes) > 0 {
		lastSeq = p.Changes[len(p.Changes)-1].ClientSeq()
	}
	require.NoError(t, d.ApplyChangePack(change.NewPack(
		p.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))
	return p.Changes
}

// vpull models a pull: the client applies changes the server already holds.
func vpull(t *testing.T, d *document.Document, changes []*change.Change) {
	t.Helper()
	require.NoError(t, d.ApplyChangePack(change.NewPack(
		d.Key(), change.NewCheckpoint(0, 0), changes, time.InitialVersionVector, nil,
	)))
}

// serverMinVV is UpdateMinVersionVector's element-wise min over the stored rows.
func serverMinVV(rows map[string]time.VersionVector) time.VersionVector {
	var out time.VersionVector
	first := true
	for _, vv := range rows {
		if first {
			out = vv.DeepCopy()
			first = false
			continue
		}
		out = time.MinVersionVector(out, vv)
	}
	return out
}

// TestConcurrentMovesOfSameElementServerMinVV is the same divergence one move
// further along: the element is moved twice, concurrently, by two actors. The
// slot the second move abandons was created by the FIRST move, so the ticket
// that must stay covered is the first move's, not the element's insert -- and
// the two moves being concurrent means a sound minVV can cover one and not the
// other.
func TestConcurrentMovesOfSameElementServerMinVV(t *testing.T) {
	t.Run("with GC", func(t *testing.T) { runConcurrentMovesScenario(t, true) })
	t.Run("without GC (control)", func(t *testing.T) { runConcurrentMovesScenario(t, false) })
}

func runConcurrentMovesScenario(t *testing.T, collect bool) {
	dA, dB, idA, idB := newReplicas(t)
	rows := map[string]time.VersionVector{}

	// Base ["a","w","v"] on both, both rows on file.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("w").AddString("v")
		return nil
	}))
	base := vpush(t, dA, rows)
	vpull(t, dB, base)
	_ = vpush(t, dB, rows)
	require.Equal(t, `["a","w","v"]`, dB.Root().GetArray("arr").Marshal())

	// A (local, unpushed): move "a" after "w" (m1), filler, then insert "s"
	// anchored on "a"'s new (m1) position.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 0)
		return nil
	}))
	for i := range 2 {
		require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("fa-%d", i), i)
			return nil
		}))
	}
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(1, "s")
		return nil
	}))
	require.Equal(t, `["w","a","s","v"]`, dA.Root().GetArray("arr").Marshal())

	// B (concurrently, from base): move "a" after "v" (m2). B pushes.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(2, 0)
		return nil
	}))
	require.Equal(t, `["w","v","a"]`, dB.Root().GetArray("arr").Marshal())
	fromB := vpush(t, dB, rows)

	// A pulls m2, then pushes its own backlog. Both rows are now on file.
	vpull(t, dA, fromB)
	fromA := vpush(t, dA, rows)

	minVV := serverMinVV(rows)
	for id, vv := range rows {
		t.Logf("stored row %s = %s", id, vv.Marshal())
	}
	t.Logf("server minVV = %s", minVV.Marshal())

	// A collects with the server's minVV. This is the only asymmetry.
	t.Log(dumpRGA(t, dA, "A before GC"))
	if collect {
		t.Logf("A collected %d", dA.GarbageCollect(minVV))
	}
	t.Log(dumpRGA(t, dA, "A after GC "))
	t.Logf("RETAINED after deferred GC: garbageLen=%d docSize=%+v", dA.GarbageLen(), dA.DocSize())

	// B, which has not pulled A's backlog yet, inserts "x" after "w".
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "x")
		return nil
	}))
	fromB2 := vpush(t, dB, rows)

	// Everything is delivered both ways.
	vpull(t, dB, fromA)
	vpull(t, dA, fromB2)

	t.Log(dumpRGA(t, dA, "A final"))
	t.Log(dumpRGA(t, dB, "B final"))
	t.Logf("A=%s", dA.Root().GetArray("arr").Marshal())
	t.Logf("B=%s", dB.Root().GetArray("arr").Marshal())

	// Nothing reconverges them, including a full collection on both sides.
	dA.GarbageCollect(helper.MaxVersionVector(idA, idB))
	dB.GarbageCollect(helper.MaxVersionVector(idA, idB))
	t.Logf("after full GC: A=%s B=%s",
		dA.Root().GetArray("arr").Marshal(), dB.Root().GetArray("arr").Marshal())

	require.Equal(t,
		dA.Root().GetArray("arr").Marshal(),
		dB.Root().GetArray("arr").Marshal(),
		"replicas diverged permanently under a server-computed minVV")

	// One dead slot survives a full version vector on each side, and the
	// original form of this assertion -- GarbageLen back to zero -- was wrong
	// about it. That slot is "a"'s FIRST position, the one nodeMapByCreatedAt
	// answers with when an ArraySet on "a" resolves its anchor, so it stays
	// reachable for exactly as long as "a" is. "Removed" is not the same as
	// "unreachable", and a vector says nothing about the second.
	//
	// Retiring the name is what collects it, so the retention is bounded by
	// the element and not by the number of moves: remove "a" and the slot
	// drains on the next pass.
	require.Equal(t, 1, dA.GarbageLen(), "A should hold only \"a\"'s first slot")
	require.Equal(t, 1, dB.GarbageLen(), "B should hold only \"a\"'s first slot")

	for _, d := range []*document.Document{dA, dB} {
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			arr := r.GetArray("arr")
			for i := range arr.Len() {
				if arr.Get(i).Marshal() == `"a"` {
					arr.Delete(i)
					break
				}
			}
			return nil
		}))
		d.GarbageCollect(helper.MaxVersionVector(idA, idB))
		require.Equal(t, 0, d.GarbageLen(), "the first slot drains once the element is removed")
	}
}

// TestConcurrentDeleteAndInsertThenGCKeepsTextOrder is the same defect in
// RGATreeSplit, with no move anywhere in it: a plain delete is enough. The
// tombstone left by the delete is what stops the skip in findNodeWithSplit, and
// collecting it sends a later concurrent insert past the node behind it.
func TestConcurrentDeleteAndInsertThenGCKeepsTextOrder(t *testing.T) {
	remover, mover, removerID, moverID := newReplicas(t)

	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		txt := r.SetNewText("t")
		txt.Edit(0, 0, "A")
		txt.Edit(0, 0, "W")
		return nil
	}))
	crossSync(t, remover, mover)
	require.Equal(t, `WA`, remover.Root().GetText("t").String())
	require.Equal(t, `WA`, mover.Root().GetText("t").String())

	// mover: bump its clock, then append "S" anchored right after "A".
	for i := range 3 {
		require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("mover-%d", i), i)
			return nil
		}))
	}
	require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetText("t").Edit(2, 2, "S")
		return nil
	}))
	require.Equal(t, `WAS`, mover.Root().GetText("t").String())

	// remover: delete "A".
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetText("t").Edit(1, 2, "")
		return nil
	}))
	require.Equal(t, `W`, remover.Root().GetText("t").String())

	// The removal reaches the mover, so it is causally stable. "S" is not.
	oneWayDeliver(t, remover, mover)
	require.Equal(t, `WS`, mover.Root().GetText("t").String())

	minVV := time.MinVersionVector(remover.VersionVector(), mover.VersionVector())
	t.Logf("minVV=%s", minVV.Marshal())

	// remover keeps editing without pulling: insert "X" right after "W".
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("remover-0", 0)
		return nil
	}))
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetText("t").Edit(1, 1, "X")
		return nil
	}))
	require.Equal(t, `WX`, remover.Root().GetText("t").String())

	t.Logf("mover before GC: %s", mover.Root().GetText("t").ToTestString())
	t.Logf("mover collected %d", mover.GarbageCollect(minVV))
	t.Logf("mover after  GC: %s", mover.Root().GetText("t").ToTestString())

	oneWayDeliver(t, remover, mover)
	crossSync(t, remover, mover)

	t.Logf("remover=%s", remover.Root().GetText("t").String())
	t.Logf("mover  =%s", mover.Root().GetText("t").String())
	require.Equal(t,
		remover.Root().GetText("t").String(),
		mover.Root().GetText("t").String(),
		"text replicas diverged after collecting a tombstone")

	remover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	mover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	require.Equal(t, 0, remover.GarbageLen(), "remover leaked garbage")
	require.Equal(t, 0, mover.GarbageLen(), "mover leaked garbage")
}

// TestConcurrentDeleteAndInsertThenGCKeepsTreeOrder is the same defect again in
// CRDTTree, whose sibling skip in findNodesAndSplitText reads the parent's
// children with the removed ones included.
func TestConcurrentDeleteAndInsertThenGCKeepsTreeOrder(t *testing.T) {
	remover, mover, removerID, moverID := newReplicas(t)

	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{
			Type:     "doc",
			Children: []json.TreeNode{{Type: "p"}},
		})
		r.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "A"}, 0)
		r.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "W"}, 0)
		return nil
	}))
	crossSync(t, remover, mover)
	require.Equal(t, `<doc><p>WA</p></doc>`, remover.Root().GetTree("t").ToXML())
	require.Equal(t, `<doc><p>WA</p></doc>`, mover.Root().GetTree("t").ToXML())

	for i := range 3 {
		require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("mover-%d", i), i)
			return nil
		}))
	}
	require.NoError(t, mover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "S"}, 0)
		return nil
	}))
	require.Equal(t, `<doc><p>WAS</p></doc>`, mover.Root().GetTree("t").ToXML())

	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(2, 3, nil, 0)
		return nil
	}))
	require.Equal(t, `<doc><p>W</p></doc>`, remover.Root().GetTree("t").ToXML())

	oneWayDeliver(t, remover, mover)
	require.Equal(t, `<doc><p>WS</p></doc>`, mover.Root().GetTree("t").ToXML())

	minVV := time.MinVersionVector(remover.VersionVector(), mover.VersionVector())
	t.Logf("minVV=%s", minVV.Marshal())

	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("remover-0", 0)
		return nil
	}))
	require.NoError(t, remover.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "X"}, 0)
		return nil
	}))
	require.Equal(t, `<doc><p>WX</p></doc>`, remover.Root().GetTree("t").ToXML())

	t.Logf("mover collected %d", mover.GarbageCollect(minVV))

	oneWayDeliver(t, remover, mover)
	crossSync(t, remover, mover)

	t.Logf("remover=%s", remover.Root().GetTree("t").ToXML())
	t.Logf("mover  =%s", mover.Root().GetTree("t").ToXML())
	require.Equal(t,
		remover.Root().GetTree("t").ToXML(),
		mover.Root().GetTree("t").ToXML(),
		"tree replicas diverged after collecting a tombstone")

	remover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	mover.GarbageCollect(helper.MaxVersionVector(removerID, moverID))
	require.Equal(t, 0, remover.GarbageLen(), "remover leaked garbage")
	require.Equal(t, 0, mover.GarbageLen(), "mover leaked garbage")
}
