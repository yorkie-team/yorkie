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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestPresenceValueMustBeJSONOnTheWire covers the two opposite jobs
// FromPresenceChange has on a presence value that is not valid JSON. On the
// wire it is rejected, because every SDK reads a presence value as JSON and
// the server is the only boundary a client speaking the RPC API directly has
// to pass: accepted once, the value is persisted in the change history and the
// next snapshot and fanned out, failing the pull for every parsing peer for as
// long as it is stored. On the stored path the same rejection would make a
// document written before the check permanently unreadable, so the value is
// JSON-encoded instead.
func TestPresenceValueMustBeJSONOnTheWire(t *testing.T) {
	// A bare string is the shape a Go client wrote before presence values were
	// encoded, and the shape a hostile client would choose.
	poisoned := func() *api.PresenceChange {
		return &api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"cursor": "moved"}},
		}
	}

	t.Run("put with a non-JSON value test", func(t *testing.T) {
		_, err := converter.FromPresenceChange(poisoned())
		assert.ErrorIs(t, err, converter.ErrInvalidPresenceValue)
	})

	t.Run("patch with a non-JSON value test", func(t *testing.T) {
		_, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:        api.PresenceChange_CHANGE_TYPE_PATCH,
			Presence:    &api.Presence{Data: map[string]string{"cursor": "moved"}},
			RemovedKeys: []string{"selection"},
		})
		assert.ErrorIs(t, err, converter.ErrInvalidPresenceValue)
	})

	// An empty string is not valid JSON either, and it is what a client that
	// forgot to encode an empty value puts on the wire.
	t.Run("empty value test", func(t *testing.T) {
		_, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"cursor": ""}},
		})
		assert.ErrorIs(t, err, converter.ErrInvalidPresenceValue)
	})

	// Whatever a peer legitimately puts on the wire still decodes: an encoded
	// string, and the object and number a map[string]string has nowhere else
	// to put.
	t.Run("encoded values test", func(t *testing.T) {
		data := presence.Data{
			"cursor":    presence.EncodeValue("moved"),
			"selection": `{"from":1,"to":2}`,
			"width":     "10",
		}
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: data},
		})
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{ChangeType: presence.Put, Presence: data}, decoded)
	})

	// A Clear carries no presence, so there is nothing to validate.
	t.Run("clear test", func(t *testing.T) {
		decoded, err := converter.FromPresenceChange(&api.PresenceChange{
			Type: api.PresenceChange_CHANGE_TYPE_CLEAR,
		})
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{ChangeType: presence.Clear}, decoded)
	})

	// The ingress the attack actually uses: a pushed pack, decoded by the
	// server before anything is stored.
	t.Run("change pack ingress test", func(t *testing.T) {
		_, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: "test-doc",
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
			Changes: []*api.Change{{
				Id:             &api.ChangeID{ClientSeq: 1, Lamport: 1, ActorId: time.InitialActorID.Bytes()},
				PresenceChange: poisoned(),
			}},
		})
		assert.ErrorIs(t, err, converter.ErrInvalidPresenceValue)
	})

	t.Run("stored change repairs rather than rejects test", func(t *testing.T) {
		stored := poisoned()
		decoded, err := converter.FromStoredPresenceChange(stored)
		require.NoError(t, err)
		assert.Equal(t, &presence.Change{
			ChangeType: presence.Put,
			Presence:   presence.Data{"cursor": `"moved"`},
		}, decoded)

		// The repaired value reads as the same text it always did on this
		// side, and is now one a JS peer can parse at all.
		assert.Equal(t, "moved", presence.DecodeValue(decoded.Presence["cursor"]))

		// Repaired in place, so what the server forwards to a pulling client
		// -- which decodes through the strict path -- is the repaired value.
		assert.Equal(t, `"moved"`, stored.Presence.Data["cursor"])
	})

	t.Run("stored change leaves encoded values alone test", func(t *testing.T) {
		stored := &api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PUT,
			Presence: &api.Presence{Data: map[string]string{"cursor": `"moved"`}},
		}
		decoded, err := converter.FromStoredPresenceChange(stored)
		require.NoError(t, err)
		assert.Equal(t, presence.Data{"cursor": `"moved"`}, decoded.Presence)
	})

	t.Run("nil stored change test", func(t *testing.T) {
		decoded, err := converter.FromStoredPresenceChange(nil)
		require.NoError(t, err)
		assert.Nil(t, decoded)
	})
}
