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

package converter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestPresenceValueIsRepairedToJSON covers the one job FromPresenceChange has
// on a presence value no peer can parse: encode it, so what the server stores
// and fans out is always a value every peer reads. Rejecting instead is not
// available to it, because the same decode sits on the server's ingress, on
// the Go client's pull and on every stored read -- see repairPresence.
func TestPresenceValueIsRepairedToJSON(t *testing.T) {
	// A bare string is the shape a Go client older than presence.EncodeValue
	// writes, and the shape a client speaking the RPC API directly would.
	poisoned := func() *api.PresenceChange {
		return &api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"cursor": "moved"}},
		}
	}

	t.Run("put with a non-JSON value test", func(t *testing.T) {
		pbChange := poisoned()
		decoded, err := converter.FromPresenceChange(pbChange)
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{
			ChangeType: presence.Put,
			Presence:   presence.Data{"cursor": `"moved"`},
		}, decoded)

		// The repaired value reads as the same text it always did on this
		// side, and is now one a JS peer can parse at all.
		assert.Equal(t, "moved", presence.DecodeValue(decoded.Presence["cursor"]))

		// Repaired in place, so what the server stores and forwards to a
		// pulling client is the repaired value rather than the original.
		assert.Equal(t, `"moved"`, pbChange.Presence.Data["cursor"])
	})

	t.Run("patch with a non-JSON value test", func(t *testing.T) {
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:        api.PresenceChange_CHANGE_TYPE_PATCH,
			Presence:    &api.Presence{Data: map[string]string{"cursor": "moved"}},
			RemovedKeys: []string{"selection"},
		})
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{
			ChangeType:  presence.Patch,
			Presence:    presence.Data{"cursor": `"moved"`},
			RemovedKeys: []string{"selection"},
		}, decoded)
	})

	// An empty string is not valid JSON either, and it is what a client that
	// forgot to encode an empty value puts on the wire.
	t.Run("empty value test", func(t *testing.T) {
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"cursor": ""}},
		})
		require.NoError(t, err)
		assert.Equal(t, presence.Data{"cursor": `""`}, decoded.Presence)
	})

	// Valid JSON is not enough: Go's scanner accepts nesting far past where a
	// recursive-descent parser such as V8's JSON.parse overflows its stack, so
	// a value nested beyond the bound is repaired like unparseable text.
	t.Run("deeply nested value test", func(t *testing.T) {
		nested := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"deep": nested}},
		})
		require.NoError(t, err)
		assert.Equal(t, presence.EncodeValue(nested), decoded.Presence["deep"])
		assert.Equal(t, nested, presence.DecodeValue(decoded.Presence["deep"]))
	})

	// Nesting a presence payload plausibly reaches is left alone, so the bound
	// repairs only what parsers actually give out on.
	t.Run("modestly nested value test", func(t *testing.T) {
		nested := strings.Repeat("[", 20) + strings.Repeat("]", 20)
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"deep": nested}},
		})
		require.NoError(t, err)
		assert.Equal(t, nested, decoded.Presence["deep"])
	})

	// Whatever a peer legitimately puts on the wire decodes untouched: an
	// encoded string, and the object and number a map[string]string has
	// nowhere else to put. A brace inside a string is a character, not a
	// nesting level, so it must not move the depth count.
	t.Run("encoded values test", func(t *testing.T) {
		data := presence.Data{
			"cursor":    presence.EncodeValue("moved"),
			"selection": `{"from":1,"to":2}`,
			"width":     "10",
			"label":     presence.EncodeValue(`a {[ b \" c`),
		}
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: data},
		})
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{ChangeType: presence.Put, Presence: data}, decoded)
	})

	// A Clear carries no presence, so there is nothing to repair.
	t.Run("clear test", func(t *testing.T) {
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type: api.PresenceChange_CHANGE_TYPE_CLEAR,
		})
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{ChangeType: presence.Clear}, decoded)
	})

	t.Run("nil change test", func(t *testing.T) {
		decoded, err := converter.FromPresenceChange(nil)
		require.NoError(t, err)
		assert.Nil(t, decoded)
	})

	// The server's ingress: a pushed pack, decoded before anything is stored.
	// A client on a Go SDK predating the encoding pushes exactly this, and it
	// has to go through rather than be refused for as long as it runs.
	t.Run("change pack ingress test", func(t *testing.T) {
		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: "test-doc",
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
			Changes: []*api.Change{{
				Id:             &api.ChangeID{ClientSeq: 1, Lamport: 1, ActorId: time.InitialActorID.Bytes()},
				PresenceChange: poisoned(),
			}},
		})
		require.NoError(t, err)
		require.Len(t, pack.Changes, 1)
		assert.Equal(t, presence.Data{"cursor": `"moved"`}, pack.Changes[0].PresenceChange().Presence)
	})
}

// TestStoredSnapshotPresenceIsRepaired covers the snapshot read path, which
// decodes presences through fromPresences rather than FromPresenceChange. A
// snapshot written before presence values had to be JSON has to stay loadable,
// and the document rebuilt from it must not carry the unparseable value into
// the next snapshot it writes.
func TestStoredSnapshotPresenceIsRepaired(t *testing.T) {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	bytes, err := converter.SnapshotToBytes(root, map[string]presence.Data{
		"c1": {"cursor": "moved", "width": "10"},
	})
	require.NoError(t, err)

	_, presences, err := converter.BytesToSnapshot(bytes)
	require.NoError(t, err)

	loaded := presences.Load("c1")
	require.NotNil(t, loaded)
	assert.Equal(t, `"moved"`, loaded["cursor"])
	assert.Equal(t, "moved", presence.DecodeValue(loaded["cursor"]))

	// Already-encoded values are left exactly as they were written.
	assert.Equal(t, "10", loaded["width"])
}
