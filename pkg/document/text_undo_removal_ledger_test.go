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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ledgerPack returns the pending changes of doc after a protobuf round trip,
// so what the wire carries is part of what is tested.
func ledgerPack(t *testing.T, doc *document.Document) *change.Pack {
	t.Helper()

	pb, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(t, err)
	pack, err := converter.FromChangePack(pb)
	require.NoError(t, err)
	return pack
}

// ledgerSend delivers the pending changes of from to each of the given docs.
func ledgerSend(t *testing.T, from *document.Document, to ...*document.Document) {
	t.Helper()

	pack := ledgerPack(t, from)
	for _, doc := range to {
		require.NoError(t, doc.ApplyChangePack(change.NewPack(
			pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
		)))
	}
}

// ledgerAck drops the changes doc has already sent from its local queue.
func ledgerAck(t *testing.T, doc *document.Document) {
	t.Helper()

	pack := doc.CreateChangePack()
	var lastSeq uint32
	if n := len(pack.Changes); n > 0 {
		lastSeq = pack.Changes[n-1].ClientSeq()
	}
	require.NoError(t, doc.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))
}

func ledgerExchange(t *testing.T, a, b *document.Document) {
	t.Helper()

	ledgerSend(t, a, b)
	ledgerSend(t, b, a)
	ledgerAck(t, a)
	ledgerAck(t, b)
}

// ledgerReplicas returns two replicas holding content in the Text under "t".
func ledgerReplicas(t *testing.T, content string) (*document.Document, *document.Document) {
	t.Helper()

	a := document.New("ledger")
	b := document.New("ledger")
	a.SetActor(ledgerActor(t, 1))
	b.SetActor(ledgerActor(t, 2))
	require.NoError(t, a.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, content)
		return nil
	}))
	ledgerExchange(t, a, b)
	return a, b
}

// ledgerFromSnapshot returns a third replica loaded from the snapshot of
// src, as a client that attached late would be.
func ledgerFromSnapshot(t *testing.T, src *document.Document) *document.Document {
	t.Helper()

	bytes, err := converter.SnapshotToBytes(src.RootObject(), nil)
	require.NoError(t, err)
	doc := document.New("ledger")
	doc.SetActor(ledgerActor(t, 3))
	require.NoError(t, doc.ApplyChangePack(change.NewPack(
		"ledger", change.NewCheckpoint(1, 0), nil, src.VersionVector(), bytes,
	)))
	return doc
}

func ledgerActor(t *testing.T, n int) time.ActorID {
	t.Helper()

	actor, err := time.ActorIDFromHex([]string{
		"", "000000000000000000000001", "000000000000000000000002", "000000000000000000000003",
	}[n])
	require.NoError(t, err)
	return actor
}

func ledgerEdit(t *testing.T, doc *document.Document, from, to int, content string) {
	t.Helper()

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(from, to, content)
		return nil
	}))
}

func ledgerText(t *testing.T, doc *document.Document) string {
	t.Helper()

	return textOf(t, doc, "t").String()
}

// ledgerTombstones counts the removed nodes still in the Text under "t".
func ledgerTombstones(t *testing.T, doc *document.Document) int {
	t.Helper()

	n := 0
	for _, node := range textOf(t, doc, "t").Nodes() {
		if node.RemovedAt() != nil {
			n++
		}
	}
	return n
}

func TestTextUndoRemovalLedger(t *testing.T) {
	for _, undoerFirst := range []bool{true, false} {
		name := "deleter first"
		if undoerFirst {
			name = "undoer first"
		}
		t.Run("keeps a concurrent deletion the undo never saw ("+name+")", func(t *testing.T) {
			a, b := ledgerReplicas(t, "abcdefgh")
			// b deletes `d` and undoes it; a concurrently deletes `defgh`.
			ledgerEdit(t, b, 3, 4, "")
			require.NoError(t, b.Undo())
			ledgerEdit(t, a, 3, 8, "")

			if undoerFirst {
				ledgerSend(t, b, a)
				ledgerSend(t, a, b)
			} else {
				ledgerSend(t, a, b)
				ledgerSend(t, b, a)
			}

			// `d` was removed by a's deletion too: b's undo cancels only its own.
			assert.Equal(t, "abc", ledgerText(t, a))
			assert.Equal(t, "abc", ledgerText(t, b))
		})
	}

	t.Run("does not remove text a concurrent undo revives (#2237)", func(t *testing.T) {
		d1, d2 := ledgerReplicas(t, "efgh")
		ledgerEdit(t, d2, 1, 2, "")
		ledgerExchange(t, d1, d2)
		assert.Equal(t, "egh", ledgerText(t, d1))

		// d1 deletes `g` while d2 undoes its deletion of `f`.
		ledgerEdit(t, d1, 1, 2, "")
		require.NoError(t, d2.Undo())
		ledgerExchange(t, d1, d2)

		// d1 saw `f` removed, so its deletion leaves the revived `f` alone.
		assert.Equal(t, "efh", ledgerText(t, d1))
		assert.Equal(t, "efh", ledgerText(t, d2))
	})

	t.Run("cancels the redo, not the original deletion, on the next undo", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		// b: delete `d`, undo, redo, undo. Each undo has to cancel the removal
		// just before it: the second one cancels the redo.
		ledgerEdit(t, b, 3, 4, "")
		require.NoError(t, b.Undo())
		require.NoError(t, b.Redo())
		require.NoError(t, b.Undo())
		assert.Equal(t, "abcdefgh", ledgerText(t, b))
		// a concurrently removes `defgh`, which keeps `d` removed everywhere.
		ledgerEdit(t, a, 3, 8, "")
		ledgerExchange(t, a, b)

		assert.Equal(t, "abc", ledgerText(t, a))
		assert.Equal(t, "abc", ledgerText(t, b))
	})

	t.Run("leaves alone a removed piece the deleter saw removed", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, b, 3, 4, "")
		ledgerExchange(t, a, b)
		// a removes `cef` around the removed `d`; b concurrently undoes.
		ledgerEdit(t, a, 2, 5, "")
		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)

		assert.Equal(t, "abdgh", ledgerText(t, a))
		assert.Equal(t, "abdgh", ledgerText(t, b))
	})

	t.Run("keeps the ledger through a snapshot", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, b, 3, 4, "")
		ledgerEdit(t, a, 3, 8, "")
		ledgerSend(t, a, b)

		// c loads b's snapshot, where `d` is removed by both deletions; it
		// must not forget a's part when b's undo arrives.
		c := ledgerFromSnapshot(t, b)
		require.NoError(t, b.Undo())
		ledgerSend(t, b, a, c)

		assert.Equal(t, "abc", ledgerText(t, a))
		assert.Equal(t, "abc", ledgerText(t, b))
		assert.Equal(t, "abc", ledgerText(t, c))
	})

	t.Run("keeps a piece another deletion still holds out of GC", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, b, 3, 4, "")
		ledgerEdit(t, a, 3, 8, "")
		ledgerExchange(t, a, b)

		// Both deletions are known everywhere. b collects `efgh` but keeps
		// `d`: an undo of either deletion must find it held by the other.
		assert.Positive(t, b.GarbageCollect(b.VersionVector()))
		assert.Equal(t, 1, ledgerTombstones(t, b))

		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)
		assert.Equal(t, "abc", ledgerText(t, a))
		assert.Equal(t, "abc", ledgerText(t, b))

		// Only a's deletion is left on `d`, so it can be collected now.
		assert.Positive(t, b.GarbageCollect(b.VersionVector()))
		assert.Equal(t, 0, ledgerTombstones(t, b))
	})

	t.Run("does not recreate a purged piece a redo never removed", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		// b deletes `d` and undoes it while a deletes `d` too.
		ledgerEdit(t, b, 3, 4, "")
		require.NoError(t, b.Undo())
		ledgerEdit(t, a, 3, 4, "")
		ledgerExchange(t, a, b)
		assert.Equal(t, "abcefgh", ledgerText(t, b))

		// b's redo finds `d` already removed by a, so it records nothing
		// there, and the undo of the redo must not name `d` either: a has
		// collected `d` and would recreate it.
		require.NoError(t, b.Redo())
		ledgerExchange(t, a, b)
		assert.Positive(t, a.GarbageCollect(a.VersionVector()))
		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)

		assert.Equal(t, "abcefgh", ledgerText(t, a))
		assert.Equal(t, "abcefgh", ledgerText(t, b))
	})

	t.Run("revives a deletion nobody else made, also after a snapshot", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, b, 3, 4, "")
		ledgerExchange(t, a, b)
		c := ledgerFromSnapshot(t, a)
		require.NoError(t, b.Undo())
		ledgerSend(t, b, a, c)

		assert.Equal(t, "abcdefgh", ledgerText(t, a))
		assert.Equal(t, "abcdefgh", ledgerText(t, b))
		assert.Equal(t, "abcdefgh", ledgerText(t, c))
	})

	t.Run("redoes only what the redoer saw live", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdef")
		// a and b delete `cd` concurrently; a learns of b's deletion.
		ledgerEdit(t, a, 2, 4, "")
		ledgerEdit(t, b, 2, 4, "")
		ledgerSend(t, b, a)
		ledgerAck(t, b)
		// a undoes and redoes: `cd` stays removed by b's deletion all along,
		// so a's redo removes nothing. b then undoes its deletion.
		require.NoError(t, a.Undo())
		require.NoError(t, a.Redo())
		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)

		// b revived `cd` before a's redo arrived; the redo, which saw `cd`
		// removed, leaves it alone there as it did on a.
		assert.Equal(t, "abcdef", ledgerText(t, a))
		assert.Equal(t, "abcdef", ledgerText(t, b))
	})

	t.Run("propagates an undo that leaves the text as it was", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		// Both delete `a` concurrently; b learns of a's deletion first.
		ledgerEdit(t, a, 0, 1, "")
		ledgerEdit(t, b, 0, 1, "")
		ledgerSend(t, a, b)
		ledgerAck(t, a)

		// b's undo cancels its own deletion while a's still holds `a`, so b's
		// text does not change. Its change has to reach a all the same.
		require.NoError(t, a.Undo())
		require.NoError(t, b.Undo())
		assert.Equal(t, "bcdefgh", ledgerText(t, b))
		ledgerSend(t, b, a)
		ledgerSend(t, a, b)

		assert.Equal(t, "abcdefgh", ledgerText(t, a))
		assert.Equal(t, "abcdefgh", ledgerText(t, b))
	})

	t.Run("redo leaves alone a piece removed in its view", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, a, 0, 1, "")
		ledgerEdit(t, b, 0, 3, "")
		ledgerSend(t, b, a)
		ledgerAck(t, b)
		// b undoes before a hears of it, so in a's view `a` stays removed by
		// b's deletion: a's undo and redo leave it removed, and the redo
		// must not remove it on b, where b's undo already revived it.
		require.NoError(t, b.Undo())
		require.NoError(t, a.Undo())
		require.NoError(t, a.Redo())
		ledgerSend(t, a, b)
		ledgerSend(t, b, a)

		assert.Equal(t, ledgerText(t, a), ledgerText(t, b))
		assert.Equal(t, "abcdefgh", ledgerText(t, a))
	})

	t.Run("recreates a purged piece before the piece that follows it", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefghij")
		ledgerEdit(t, a, 1, 3, "")
		ledgerEdit(t, a, 0, 1, "")
		// b splits `defghij` by removing `e`, so two pieces of the insertion
		// survive after the gap a's undo will recreate.
		ledgerEdit(t, b, 4, 5, "")
		ledgerSend(t, a, b)
		ledgerAck(t, a)
		assert.Positive(t, b.GarbageCollect(b.VersionVector()))

		// a revives `a` and removes `d`. On b, `a` must come back before `d`:
		// placed after a later piece, a's removal would run from `a` to the
		// end of the text.
		require.NoError(t, a.Undo())
		ledgerEdit(t, a, 1, 2, "")
		ledgerExchange(t, a, b)

		assert.Equal(t, "afghij", ledgerText(t, a))
		assert.Equal(t, "afghij", ledgerText(t, b))
	})

	t.Run("keeps a piece until its cancelled removals are known everywhere", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abc")
		c := ledgerFromSnapshot(t, a)
		// a and b remove `b` concurrently; c hears only of a's removal and
		// removes the text around it.
		ledgerEdit(t, a, 1, 2, "")
		ledgerEdit(t, b, 1, 2, "")
		ledgerSend(t, a, c)
		ledgerEdit(t, c, 0, 2, "")

		// a undoes its removal. b learns all of it, so only b's own removal
		// holds `b` there; c still knows nothing of a's undo.
		ledgerSend(t, a, b)
		ledgerAck(t, a)
		require.NoError(t, a.Undo())
		ledgerSend(t, a, b)
		ledgerSend(t, b, a, c)
		ledgerAck(t, b)
		vector := time.MinVersionVector(a.VersionVector(), b.VersionVector(), c.VersionVector())
		b.GarbageCollect(vector)

		// b's undo revives `b`; c's removal, which saw `b` removed by a,
		// must leave it alone on b as on a, whether or not b purged it.
		require.NoError(t, b.Undo())
		ledgerSend(t, b, a, c)
		ledgerSend(t, c, a, b)
		ledgerSend(t, a, c)

		assert.Equal(t, "b", ledgerText(t, a))
		assert.Equal(t, "b", ledgerText(t, b))
		assert.Equal(t, "b", ledgerText(t, c))
	})

	t.Run("links a recreated piece to the nearest surviving neighbours", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefghij")
		// a removes `g`, then `h`; b collects both.
		ledgerEdit(t, a, 6, 7, "")
		ledgerEdit(t, a, 6, 7, "")
		ledgerSend(t, a, b)
		ledgerAck(t, a)
		assert.Positive(t, b.GarbageCollect(b.VersionVector()))

		// b removes `f`, the piece before the gap, and a learns of it.
		ledgerEdit(t, b, 5, 6, "")
		ledgerSend(t, b, a)
		ledgerAck(t, b)

		// a's undo recreates `h` on b, which then collects `f` too: `f` still
		// pointed past the recreated `h`, so purging it must not hand `ij`
		// an insertion predecessor that skips `h`.
		require.NoError(t, a.Undo())
		ledgerSend(t, a, b)
		ledgerAck(t, a)
		assert.Positive(t, b.GarbageCollect(time.MinVersionVector(a.VersionVector(), b.VersionVector())))

		// An edit ending right after `h` resolves through `ij`'s predecessor.
		ledgerEdit(t, a, 5, 6, "")
		ledgerSend(t, a, b)

		assert.Equal(t, "abcdeij", ledgerText(t, a))
		assert.Equal(t, "abcdeij", ledgerText(t, b))
	})

	t.Run("keeps a recreated piece out of a deletion that saw it removed", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abc")
		c := ledgerFromSnapshot(t, a)
		ledgerEdit(t, b, 1, 2, "")
		ledgerSend(t, b, a, c)
		ledgerAck(t, b)
		vector := time.MinVersionVector(a.VersionVector(), b.VersionVector(), c.VersionVector())
		assert.Positive(t, a.GarbageCollect(vector))

		// b undoes; c, which saw `b` removed but not the undo, removes the
		// text around it. a recreates `b` from the undo, so the recreated
		// node has to answer c's view as b's own ledger does.
		require.NoError(t, b.Undo())
		ledgerEdit(t, c, 0, 2, "")
		ledgerSend(t, b, a, c)
		ledgerSend(t, c, a, b)

		assert.Equal(t, "b", ledgerText(t, a))
		assert.Equal(t, "b", ledgerText(t, b))
		assert.Equal(t, "b", ledgerText(t, c))
	})

	t.Run("loads a snapshot whose removal events break the rules", func(t *testing.T) {
		a, _ := ledgerReplicas(t, "abc")
		ledgerEdit(t, a, 1, 2, "")
		bytes, err := converter.SnapshotToBytes(a.RootObject(), nil)
		require.NoError(t, err)

		// Repeat removed_at as an event: no undo can produce that.
		pbSnapshot := &api.Snapshot{}
		require.NoError(t, proto.Unmarshal(bytes, pbSnapshot))
		var removedAt *api.TimeTicket
		for _, node := range pbSnapshot.Root.GetJsonObject().Nodes[0].Element.GetText().Nodes {
			if node.RemovedAt != nil {
				removedAt = node.RemovedAt
				node.RemovalEvents = []*api.TimeTicket{node.RemovedAt}
			}
		}
		require.NotNil(t, removedAt)
		bytes, err = proto.Marshal(pbSnapshot)
		require.NoError(t, err)

		// The document still loads, with removed_at alone.
		obj, _, err := converter.BytesToSnapshot(bytes)
		require.NoError(t, err)
		text := obj.Get("t").(*crdt.Text)
		assert.Equal(t, "ac", text.String())
		for _, node := range text.Nodes() {
			assert.Empty(t, node.RemovalEvents())
		}
	})

	t.Run("drops cancelled removals every replica knows", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		events := func() int {
			n := 0
			for _, node := range textOf(t, a, "t").Nodes() {
				n += len(node.RemovalEvents())
			}
			return n
		}
		for range 5 {
			ledgerEdit(t, a, 0, 8, "")
			require.NoError(t, a.Undo())
			ledgerExchange(t, a, b)
		}
		// Every removal is cancelled, but only a vector both replicas have
		// seen lets the pairs go; until then they decide later deletions.
		assert.Positive(t, events())
		a.GarbageCollect(time.MinVersionVector(a.VersionVector(), b.VersionVector()))
		assert.Zero(t, events())
		assert.Equal(t, "abcdefgh", ledgerText(t, a))
	})

	t.Run("carries a cancelled removal through a snapshot", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		// a deletes `d` and undoes it; the pair stays in the ledger while b
		// has not seen the cancel, so the snapshot has to carry both halves.
		ledgerEdit(t, a, 3, 4, "")
		require.NoError(t, a.Undo())
		cancelled := 0
		for _, node := range textOf(t, a, "t").Nodes() {
			if len(node.RemovalEvents()) > 0 {
				cancelled++
			}
		}
		require.Positive(t, cancelled, "the ledger must hold the cancelled pair")

		c := ledgerFromSnapshot(t, a)
		restored := 0
		for _, node := range textOf(t, c, "t").Nodes() {
			restored += len(node.RemovalEvents())
		}
		assert.Positive(t, restored, "the snapshot must carry the cancel")
		assert.Equal(t, "abcdefgh", ledgerText(t, c))

		// The replica loaded from that snapshot answers a later concurrent
		// deletion as the one that wrote it does: b never saw the undo, so
		// its deletion of `d` holds the piece removed on both.
		ledgerEdit(t, b, 3, 4, "")
		ledgerSend(t, a, b)
		ledgerSend(t, b, a, c)
		assert.Equal(t, "abcefgh", ledgerText(t, a))
		assert.Equal(t, ledgerText(t, a), ledgerText(t, c))
	})

	t.Run("reverses an undo that found nothing left to undo", func(t *testing.T) {
		a, b := ledgerReplicas(t, "ab")
		// b inserts `X`, then deletes `a` and undoes that deletion, leaving
		// one entry on each stack.
		ledgerEdit(t, b, 2, 2, "X")
		ledgerEdit(t, b, 0, 1, "")
		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)
		assert.Equal(t, "abX", ledgerText(t, b))

		// a removes `X`; b's undo of its own insertion then finds nothing
		// live to re-remove, so its reverse names no span.
		ledgerEdit(t, a, 2, 3, "")
		ledgerExchange(t, a, b)
		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)
		assert.Equal(t, "ab", ledgerText(t, b))

		// That reverse still has to be pushed: dropping it would leave the
		// redo stack one entry short, and this redo would re-run the deletion
		// of `a` instead of doing nothing.
		require.NoError(t, b.Redo())
		ledgerExchange(t, a, b)
		assert.Equal(t, "ab", ledgerText(t, b))
		assert.Equal(t, "ab", ledgerText(t, a))
	})
}
