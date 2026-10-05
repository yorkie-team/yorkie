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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestSetOnForgedIdentityCollision pins that a local Set stays a plain Set
// when the document it runs against has been shaped to make it lose.
//
// The shape takes two things a peer controls, because an operation's tickets
// are not validated off the wire (tracked at the push boundary in
// yorkie-team/yorkie#2081): a member planted under the createdAt this client
// will mint next -- predictable, since it is the next delimiter of the next
// change ID -- and an occupant of the target key positioned far in the
// future, which makes the fresh ticket lose the LWW comparison. Together
// they are the only way a locally created value reaches ElementRHT's loser
// branch at all.
//
// Reaching it must not be fatal. json.Object.setInternal has by then handed
// the caller a proxy for the value, so it has nothing to do with a refusal
// but panic -- and a panic here escapes Document.Update past
// invalidateClone, taking down a server that runs json proxies over a
// document rebuilt from stored client changes (server/revisions,
// server/documents). The refusal belongs to the restore path, which has a
// caller able to skip the operation; crdt.Object.Set does not refuse.
func TestSetOnForgedIdentityCollision(t *testing.T) {
	newRoot := func() *crdt.Root {
		return crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	}

	// The context issues tickets off change.InitialID, so an identical one
	// answers with the ticket the Set below will mint.
	forged := change.NewContext(change.InitialID(), "", newRoot()).IssueTimeTicket()

	root := newRoot()
	ctx := change.NewContext(change.InitialID(), "", root)

	planted, err := crdt.NewPrimitive("planted", forged)
	require.NoError(t, err)
	root.Object().Set("planted", planted)
	root.RegisterElement(planted, root.Object())

	occupant, err := crdt.NewPrimitive("occupant", time.NewTicket(1<<40, 0, time.InitialActorID))
	require.NoError(t, err)
	root.Object().SetWithExecutedAt("k", occupant, crdt.PositionedAt(occupant))
	root.RegisterElement(occupant, root.Object())

	obj := json.NewObject(ctx, root.Object())
	require.NotPanics(t, func() { obj.SetString("k", "v") },
		"a local Set crashed on document state a peer can shape")

	// The planted member keeps the key it was planted under: losing the LWW
	// comparison is what the loser branch is for, and it leaves the key's
	// occupant alone.
	assert.Same(t, occupant, root.Object().Get("k"))
}

// TestLocalSetRefusedOnForgedIdentityDropsClone pins what happens when the
// same shape is reached through Document.Update, where a local edit has two
// apply targets under two different contracts: the updater mutates the clone
// through crdt.Object.Set, which never refuses, while the operation it pushes
// reaches the root through operations.Set.Execute, which can.
//
// The root refuses the value here, so the clone is the only copy holding it --
// and worse, holding it in the createdAt slot the planted member needs, which
// is the only way DeepCopy, purge and GC address that member. Reporting the
// refusal as ErrOperationSkipped would have Change.Execute swallow it and
// Document.Update return nil, leaving that clone to serve every later read
// and every later edit. The update has to fail instead, so document.go drops
// the clone and rebuilds it from the root.
func TestLocalSetRefusedOnForgedIdentityDropsClone(t *testing.T) {
	doc := document.New("forged-identity-local-set")
	root := doc.InternalDocumentForTest().Root()

	// The document's changes are issued off change.InitialID(), so an
	// identical context answers with the ticket the Update below will mint.
	forged := change.NewContext(change.InitialID(), "",
		crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))).IssueTimeTicket()

	planted, err := crdt.NewPrimitive("planted", forged)
	require.NoError(t, err)
	root.Object().Set("planted", planted)
	root.RegisterElement(planted, root.Object())

	occupant, err := crdt.NewPrimitive("occupant", time.NewTicket(1<<40, 0, time.InitialActorID))
	require.NoError(t, err)
	root.Object().SetWithExecutedAt("k", occupant, crdt.PositionedAt(occupant))
	root.RegisterElement(occupant, root.Object())

	before := root.Object().Marshal()

	err = doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "v")
		return nil
	})
	assert.ErrorIs(t, err, operations.ErrRefusedLocalSet,
		"a Set the root refused was reported to the caller as applied")
	assert.Equal(t, before, root.Object().Marshal())

	// The clone the updater mutated is gone: what the next read rebuilds from
	// the root still indexes the planted member under its own createdAt,
	// rather than the refused value that overwrote that slot on the clone.
	nodeValues := func(obj *crdt.Object) []string {
		var out []string
		for _, node := range obj.RHTNodes() {
			out = append(out, node.Element().Marshal())
		}
		slices.Sort(out)
		return out
	}
	assert.Equal(t, nodeValues(root.Object()), nodeValues(doc.Root().Object),
		"the clone kept a member the root refused")
}

// TestLocalSetAfterRemoteRebuildKeepsApplying pins that ErrRefusedLocalSet
// stays out of reach of a document rebuilt from well-formed peer changes --
// the shape the server drives json proxies over, where it rebuilds from the
// stored change log (packs.BuildDocForCheckpoint) and then runs a local
// Update over the result.
//
// A local Set reaches ElementRHT's loser branch only when its freshly minted
// ticket does not come after the occupant of the key it targets, and a
// rebuild leaves the local clock past every ticket it applied: ApplyChanges
// advances the lamport once per applied change (change.ID.SyncClocks, and
// SyncLamport for the GC-disabled attachment), and the change the Update
// issues takes it one further (change.ID.Next). So an overwrite after a
// rebuild wins its key whatever the peers did, and the refusal needs a ticket
// no SDK mints -- see TestLocalSetRefusedOnForgedIdentityDropsClone for the
// shape that does reach it.
func TestLocalSetAfterRemoteRebuildKeepsApplying(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	peer := document.New("local-set-after-rebuild")
	peer.SetActor(actorA)
	require.NoError(t, peer.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "peer")
		r.SetNewObject("nested").SetString("k", "peer")
		return nil
	}))
	require.NoError(t, peer.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "peer-again")
		r.Delete("nested")
		return nil
	}))

	rebuilt := document.New("local-set-after-rebuild")
	pack := peer.CreateChangePack()
	pack.VersionVector.Set(rebuilt.ActorID(), rebuilt.VersionVector().VersionOf(rebuilt.ActorID()))
	require.NoError(t, rebuilt.ApplyChangePack(pack))
	// The server drives the proxies under InitialActorID, as
	// packs.BuildDocForCheckpoint hands them over.
	rebuilt.SetActor(time.InitialActorID)

	// Repeated, because each round overwrites keys the previous round minted
	// locally: a later local ticket has to keep winning against an earlier one.
	for range 3 {
		require.NoError(t, rebuilt.Update(func(r *json.Object, _ *presence.Presence) error {
			var keys []string
			for key := range r.Object.Members() {
				keys = append(keys, key)
			}
			for _, key := range keys {
				r.Delete(key)
			}

			r.SetString("k", "server")
			r.SetNewObject("nested").SetString("k", "server")
			return nil
		}), "a local Set over a document rebuilt from peer changes was refused")
	}

	assert.Equal(t, `{"k":"server","nested":{"k":"server"}}`, rebuilt.Marshal())
}
