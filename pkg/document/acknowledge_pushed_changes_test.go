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
)

// TestAcknowledgePushedChanges pins that the reply to a push-only request is
// taken as a push ack only: the confirmed local changes go, the client seq
// moves, and nothing else of the pack is applied -- above all not its version
// vector, which garbage collection would otherwise use.
func TestAcknowledgePushedChanges(t *testing.T) {
	newDoc := func(t *testing.T) *document.Document {
		d := document.New("ack-pushed-changes")
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewText("t").Edit(0, 0, "abc")
			return nil
		}))
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetText("t").Edit(1, 2, "")
			return nil
		}))
		require.Positive(t, d.GarbageLen())
		return d
	}

	// ackPack is a reply that acknowledges up to clientSeq and carries a
	// version vector under which every tombstone of d is collectable.
	ackPack := func(d *document.Document, clientSeq uint32) *change.Pack {
		return &change.Pack{
			DocumentKey:   d.Key(),
			Checkpoint:    change.NewCheckpoint(5, clientSeq),
			VersionVector: d.VersionVector().DeepCopy(),
		}
	}

	t.Run("drops acked local changes without collecting", func(t *testing.T) {
		d := newDoc(t)
		garbage := d.GarbageLen()

		d.AcknowledgePushedChanges(ackPack(d, 1))
		assert.True(t, d.HasLocalChanges(), "the change after the ack stays")
		assert.Equal(t, change.NewCheckpoint(0, 1), d.Checkpoint())
		assert.Equal(t, garbage, d.GarbageLen())

		pack := ackPack(d, 2)
		d.AcknowledgePushedChanges(pack)
		assert.False(t, d.HasLocalChanges())
		assert.Equal(t, change.NewCheckpoint(0, 2), d.Checkpoint())
		assert.Equal(t, garbage, d.GarbageLen())

		// The vector was collectable: only the ack path left it alone.
		assert.Positive(t, d.GarbageCollect(pack.VersionVector))
	})

	t.Run("takes the removal flag", func(t *testing.T) {
		d := newDoc(t)
		pack := ackPack(d, 2)
		pack.IsRemoved = true

		d.AcknowledgePushedChanges(pack)
		assert.Equal(t, document.StatusRemoved, d.Status())
	})
}
