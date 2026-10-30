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
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// receiveEvent reads one event from the document event channel or fails
// the test after a timeout, so a missing emission does not hang the run.
func receiveEvent(t *testing.T, doc *document.Document) document.DocEvent {
	t.Helper()
	select {
	case e := <-doc.Events():
		return e
	case <-gotime.After(gotime.Second):
		t.Fatal("expected an event on doc.Events(), got none")
		return document.DocEvent{}
	}
}

// assertNoEvent asserts that no event is pending on the document event
// channel.
func assertNoEvent(t *testing.T, doc *document.Document) {
	t.Helper()
	select {
	case e := <-doc.Events():
		t.Fatalf("expected no event, got %s", e.Type)
	default:
	}
}

// presencePack builds a change pack carrying the peer's initial presence,
// with the version vector adjusted for the receiver as in real delivery.
func presencePack(t *testing.T, from, to *document.Document) *change.Pack {
	t.Helper()
	pack := from.CreateChangePack()
	pCopy := *pack
	pCopy.VersionVector = pack.VersionVector.DeepCopy()
	pCopy.VersionVector.Set(to.ActorID(),
		to.VersionVector().VersionOf(to.ActorID()))
	return &pCopy
}

// TestPresenceEventSinglePath pins the contract from
// docs/design/doc-presence.md ("Presence Event Delivery Unification"):
// AddOnlineClientAndReconcile and RemoveOnlineClientAndReconcile emit
// their reconciled events through the document event channel, so watched
// and unwatched are delivered in state-transition order regardless of
// whether the peer's presence or watch signal arrives first. Previously
// the watch-stream path returned events to the caller, which raced with
// the sync path on the client watch response channel (yorkie#1847).
func TestPresenceEventSinglePath(t *testing.T) {
	newPeers := func(t *testing.T) (observer, peer *document.Document) {
		observerActor, err := time.ActorIDFromHex("000000000000000000000001")
		assert.NoError(t, err)
		peerActor, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)

		observer = document.New("single-path")
		observer.SetActor(observerActor)

		peer = document.New("single-path")
		peer.SetActor(peerActor)
		assert.NoError(t, peer.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("name", "peer")
			return nil
		}))
		return observer, peer
	}

	t.Run("presence before watch", func(t *testing.T) {
		observer, peer := newPeers(t)

		// 01. Peer's presence syncs first: both parts are not ready yet,
		// so no event is emitted (waiting for online).
		assert.NoError(t, observer.ApplyChangePack(presencePack(t, peer, observer)))
		assertNoEvent(t, observer)

		// 02. The watch signal arrives: the reconciled watched event must
		// come through the document event channel.
		observer.AddOnlineClientAndReconcile(peer.ActorID().String())
		e := receiveEvent(t, observer)
		assert.Equal(t, document.WatchedEvent, e.Type)

		// 03. The unwatch signal arrives: same channel, after watched.
		observer.RemoveOnlineClientAndReconcile(peer.ActorID().String())
		e = receiveEvent(t, observer)
		assert.Equal(t, document.UnwatchedEvent, e.Type)
	})

	t.Run("watch before presence", func(t *testing.T) {
		observer, peer := newPeers(t)

		// 01. The watch signal arrives first: no presence data yet, so no
		// event is emitted (waiting for presence).
		observer.AddOnlineClientAndReconcile(peer.ActorID().String())
		assertNoEvent(t, observer)

		// 02. Peer's presence syncs: the watched event is emitted by the
		// apply path through the same channel.
		assert.NoError(t, observer.ApplyChangePack(presencePack(t, peer, observer)))
		e := receiveEvent(t, observer)
		assert.Equal(t, document.WatchedEvent, e.Type)

		// 03. The unwatch signal arrives: unwatched must follow watched on
		// the same channel — this ordering was racy before unification.
		observer.RemoveOnlineClientAndReconcile(peer.ActorID().String())
		e = receiveEvent(t, observer)
		assert.Equal(t, document.UnwatchedEvent, e.Type)
	})
}

// clearChange builds a presence-clear change the way the server's
// DetachDocument does on a client's behalf.
func clearChange(clientSeq uint32, actorID time.ActorID) *change.Change {
	ctx := change.NewContext(
		change.NewID(clientSeq, 0, time.InitialLamport, actorID, nil),
		"",
		nil,
	)
	presence.New(ctx, presence.NewData()).Clear()
	return ctx.ToChange()
}

// TestRedundantPresenceClear pins the premise of the server-side detach
// (yorkie#2290): the server cannot tell which of a client's two identities it
// stamped, so it writes one clear per identity, and the one stamped with the
// identity that holds no presence must delete nothing and publish no event.
// The session-ID clear goes first; it is the redundant one for a JS SDK
// client, the stable-actor clear is for a Go SDK client.
func TestRedundantPresenceClear(t *testing.T) {
	otherActor, err := time.ActorIDFromHex("000000000000000000000003")
	assert.NoError(t, err)

	for _, tc := range []struct {
		name           string
		redundantFirst bool
	}{
		{"redundant clear first", true},
		{"redundant clear second", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observerActor, err := time.ActorIDFromHex("000000000000000000000001")
			assert.NoError(t, err)
			peerActor, err := time.ActorIDFromHex("000000000000000000000002")
			assert.NoError(t, err)

			observer := document.New("redundant-clear")
			observer.SetActor(observerActor)
			peer := document.New("redundant-clear")
			peer.SetActor(peerActor)
			assert.NoError(t, peer.Update(func(root *json.Object, p *presence.Presence) error {
				p.Set("name", "peer")
				return nil
			}))

			// 01. The peer is watched with its presence.
			assert.NoError(t, observer.ApplyChangePack(presencePack(t, peer, observer)))
			observer.AddOnlineClientAndReconcile(peerActor.String())
			assert.Equal(t, document.WatchedEvent, receiveEvent(t, observer).Type)

			// 02. The server detaches the peer: one clear per identity.
			changes := []*change.Change{clearChange(1, peerActor), clearChange(2, otherActor)}
			if tc.redundantFirst {
				changes = []*change.Change{clearChange(1, otherActor), clearChange(2, peerActor)}
			}
			vv := time.NewVersionVector()
			vv.Set(observerActor, observer.VersionVector().VersionOf(observerActor))
			pack := change.NewPack(observer.Key(), change.InitialCheckpoint, changes, vv, nil)

			// The event channel has capacity one, so a second event would
			// block ApplyChangePack; run it aside to fail instead of hang.
			done := make(chan error, 1)
			go func() { done <- observer.ApplyChangePack(pack) }()

			// 03. Exactly one unwatched event, for the peer's own identity.
			e := receiveEvent(t, observer)
			assert.Equal(t, document.UnwatchedEvent, e.Type)
			assert.Contains(t, e.Presences, peerActor.String())
			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-gotime.After(gotime.Second):
				t.Fatal("the redundant clear published a second event")
			}
			assertNoEvent(t, observer)

			assert.NotContains(t, observer.AllPresences(), peerActor.String())
			assert.NotContains(t, observer.AllPresences(), otherActor.String())
		})
	}
}
