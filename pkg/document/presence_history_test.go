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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestPresenceHistoryOrdering pins what undo restores when a single Update
// touches the same presence key more than once, or clears the presence
// alongside a tracked set.
//
// What undo restores is the value the key held when the Update began -- JS
// takes that snapshot once, at ChangeContext construction (context.ts:69), so
// nothing done inside the closure can move it. Go records the same value per
// key, on the first mutation the presence proxy makes to that key, which is
// equivalent only because the proxy is the sole writer of the presence during
// an Update. Every case below is one where a value observed at some later
// point inside the closure would differ from the value at its start.
func TestPresenceHistoryOrdering(t *testing.T) {
	// newDoc returns a document seeded with the given presence in a change of
	// its own, so the seeded values are what the next Update sees at its
	// start. Nothing is marked undoable, so the seed leaves the undo stack
	// empty and every assertion below is about the Update that follows it.
	newDoc := func(t *testing.T, seed presence.Data) (*document.Document, func() presence.Data) {
		doc := document.New("d1")
		if len(seed) > 0 {
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				for key, value := range seed {
					p.Set(key, value)
				}
				return nil
			}))
		}
		require.Equal(t, 0, doc.UndoStackLenForTest())
		return doc, func() presence.Data {
			return doc.PresenceForTest(doc.ActorID().String())
		}
	}

	t.Run("plain set before a tracked set of the same key test", func(t *testing.T) {
		// The plain set moves the key before the tracked one records
		// anything. Undo must still restore "red": recording the value seen
		// by the tracked set would restore "blue", a value that only ever
		// existed midway through this closure and that no observer saw.
		doc, myPresence := newDoc(t, presence.Data{"color": "red"})

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "blue")
			p.Set("color", "green", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "green"}, myPresence())

		require.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": "red"}, myPresence())
	})

	t.Run("tracked set before a plain set of the same key test", func(t *testing.T) {
		// Only the last set of a key decides whether it is undoable, so the
		// plain set opts the key back out and the change becomes
		// untracked entirely -- nothing reaches the undo stack
		// (context.ts:186-198).
		doc, myPresence := newDoc(t, presence.Data{"color": "red"})

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "blue", presence.WithHistory())
			p.Set("color", "green")
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "green"}, myPresence())

		assert.Equal(t, 0, doc.UndoStackLenForTest())
		assert.False(t, doc.CanUndo())
	})

	t.Run("tracked set of a key absent before the update test", func(t *testing.T) {
		// The key held nothing when the Update began, so undo removes it
		// rather than restoring the empty string -- and the plain set that
		// precedes it, which is what actually introduces the key, must not
		// turn the recorded absence into a recorded "". TestHistoryStack
		// pins the same outcome without the preceding plain set.
		doc, myPresence := newDoc(t, presence.Data{"color": "red"})

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("shape", "circle")
			p.Set("shape", "square", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "red", "shape": "square"}, myPresence())

		require.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": "red"}, myPresence(),
			"undo removes the key it introduced instead of setting it to \"\"")
	})

	t.Run("tracked set before a clear test", func(t *testing.T) {
		// Clear leaves the recorded values alone -- JS rebinds the proxy's
		// presence to a fresh object and never touches previousPresence
		// (presence.ts:57-64) -- so the tracked key still undoes to what it
		// held when the Update began.
		doc, myPresence := newDoc(t, presence.Data{"color": "red"})

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "blue", presence.WithHistory())
			p.Clear()
			return nil
		}))

		require.NoError(t, doc.Undo())
		assert.Equal(t, "red", myPresence()["color"])
	})

	t.Run("clear before a tracked set test", func(t *testing.T) {
		// The ordering that makes a lazily recorded value wrong: by the time
		// the tracked set runs, the key it names has already been cleared, so
		// a value read then would say the key was absent and undo would
		// remove it. It has to undo to "red".
		doc, myPresence := newDoc(t, presence.Data{"color": "red"})

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Clear()
			p.Set("color", "blue", presence.WithHistory())
			return nil
		}))

		require.NoError(t, doc.Undo())
		assert.Equal(t, "red", myPresence()["color"])
	})
}
