//go:build rgafuzz

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

// This harness FAILS on main, deliberately. It is the reproduction for
// docs/tasks/active/20260912-collection-changes-rga-insertion-todo.md:
// collection unlinks the tombstones the RGA forward skip reads, so two
// replicas that differ only in whether they collected order the same later
// insert differently.
//
// It is behind a build tag rather than skipped so that it cannot rot silently
// and cannot fail CI:
//
//	go test -tags rgafuzz ./pkg/document/ -run TestAdvArray -v
//
// TestAdvArrayFuzzNoGC is the control and passes -- the same seeds converge
// when nothing is collected, which is what makes collection the cause rather
// than a correlate. A fix for the filed defect is complete when the
// collection-on run matches that control.
//
// The push/pull model here is faithful to server/packs/pushpull.go: each
// client's row is stored from its request's version vector at push time, a
// client's own changes are filtered out of its pull, minVV is the element-wise
// minimum over the stored rows, and collection runs only inside
// Document.ApplyChangePack with the vector that pull delivered. No
// helper.MaxVersionVector is used anywhere, so every collection it performs is
// one the protocol would also perform.

package document_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// advServer models server/packs/pushpull.go: one ordered change log, one stored
// VersionVector row per client (the VV carried in that client's push request),
// and minVV = element-wise min over the stored rows (memory DB
// GetMinVersionVector). A client only ever sees minVV as the VersionVector of a
// pull response, and Document.ApplyChangePack is what runs GC with it -- so GC
// here can never be called at a moment the protocol would not produce.
type advServer struct {
	log    []*change.Change
	actors []string
	rows   map[string]time.VersionVector
}

type advClient struct {
	doc    *document.Document
	id     time.ActorID
	cursor int
}

func newAdvServer() *advServer {
	return &advServer{rows: map[string]time.VersionVector{}}
}

func (s *advServer) minVV() time.VersionVector {
	var vectors []time.VersionVector
	for _, vv := range s.rows {
		vectors = append(vectors, vv)
	}
	return time.MinVersionVector(vectors...)
}

// sync is one PushPull round trip: push local changes, store the request's VV,
// pull everything the server holds, and hand the response (changes + minVV) to
// ApplyChangePack, which applies then collects.
func (s *advServer) sync(c *advClient, gcOn bool) error {
	p := c.doc.CreateChangePack()

	// 01. push: append to the log, then record the row the request carried.
	var lastSeq uint32
	for _, ch := range p.Changes {
		s.log = append(s.log, ch)
		s.actors = append(s.actors, ch.ID().ActorID().String())
		lastSeq = ch.ClientSeq()
	}
	s.rows[c.id.String()] = p.VersionVector.DeepCopy()

	// 02. pull: everything on the log this client has not seen.
	var chs []*change.Change
	for i := c.cursor; i < len(s.log); i++ {
		if s.actors[i] == c.id.String() {
			continue
		}
		chs = append(chs, s.log[i])
	}
	c.cursor = len(s.log)

	vv := s.minVV()
	if !gcOn {
		vv = time.InitialVersionVector
	}

	return c.doc.ApplyChangePack(change.NewPack(
		c.doc.Key(), change.NewCheckpoint(0, lastSeq), chs, vv, nil,
	))
}

func newAdvClients(t *testing.T, n int) []*advClient {
	t.Helper()
	cs := make([]*advClient, 0, n)
	for i := range n {
		id, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}
	return cs
}

var allOps = []int{0, 1, 2, 3}

var (
	advTraceSeed    = 60
	advTraceClients = 3
	advTraceRounds  = 2
	advTraceOps     = []int{0, 1, 2, 3}
)

// ---- array fuzz ----------------------------------------------------------

func advArrayOp(c *advClient, rnd *rand.Rand, tag string, ops []int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in local op: %v", r)
		}
	}()
	return c.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		arr := r.GetArray("arr")
		n := arr.Len()
		switch ops[rnd.Intn(len(ops))] {
		case 0:
			if n == 0 {
				arr.AddString(tag)
				return nil
			}
			arr.InsertStringAfter(rnd.Intn(n), tag)
		case 1:
			if n == 0 {
				return nil
			}
			arr.Delete(rnd.Intn(n))
		case 2:
			if n < 2 {
				return nil
			}
			arr.MoveAfterByIndex(rnd.Intn(n), rnd.Intn(n))
		case 3:
			if n == 0 {
				return nil
			}
			arr.SetString(rnd.Intn(n), tag)
		}
		return nil
	})
}

func runAdvArraySeed(seed int64, nClients, rounds int, gc bool, ops []int) (string, error) {
	rnd := rand.New(rand.NewSource(seed))
	srv := newAdvServer()

	cs := make([]*advClient, 0, nClients)
	for i := range nClients {
		id, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		if err != nil {
			return "", err
		}
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}

	if err := cs[0].doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}); err != nil {
		return "", err
	}
	for _, c := range cs {
		if err := srv.sync(c, gc); err != nil {
			return "", fmt.Errorf("bootstrap sync: %w", err)
		}
	}

	for range rounds {
		for _, c := range cs {
			if rnd.Intn(100) < 70 {
				if err := advArrayOp(c, rnd, fmt.Sprintf("%s%d", c.id.String()[22:], rnd.Intn(100)), ops); err != nil {
					return "", err
				}
			}
		}
		for _, c := range cs {
			if rnd.Intn(100) < 55 {
				if err := srv.sync(c, gc); err != nil {
					return "", fmt.Errorf("sync: %w", err)
				}
			}
		}
	}

	// Full convergence: everyone syncs until quiet.
	for range 6 {
		for _, c := range cs {
			if err := srv.sync(c, gc); err != nil {
				return "", fmt.Errorf("final sync: %w", err)
			}
		}
	}

	want := cs[0].doc.Root().GetArray("arr").Marshal()
	for i, c := range cs[1:] {
		got := c.doc.Root().GetArray("arr").Marshal()
		if got != want {
			return "", fmt.Errorf("DIVERGED c0 vs c%d\n  c0=%s\n  c%d=%s", i+1, want, i+1, got)
		}
	}
	return want, nil
}

func TestAdvArrayFuzz(t *testing.T) {
	var failures int
	var first []string
	for seed := int64(1); seed <= 300; seed++ {
		if _, err := runAdvArraySeed(seed, 3, 12, true, allOps); err != nil {
			failures++
			if len(first) < 3 {
				first = append(first, fmt.Sprintf("seed %d: %v", seed, err))
			}
		}
	}
	for _, f := range first {
		t.Log(f)
	}
	if failures > 0 {
		t.Errorf("GC ON: %d/300 seeds diverged or errored", failures)
	}
}

func TestAdvArrayFuzzNoGC(t *testing.T) {
	var failures int
	for seed := int64(1); seed <= 300; seed++ {
		if _, err := runAdvArraySeed(seed, 3, 12, false, allOps); err != nil {
			t.Errorf("seed %d (GC off): %v", seed, err)
			failures++
			if failures >= 5 {
				t.Fatal("stopping after 5")
			}
		}
	}
}

func TestAdvArrayOpMask(t *testing.T) {
	cases := []struct {
		name string
		ops  []int
	}{
		{"insert+delete", []int{0, 1}},
		{"insert+delete+move", []int{0, 1, 2}},
		{"insert+delete+set", []int{0, 1, 3}},
		{"insert+move", []int{0, 2}},
		{"insert+set", []int{0, 3}},
		{"all", []int{0, 1, 2, 3}},
	}
	for _, c := range cases {
		var fail int
		var first string
		for seed := int64(1); seed <= 300; seed++ {
			if _, err := runAdvArraySeed(seed, 3, 12, true, c.ops); err != nil {
				fail++
				if first == "" {
					first = fmt.Sprintf("seed %d: %v", seed, err)
				}
			}
		}
		t.Logf("%-22s GC ON: %3d/300 failed   %s", c.name, fail, first)
	}
}

func TestAdvArrayShrinkSweep(t *testing.T) {
	for _, nc := range []int{2, 3} {
		for _, rounds := range []int{2, 3, 4, 6, 8} {
			var fail int
			var first string
			for seed := int64(1); seed <= 2000; seed++ {
				if _, err := runAdvArraySeed(seed, nc, rounds, true, []int{0, 1}); err != nil {
					fail++
					if first == "" {
						first = fmt.Sprintf("seed=%d %v", seed, err)
					}
				}
			}
			t.Logf("clients=%d rounds=%2d insert+delete: %4d/2000  %s", nc, rounds, fail, first)
		}
	}
}

// ---- traced minimal reproduction ----------------------------------------

func advDumpArr(d *document.Document) string {
	arr, ok := d.RootObject().Get("arr").(*crdt.Array)
	if !ok {
		return "<none>"
	}
	var sb strings.Builder
	for _, n := range arr.AllRGANodes() {
		if n.Element() == nil {
			sb.WriteString(fmt.Sprintf("[dead pos=%s] ", n.PositionCreatedAt().Key()))
			continue
		}
		rm := ""
		if n.Element().RemovedAt() != nil {
			rm = "!"
		}
		sb.WriteString(fmt.Sprintf("%s%s(pos=%s) ", n.Element().Marshal(), rm, n.PositionCreatedAt().Key()))
	}
	return sb.String()
}

func TestAdvArrayTrace(t *testing.T) {
	seed := int64(advTraceSeed)
	nClients, rounds := advTraceClients, advTraceRounds
	rnd := rand.New(rand.NewSource(seed))
	srv := newAdvServer()

	cs := make([]*advClient, 0, nClients)
	for i := range nClients {
		id, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}
	name := func(c *advClient) string { return "C" + c.id.String()[23:] }

	sync := func(c *advClient) error {
		p := c.doc.CreateChangePack()
		var lastSeq uint32
		for _, ch := range p.Changes {
			srv.log = append(srv.log, ch)
			srv.actors = append(srv.actors, ch.ID().ActorID().String())
			lastSeq = ch.ClientSeq()
		}
		srv.rows[c.id.String()] = p.VersionVector.DeepCopy()
		var chs []*change.Change
		for i := c.cursor; i < len(srv.log); i++ {
			if srv.actors[i] == c.id.String() {
				continue
			}
			chs = append(chs, srv.log[i])
		}
		c.cursor = len(srv.log)
		vv := srv.minVV()
		t.Logf("  SYNC %s push=%d pull=%d row=%s minVV=%s", name(c), len(p.Changes), len(chs),
			p.VersionVector.Marshal(), vv.Marshal())
		before := advDumpArr(c.doc)
		gl := c.doc.GarbageLen()
		err := c.doc.ApplyChangePack(change.NewPack(
			c.doc.Key(), change.NewCheckpoint(0, lastSeq), chs, vv, nil,
		))
		if err != nil {
			t.Logf("    ERROR: %v", err)
			t.Logf("    state before apply: %s", before)
			return err
		}
		t.Logf("    %s: %s   (garbageLen %d -> %d)", name(c), advDumpArr(c.doc), gl, c.doc.GarbageLen())
		return nil
	}

	require.NoError(t, cs[0].doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	for _, c := range cs {
		require.NoError(t, sync(c))
	}
	t.Logf("bootstrap done")

	ops := advTraceOps
	for round := range rounds {
		t.Logf("--- round %d ---", round)
		for _, c := range cs {
			if rnd.Intn(100) < 70 {
				tag := fmt.Sprintf("%s%d", c.id.String()[22:], rnd.Intn(100))
				require.NoError(t, c.doc.Update(func(r *json.Object, _ *presence.Presence) error {
					arr := r.GetArray("arr")
					n := arr.Len()
					switch ops[rnd.Intn(len(ops))] {
					case 0:
						if n == 0 {
							arr.AddString(tag)
							t.Logf("  %s ADD %s", name(c), tag)
							return nil
						}
						i := rnd.Intn(n)
						t.Logf("  %s INSERT %s after idx %d (%s)", name(c), tag, i, arr.Get(i).Marshal())
						arr.InsertStringAfter(i, tag)
					case 1:
						if n == 0 {
							return nil
						}
						i := rnd.Intn(n)
						t.Logf("  %s DELETE idx %d (%s)", name(c), i, arr.Get(i).Marshal())
						arr.Delete(i)
					case 2:
						if n < 2 {
							return nil
						}
						a1, a2 := rnd.Intn(n), rnd.Intn(n)
						t.Logf("  %s MOVE idx %d (%s) after idx %d (%s)", name(c), a2, arr.Get(a2).Marshal(), a1, arr.Get(a1).Marshal())
						arr.MoveAfterByIndex(a1, a2)
					case 3:
						if n == 0 {
							return nil
						}
						i := rnd.Intn(n)
						t.Logf("  %s SET idx %d (%s) = %s", name(c), i, arr.Get(i).Marshal(), tag)
						arr.SetString(i, tag)
					}
					return nil
				}))
			}
		}
		for _, c := range cs {
			if rnd.Intn(100) < 55 {
				if err := sync(c); err != nil {
					t.Fatalf("FAILED in round %d", round)
				}
			}
		}
	}
	for range 6 {
		for _, c := range cs {
			if err := sync(c); err != nil {
				t.Fatalf("FAILED in final sync")
			}
		}
	}
	for i, c := range cs {
		t.Logf("final C%d = %s", i, c.doc.Root().GetArray("arr").Marshal())
	}
}

func TestAdvArrayCategorize(t *testing.T) {
	for _, c := range []struct {
		name string
		ops  []int
	}{
		{"insert+delete", []int{0, 1}},
		{"all", []int{0, 1, 2, 3}},
	} {
		var diverged, notfound, other int
		var firstDiv string
		for seed := int64(1); seed <= 1000; seed++ {
			_, err := runAdvArraySeed(seed, 3, 12, true, c.ops)
			if err == nil {
				continue
			}
			switch {
			case strings.Contains(err.Error(), "DIVERGED"):
				diverged++
				if firstDiv == "" {
					firstDiv = fmt.Sprintf("seed %d: %v", seed, err)
				}
			case strings.Contains(err.Error(), "child not found"):
				notfound++
			default:
				other++
				t.Logf("OTHER seed %d: %v", seed, err)
			}
		}
		t.Logf("%-14s /1000: diverged=%d childNotFound=%d other=%d", c.name, diverged, notfound, other)
		if firstDiv != "" {
			t.Logf("   %s", firstDiv)
		}
	}
}

func TestAdvArrayNoGC1000(t *testing.T) {
	var fail int
	var first string
	for seed := int64(1); seed <= 1000; seed++ {
		if _, err := runAdvArraySeed(seed, 3, 12, false, allOps); err != nil {
			fail++
			if first == "" {
				first = fmt.Sprintf("seed %d: %v", seed, err)
			}
		}
	}
	t.Logf("GC OFF all ops: %d/1000 failed  %s", fail, first)
	if fail > 0 {
		t.Errorf("control failed")
	}
}

func TestAdvArrayMinDiverge(t *testing.T) {
	for _, nc := range []int{2, 3} {
		for _, rounds := range []int{2, 3, 4, 5, 6} {
			var div int
			var first string
			for seed := int64(1); seed <= 3000; seed++ {
				_, err := runAdvArraySeed(seed, nc, rounds, true, allOps)
				if err != nil && strings.Contains(err.Error(), "DIVERGED") {
					div++
					if first == "" {
						first = fmt.Sprintf("seed=%d %v", seed, err)
					}
				}
			}
			t.Logf("clients=%d rounds=%d diverged=%d  %s", nc, rounds, div, first)
		}
	}
}

// TestAdvArraySetAnchorPurged is the hand-built distillation of the fuzz
// finding: RGATreeList.insertAfter resolves prevCreatedAt through
// nodeMapByCreatedAt FIRST and falls back to elementMapByCreatedAt. GC deletes
// the nodeMapByCreatedAt entry, so purging the dead position slot a move
// abandoned silently re-points every later ArraySet on that element from the
// dead slot to the element's CURRENT position node -- a different place in the
// list. The successor barrier authorises exactly this purge (the slot's
// successor is the element's own moved node, which minVV covers).
func TestAdvArraySetAnchorPurged(t *testing.T) {
	t.Run("with GC", func(t *testing.T) { runSetAnchorScenario(t, true) })
	t.Run("without GC (control)", func(t *testing.T) { runSetAnchorScenario(t, false) })
}

func runSetAnchorScenario(t *testing.T, gc bool) {
	srv := newAdvServer()
	cs := newAdvClients(t, 2)
	A, B := cs[0], cs[1]

	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b")
		return nil
	}))
	require.NoError(t, srv.sync(A, gc))
	require.NoError(t, srv.sync(B, gc))

	// B self-moves "a": a new position node is stamped with the move ticket
	// and the insert-created slot becomes a dead position node.
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(0, 0)
		return nil
	}))
	require.NoError(t, srv.sync(B, gc))
	require.NoError(t, srv.sync(A, gc))
	t.Logf("A after move: %s", advDumpArr(A.doc))

	// B replaces "a" with "z" (ArraySet), and does NOT push.
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetString(0, "z")
		return nil
	}))
	t.Logf("B after set : %s", advDumpArr(B.doc))

	// A makes its own insert after "a", with a ticket newer than B's set.
	for i := range 4 {
		require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("f%d", i), i)
			return nil
		}))
	}
	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "q")
		return nil
	}))

	// A syncs: minVV covers the move (both rows have it) but not B's set.
	require.NoError(t, srv.sync(A, gc))
	t.Logf("A after GC  : %s", advDumpArr(A.doc))

	// Everything is delivered both ways, repeatedly.
	for range 4 {
		require.NoError(t, srv.sync(B, gc))
		require.NoError(t, srv.sync(A, gc))
	}
	t.Logf("A final: %s", advDumpArr(A.doc))
	t.Logf("B final: %s", advDumpArr(B.doc))

	require.Equal(t,
		B.doc.Root().GetArray("arr").Marshal(),
		A.doc.Root().GetArray("arr").Marshal(),
		"replicas diverged after collecting the dead slot an ArraySet still anchors on")
}

// TestAdvArrayAppendAfterPurgedTail: RGATreeList.Add anchors on a.last, the
// last PHYSICAL position node, tombstones included. So an append issued long
// after a delete legitimately names the deleted node as its prevCreatedAt.
// GC purges that node as soon as minVV covers the delete -- the successor
// barrier returns nil at the tail by construction -- and the append then fails
// to apply on the collecting replica. No concurrency and no move is involved;
// the append strictly follows the delete.
func TestAdvArrayAppendAfterPurgedTail(t *testing.T) {
	srv := newAdvServer()
	cs := newAdvClients(t, 2)
	A, B := cs[0], cs[1]

	require.NoError(t, A.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	require.NoError(t, srv.sync(A, true))
	require.NoError(t, srv.sync(B, true))

	// B deletes the LAST element and pushes it.
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(2)
		return nil
	}))
	require.NoError(t, srv.sync(B, true))

	// A pulls the delete, then syncs again so its stored row covers it.
	require.NoError(t, srv.sync(A, true))
	require.NoError(t, srv.sync(A, true))
	t.Logf("A after GC: %s", advDumpArr(A.doc))
	require.NotContains(t, advDumpArr(A.doc), `"c"`, "A should have purged the tombstone")

	// B appends. Add anchors on a.last == the tombstone A just purged.
	require.NoError(t, B.doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").AddString("x")
		return nil
	}))
	require.NoError(t, srv.sync(B, true))

	// A pulls the append.
	require.NoError(t, srv.sync(A, true), "A could not apply a plain append")
	t.Logf("A=%s B=%s", A.doc.Root().GetArray("arr").Marshal(), B.doc.Root().GetArray("arr").Marshal())
}
