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

package packs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	sessionActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	stableActor  = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	peerActor    = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}
)

// packOf builds a request pack of one change per actor, in order.
func packOf(actors ...time.ActorID) *change.Pack {
	var changes []*change.Change
	for i, actorID := range actors {
		id := change.NewID(uint32(i+1), 0, int64(i+1), actorID, time.NewVersionVector())
		changes = append(changes, change.New(id, "", nil, nil))
	}

	return &change.Pack{Changes: changes}
}

// TestValidateChangeActors pins that a pushed change must be stamped with the
// pushing client's own actor. A change stored under a peer's actor is
// attributed to that peer: it is counted into the peer's lamport lane in the
// version vector, and IsOwnActor -- the predicate self-echo dedup, min-VV and
// GC all key on -- then reads it as the peer's own, which can let GC advance
// past tombstones the real owner has not seen.
func TestValidateChangeActors(t *testing.T) {
	sessionID := types.IDFromActorID(sessionActor)
	stableID := types.IDFromActorID(stableActor)

	t.Run("a change under the client's session actor", func(t *testing.T) {
		info := &database.ClientInfo{ID: sessionID}
		assert.NoError(t, validateChangeActors(info, packOf(sessionActor)))
	})

	t.Run("a change under the client's stable actor", func(t *testing.T) {
		// New SDKs stamp StableActorID, old ones the per-session ID, and
		// IsOwnActor takes either.
		info := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		assert.NoError(t, validateChangeActors(info, packOf(stableActor)))
		assert.NoError(t, validateChangeActors(info, packOf(sessionActor)))
	})

	t.Run("a change under a peer's actor", func(t *testing.T) {
		info := &database.ClientInfo{ID: sessionID}
		err := validateChangeActors(info, packOf(peerActor))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "change actor must be the pushing client")
	})

	t.Run("one change of many under a peer's actor", func(t *testing.T) {
		info := &database.ClientInfo{ID: sessionID}
		assert.Error(t, validateChangeActors(info,
			packOf(sessionActor, peerActor, sessionActor)))
	})

	t.Run("a client whose stable actor is unset never matches an empty one", func(t *testing.T) {
		// Rows written before StableActorID existed leave it empty; an actor
		// decoded from the wire is never empty, but the predicate must not
		// match on emptiness either way.
		info := &database.ClientInfo{ID: sessionID}
		assert.Error(t, validateChangeActors(info, packOf(time.InitialActorID)))
	})

	t.Run("an empty pack", func(t *testing.T) {
		info := &database.ClientInfo{ID: sessionID}
		assert.NoError(t, validateChangeActors(info, packOf()))
	})
}
