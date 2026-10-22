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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestPresenceValueIsJSONOnTheWire pins the contract of yorkie#2275: a value
// set through the Go SDK leaves JSON-encoded, which is the form the JS SDK
// parses, while the Go caller still reads back the string it set.
func TestPresenceValueIsJSONOnTheWire(t *testing.T) {
	actorID, err := time.ActorIDFromHex("000000000000000000000001")
	assert.NoError(t, err)

	t.Run("a plain string is encoded for the wire", func(t *testing.T) {
		doc := document.New("test-doc")
		doc.SetActor(actorID)
		doc.SetStatus(document.StatusAttached)
		assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
			p.Set("cursor", "moved")
			return nil
		}))

		// What a peer receives: JSON.parse("moved") throws, "\"moved\"" does not.
		assert.Equal(t, presence.Data{"cursor": `"moved"`},
			doc.InternalDocumentForTest().PresenceForTest(actorID.String()))

		// What this client reads back: the string it set.
		assert.Equal(t, presence.Data{"cursor": "moved"}, doc.MyPresence())
		assert.Equal(t, presence.Data{"cursor": "moved"}, doc.PresenceForTest(actorID.String()))
		assert.Equal(t, map[string]presence.Data{actorID.String(): {"cursor": "moved"}},
			doc.AllPresences())
	})

	t.Run("SetJSON stores an already-encoded value", func(t *testing.T) {
		doc := document.New("test-doc")
		doc.SetActor(actorID)
		doc.SetStatus(document.StatusAttached)
		assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
			p.SetJSON("cursor", `{"x":1}`)
			return nil
		}))

		assert.Equal(t, presence.Data{"cursor": `{"x":1}`},
			doc.InternalDocumentForTest().PresenceForTest(actorID.String()))

		// An object has no string to decode to, so it reads back as its
		// JSON text rather than being unwrapped.
		assert.Equal(t, presence.Data{"cursor": `{"x":1}`}, doc.MyPresence())
	})

	t.Run("a value a peer wrote is decoded once", func(t *testing.T) {
		doc := document.New("test-doc")
		doc.SetActor(actorID)
		doc.SetStatus(document.StatusAttached)
		assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
			// What a JS peer puts on the wire for set({name: 'peer'}).
			p.SetJSON("name", `"peer"`)
			return nil
		}))

		assert.Equal(t, presence.Data{"name": "peer"}, doc.MyPresence())
	})

	t.Run("undo restores the value, not its encoding", func(t *testing.T) {
		doc := document.New("test-doc")
		doc.SetActor(actorID)
		doc.SetStatus(document.StatusAttached)
		assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
			p.Set("color", "red")
			return nil
		}))
		assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
			p.Set("color", "blue", presence.WithHistory())
			return nil
		}))

		assert.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": "red"}, doc.MyPresence())
		assert.Equal(t, presence.Data{"color": `"red"`},
			doc.InternalDocumentForTest().PresenceForTest(actorID.String()))

		assert.NoError(t, doc.Redo())
		assert.Equal(t, presence.Data{"color": "blue"}, doc.MyPresence())
		assert.Equal(t, presence.Data{"color": `"blue"`},
			doc.InternalDocumentForTest().PresenceForTest(actorID.String()))
	})
}
