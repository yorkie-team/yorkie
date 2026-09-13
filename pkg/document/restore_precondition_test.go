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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/test/helper"
)

// newReplica builds a document with an explicit actor, so lamport clocks and
// version vectors of the two replicas are distinguishable. Without it both
// documents share time.InitialActorID and every concurrency comparison in
// these scenarios is meaningless.
func newReplica(t *testing.T, k, hex string) *document.Document {
	t.Helper()
	actor, err := time.ActorIDFromHex(hex)
	require.NoError(t, err)
	d := document.New(key.Key(k))
	d.SetActor(actor)
	return d
}

// takeChanges pulls the sender's pending changes and acks them, returning the
// list the server would have stored. Separated from delivery so a test can
// hold a pack back and choose the order in which the two replicas see it.
func takeChanges(t *testing.T, from *document.Document) []*change.Change {
	t.Helper()
	pack := from.CreateChangePack()
	require.NoError(t, from.ApplyChangePack(change.NewPack(
		pack.DocumentKey,
		pack.Checkpoint,
		nil,
		time.InitialVersionVector,
		nil,
	)))
	return pack.Changes
}

func deliver(t *testing.T, to *document.Document, changes []*change.Change) {
	t.Helper()
	require.NoError(t, to.ApplyChangePack(change.NewPack(
		"divergence",
		change.NewCheckpoint(0, 0),
		changes,
		time.InitialVersionVector,
		nil,
	)))
}

// replay rebuilds the document the way the server does: from the stored change
// log, with OpSourceReplay (internal_document.go:183-188). Its result is what
// every snapshot -- and so every client that later loads one -- will hold.
func replay(t *testing.T, changeLists ...[]*change.Change) *document.InternalDocument {
	t.Helper()
	var all []*change.Change
	for _, list := range changeLists {
		all = append(all, list...)
	}
	doc := document.NewInternalDocument("divergence")
	require.NoError(t, doc.ApplyChangePack(change.NewPack(
		"divergence",
		change.NewCheckpoint(0, 0),
		all,
		time.InitialVersionVector,
		nil,
	), false))
	return doc
}

// TestRestoreTombstoneIsNotReliablyPresent measures the precondition of the
// cheapest candidate fix -- reviving the tombstone in place instead of
// installing the copy, using only state the replica already has.
//
// It is not there to act on. A replica that collected between receiving the
// removal and receiving the undo has purged it, and a replica that joined by
// loading a snapshot taken after that collection never had it. Whether a
// replica can revive is therefore a function of its local collection timing,
// not of the change log, so "revive when the tombstone is there" would produce
// different documents on different replicas from the same log.
func TestRestoreTombstoneIsNotReliablyPresent(t *testing.T) {
	d1 := newReplica(t, "divergence", "000000000000000000000001")
	d2 := newReplica(t, "divergence", "000000000000000000000002")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("c").SetString("a", "1")
		return nil
	}))
	create := takeChanges(t, d1)
	deliver(t, d2, create)
	createdAt := d1.RootObject().Get("c").CreatedAt()

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("c")
		return nil
	}, "remove c"))
	remove := takeChanges(t, d1)
	require.NoError(t, d1.Undo())
	undo := takeChanges(t, d1)

	// The removal and the undo are separate changes, so they can be pushed in
	// separate packs -- a user deleting and then hitting undo across one
	// sync interval. The server collects on pushpull (packs/pushpull.go:531),
	// so a collection pass can fall between them.
	server := replay(t, create, remove)
	assert.NotNil(t, server.Root().FindByCreatedAt(createdAt),
		"the tombstone is there before collection")

	collected, err := server.GarbageCollect(
		helper.MaxVersionVector(d1.ActorID(), d2.ActorID()))
	require.NoError(t, err)
	assert.Positive(t, collected)
	assert.Nil(t, server.Root().FindByCreatedAt(createdAt),
		"the same replica has nothing left to revive once it has collected")

	// Applying the undo after that collection is legal and produces the copy,
	// because there is no longer anything else it could produce.
	require.NoError(t, server.ApplyChangePack(change.NewPack(
		"divergence", change.NewCheckpoint(0, 0), undo,
		time.InitialVersionVector, nil), false))
	assert.Equal(t, `{"c":{"a":"1"}}`, server.Marshal())
}
