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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// grab takes a document's pending local changes through the protobuf
// converter and returns them, so they can be replayed into other documents in
// any order.
func grab(t *testing.T, d *document.Document) []*change.Change {
	t.Helper()

	pack := d.CreateChangePack()
	pb, err := converter.ToChangePack(pack)
	require.NoError(t, err)
	wired, err := converter.FromChangePack(pb)
	require.NoError(t, err)

	// Self-ack so the next grab does not resend these.
	var lastSeq uint32
	if len(pack.Changes) > 0 {
		lastSeq = pack.Changes[len(pack.Changes)-1].ClientSeq()
	}
	require.NoError(t, d.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))

	return wired.Changes
}

func feed(t *testing.T, d *document.Document, changes []*change.Change) {
	t.Helper()
	require.NoError(t, d.ApplyChangePack(change.NewPack(
		"d", change.NewCheckpoint(0, 0), changes, time.InitialVersionVector, nil,
	)))
}

func newActor(t *testing.T, hex string) *document.Document {
	t.Helper()
	a, err := time.ActorIDFromHex(hex)
	require.NoError(t, err)
	d := document.New("d")
	d.SetActor(a)
	return d
}

// Two clients delete the same run concurrently; a third, which has seen only
// one of the two deletions, styles a range covering it. This is the case that
// forces canStyle not to read removedAt.
//
// removedAt is last-writer-wins and MUTABLE -- Remove overwrites it when a
// removal the node has not seen arrives with a later ticket -- while a style
// is evaluated once, when it arrives. So any predicate over removedAt gets a
// different answer depending on which of the two removals has landed, and
// delivery order B,S,C disagrees with the other three. Storing more removal
// tickets on the node does not help: the replica cannot know which of them
// the styler had seen, because it may not hold that one yet. Converging
// Remove on the earliest concurrent tombstone does not help either -- the
// style can still be applied before the earliest one arrives.
//
// S causally depends on B -- X applied B before styling -- so an order that
// delivers S first is not legal and is not replayed. The three below are, and
// they have to agree on the attributes AND on both halves of the ledger.
func TestTwoConcurrentRemovalsThenStyle(t *testing.T) {

	seed := newActor(t, "000000000000000000000009")
	require.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	p0 := grab(t, seed)

	docB := newActor(t, "000000000000000000000001")
	docC := newActor(t, "000000000000000000000002")
	docX := newActor(t, "000000000000000000000003")
	for _, d := range []*document.Document{docB, docC, docX} {
		feed(t, d, p0)
	}

	require.NoError(t, docB.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}))
	pB := grab(t, docB)

	require.NoError(t, docC.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}))
	pC := grab(t, docC)

	// X knows B's removal but not C's.
	feed(t, docX, pB)
	require.NoError(t, docX.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Style(0, 8, map[string]string{"b": "1"})
		return nil
	}))
	pS := grab(t, docX)

	orders := []struct {
		name string
		seq  [][]*change.Change
	}{
		{"C,B,S", [][]*change.Change{pC, pB, pS}},
		{"B,S,C", [][]*change.Change{pB, pS, pC}},
		{"B,C,S", [][]*change.Change{pB, pC, pS}},
	}

	var first []string
	for _, o := range orders {
		d := newActor(t, "00000000000000000000000a")
		feed(t, d, p0)
		for _, batch := range o.seq {
			feed(t, d, batch)
		}
		got := append(nodeAttrs(t, d, "t"),
			fmt.Sprintf("live=%+v gc=%+v gcLen=%d",
				d.DocSize().Live, d.DocSize().GC, d.GarbageLen()))
		t.Logf("%-6s %v", o.name, got)
		if first == nil {
			first = got
			continue
		}
		require.Equal(t, first, got, "delivery order %s diverges", o.name)
	}
}
