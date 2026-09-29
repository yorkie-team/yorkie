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

// TestPresenceWritesThroughTheClone pins that Clear and Initialize mutate the
// very map the document's clone holds for this actor, rather than rebinding a
// local map header and leaving the clone untouched.
//
// The clone survives a successful Update, so the next Update builds its Put
// from whatever the clone holds. A wipe or a replacement that missed the clone
// therefore shows up one Update later: the following Set re-broadcasts keys
// that were cleared, or drops keys that were initialized. Every assertion
// below is on the presence after that following Set.
func TestPresenceWritesThroughTheClone(t *testing.T) {
	myPresence := func(doc *document.Document) presence.Data {
		return doc.PresenceForTest(doc.ActorID().String())
	}

	t.Run("clear empties the clone test", func(t *testing.T) {
		doc := document.New("d1")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "red")
			return nil
		}))
		require.Equal(t, presence.Data{"color": "red"}, myPresence(doc))

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Clear()
			return nil
		}))
		assert.Empty(t, myPresence(doc), "clear drops this actor's presence from the root")

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("shape", "circle")
			return nil
		}))
		assert.Equal(t, presence.Data{"shape": "circle"}, myPresence(doc),
			"the set after a clear must not resurrect the cleared key from the clone")
	})

	t.Run("clear then set within one update test", func(t *testing.T) {
		// The same wipe, with the following Set in the same Update: the Put
		// this change carries is built on the cleared map, so the cleared key
		// must not ride along with it.
		doc := document.New("d1")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "red")
			return nil
		}))

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Clear()
			p.Set("shape", "circle")
			return nil
		}))
		assert.Equal(t, presence.Data{"shape": "circle"}, myPresence(doc))
	})

	t.Run("initialize writes through the clone test", func(t *testing.T) {
		doc := document.New("d1")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "red")
			return nil
		}))

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Initialize(presence.Data{"name": "yorkie"})
			return nil
		}))
		require.Equal(t, presence.Data{"name": "yorkie"}, myPresence(doc),
			"initialize replaces the presence rather than merging into it")

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("shape", "circle")
			return nil
		}))
		assert.Equal(t, presence.Data{"name": "yorkie", "shape": "circle"}, myPresence(doc),
			"the set after an initialize must keep the initialized keys and drop the replaced ones")
	})
}
