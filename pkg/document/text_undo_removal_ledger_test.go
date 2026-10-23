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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ledgerPack returns the pending changes of doc after a protobuf round trip,
// so the restore spans' removed_by is part of what is tested.
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

	t.Run("does not recreate a purged piece another deletion still holds", func(t *testing.T) {
		a, b := ledgerReplicas(t, "abcdefgh")
		ledgerEdit(t, b, 3, 4, "")
		ledgerEdit(t, a, 3, 8, "")
		ledgerExchange(t, a, b)

		// Both deletions are known everywhere: b collects `d` before undoing.
		assert.Positive(t, b.GarbageCollect(b.VersionVector()))
		purged := 0
		textOf(t, b, "t").RGATreeSplit().PurgedLedgers(
			func(_ *time.Ticket, entries []*crdt.PurgedLedger) { purged += len(entries) },
		)
		assert.Positive(t, purged)

		require.NoError(t, b.Undo())
		ledgerExchange(t, a, b)

		assert.Equal(t, "abc", ledgerText(t, a))
		assert.Equal(t, "abc", ledgerText(t, b))
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
}
