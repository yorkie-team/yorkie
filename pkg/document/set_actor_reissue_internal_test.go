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

package document

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestSetActorWithReissueInternal(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	newFilled := func(t *testing.T) *Document {
		doc := New("reissue-internal")
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("t").Edit(0, 0, "hello")
			p.Set("k", "v")
			return nil
		}))
		return doc
	}

	t.Run("a failed re-issue leaves the document untouched", func(t *testing.T) {
		doc := newFilled(t)
		before := doc.Marshal()
		root, presences := doc.doc.root, doc.doc.presences

		// A Remove whose parent no replica has: the replay of it fails.
		lamport := doc.doc.Lamport() + 1
		ghost := operations.NewRemove(
			time.NewTicket(99, 1, time.InitialActorID),
			time.NewTicket(98, 1, time.InitialActorID),
			time.NewTicket(lamport, 1, time.InitialActorID),
		)
		bad := change.New(
			change.NewID(2, 0, lamport, time.InitialActorID, doc.doc.VersionVector().DeepCopy()),
			"", []operations.Operation{ghost}, nil,
		)
		doc.doc.localChanges = append(doc.doc.localChanges, bad)
		changes := doc.doc.localChanges

		assert.Error(t, doc.SetActorWithOptions(actor, WithReissue()))
		assert.Equal(t, time.InitialActorID, doc.ActorID())
		assert.Equal(t, before, doc.Marshal())
		assert.Same(t, root, doc.doc.root)
		assert.Same(t, presences, doc.doc.presences)
		assert.Equal(t, changes, doc.doc.localChanges)
		assert.True(t, doc.CanUndo())
	})

	t.Run("a re-issue does not write into a deep copy", func(t *testing.T) {
		doc := newFilled(t)
		copied, err := doc.doc.DeepCopy()
		require.NoError(t, err)
		copiedChange := copied.localChanges[0]
		copiedActor := copiedChange.ID().ActorID()

		require.NoError(t, doc.SetActorWithOptions(actor, WithReissue()))

		assert.Equal(t, actor, doc.doc.localChanges[0].ID().ActorID())
		assert.Same(t, copiedChange, copied.localChanges[0])
		assert.Equal(t, copiedActor, copied.localChanges[0].ID().ActorID())
		assert.Equal(t, time.InitialActorID, copied.ActorID())
	})

	t.Run("a deep copy keeps the absorbed-remote guard", func(t *testing.T) {
		doc := newFilled(t)
		doc.doc.absorbedRemote = true
		copied, err := doc.doc.DeepCopy()
		require.NoError(t, err)
		assert.True(t, copied.absorbedRemote)
		assert.False(t, copied.neverSynced())
	})
}
