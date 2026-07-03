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
