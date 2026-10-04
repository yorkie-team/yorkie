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
	"errors"
	"testing"
	gotime "time"

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

// TestCloneResetOnFailedApply covers a remote change that fails partway.
// Change.Execute does not roll back, so the clone keeps the operations that
// ran before the failure while the root, executed second, never sees them.
// The clone must be dropped so it is rebuilt from the root.
func TestCloneResetOnFailedApply(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	source := document.New("d")
	source.SetActor(actor)
	require.NoError(t, source.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("k", "v")
		return nil
	}))
	c := source.CreateChangePack().Changes[0]

	// Append an operation whose parent is the string the first operation set
	// rather than an object, so the change fails after that operation already
	// ran. A parent that does not resolve at all no longer fails: an
	// unreachable createdAt is a replica that saw history in another order,
	// which operations skip (see skipUnresolvedTarget), so it would not
	// exercise the partway failure this test is about.
	set, ok := c.Operations()[0].(*operations.Set)
	require.True(t, ok)
	value, err := crdt.NewPrimitive("x", time.NewTicket(101, 0, actor))
	require.NoError(t, err)
	ops := append(c.Operations(), operations.NewSet(set.Value().CreatedAt(), "x", value, value.CreatedAt()))
	broken := change.New(c.ID(), "", ops, nil)

	target := document.New("d")
	pack := change.NewPack("d", change.InitialCheckpoint, []*change.Change{broken}, nil, nil)
	assert.Error(t, target.ApplyChangePack(pack))

	// Root() reads the clone and Marshal() the root; they must agree.
	assert.Equal(t, target.Marshal(), target.Root().Marshal())
}

// TestCloneResetOnFailedUpdate covers the same invariant on the local path:
// an updater that fails leaves the clone holding operations the root never
// took, so the next read must see a clone rebuilt from the root rather than
// the diverged one -- and must not panic on a clone that was dropped.
func TestCloneResetOnFailedUpdate(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("k", "v")
		return nil
	}))

	assert.Error(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("dropped", "x")
		return errors.New("boom")
	}))

	assert.Equal(t, doc.Marshal(), doc.Root().Marshal())
	assert.Equal(t, `{"k":"v"}`, doc.Marshal())
}

// TestRootDoesNotBlockOnUndrainedEvents pins the lock/channel ordering: the
// document lock must never be held across a send to the event channel. The
// channel holds one event, so an application that has not drained it leaves
// the second send of a pack blocked indefinitely -- and if that send held
// d.mu, every reader of the document would block behind it. Manual-sync
// documents have no pump draining the channel at all, so this is the normal
// case for them rather than a pathological one.
func TestRootDoesNotBlockOnUndrainedEvents(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)

	// Two presence changes from one actor produce two events: the first is
	// buffered, the second blocks until something drains the channel.
	source := document.New("d")
	source.SetActor(actor)
	for _, v := range []string{"one", "two"} {
		require.NoError(t, source.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("k", v)
			return nil
		}))
	}
	changes := source.CreateChangePack().Changes
	require.Len(t, changes, 2)

	target := document.New("d")
	target.SetOnlineClients(actor.String())
	pack := change.NewPack("d", change.InitialCheckpoint, changes, nil, nil)

	applied := make(chan error, 1)
	go func() { applied <- target.ApplyChangePack(pack) }()

	// Once the first event is buffered the applier is at, or about to reach,
	// the send that cannot complete until this test drains the channel.
	require.Eventually(t, func() bool {
		return len(target.Events()) == 1
	}, 5*gotime.Second, gotime.Millisecond)

	read := make(chan string, 1)
	go func() { read <- target.Root().Marshal() + target.Marshal() }()
	select {
	case <-read:
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("Root() blocked behind an undrained event channel")
	}

	<-target.Events()
	<-target.Events()
	require.NoError(t, <-applied)
}

// TestPresenceRollbackOnFailedUpdate covers a presence edit made by an
// updater that then fails. The clone's presence map shares its values with
// the root's until the updater is handed a copy it owns, so without that copy
// presence.Set writes straight through and no clone reset can undo it.
func TestPresenceRollbackOnFailedUpdate(t *testing.T) {
	doc := document.New("d")
	actorID := doc.ActorID().String()
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Set("color", "blue")
		return nil
	}))
	require.Equal(t, "blue", doc.PresenceForTest(actorID)["color"])

	boom := errors.New("boom")
	require.ErrorIs(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Set("color", "red")
		return boom
	}), boom)

	// The root kept the committed value, and the rebuilt clone serves it back
	// to the next updater rather than the abandoned one.
	assert.Equal(t, "blue", doc.PresenceForTest(actorID)["color"])
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Set("shape", "circle")
		return nil
	}))
	assert.Equal(t, "blue", doc.PresenceForTest(actorID)["color"])
}

// TestChangePackOwnsVersionVectors covers the version vector each buffered
// local change carries. Context.NextID hands the document the very ID the
// change holds, so without a copy the pack the sync goroutine serializes
// would range over the map a concurrent remote apply is writing in place.
func TestChangePackOwnsVersionVectors(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)

	doc := document.New("d")
	doc.SetActor(actor)
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("k", "v")
		return nil
	}))

	packed := doc.CreateChangePack().Changes[0].ID().VersionVector()
	require.NotEmpty(t, packed)

	// Mutating the document's own vector in place, as SyncClocks does while a
	// remote pack is applied, must not reach the packed change.
	doc.InternalDocumentForTest().VersionVector().Set(actor, 99)
	assert.NotEqual(t, int64(99), packed[actor])
}
