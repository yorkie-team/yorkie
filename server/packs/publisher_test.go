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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	sessionID = types.ID("000000000000000000000001")
	stableID  = types.ID("000000000000000000000002")
	victimID  = types.ID("000000000000000000000003")
)

func actorOf(t *testing.T, id types.ID) time.ActorID {
	t.Helper()
	actorID, err := id.ToActorID()
	assert.NoError(t, err)
	return actorID
}

func TestPublisherActor(t *testing.T) {
	t.Run("publish under the session id when no change was accepted", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}

		publisher, err := publisherActor(clientInfo, nil)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	t.Run("publish under the session id stamped by an old SDK", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		pushed := []*database.ChangeInfo{{ActorID: sessionID}}

		publisher, err := publisherActor(clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	t.Run("publish under the stable actor stamped by a new SDK", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		pushed := []*database.ChangeInfo{{ActorID: stableID}}

		publisher, err := publisherActor(clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, stableID), publisher)
	})
}

func TestValidateChangeActors(t *testing.T) {
	packOf := func(t *testing.T, cp change.Checkpoint, actors ...types.ID) *change.Pack {
		t.Helper()
		var changes []*change.Change
		for i, actor := range actors {
			id := change.NewID(uint32(i+1), 0, int64(i+1), actorOf(t, actor), time.NewVersionVector())
			changes = append(changes, change.New(id, "", nil, nil))
		}
		return change.NewPack("doc", cp, changes, nil, nil)
	}

	client := &database.ClientInfo{ID: sessionID, StableActorID: stableID}

	t.Run("accept the session id and the stable actor", func(t *testing.T) {
		pack := packOf(t, change.InitialCheckpoint, sessionID, stableID)
		assert.NoError(t, validateChangeActors(client, change.InitialCheckpoint, pack))
	})

	// A forged actor would make pullChangeInfos treat the change as the
	// victim's own, and the victim would silently drop it.
	t.Run("refuse another client's actor", func(t *testing.T) {
		pack := packOf(t, change.InitialCheckpoint, sessionID, victimID)
		err := validateChangeActors(client, change.InitialCheckpoint, pack)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	// The initial actor belongs to the server; a client never pushes it,
	// even for edits made before its attach (the SDK stamps its own actor
	// into the change ID on attach).
	t.Run("refuse the initial actor from a client", func(t *testing.T) {
		initial := types.IDFromActorID(time.InitialActorID)
		pack := packOf(t, change.InitialCheckpoint, initial)
		err := validateChangeActors(client, change.InitialCheckpoint, pack)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	// Rows written before StableActorID existed leave it empty; the empty
	// value must not open a hole.
	t.Run("refuse a foreign actor when the stable actor is empty", func(t *testing.T) {
		legacy := &database.ClientInfo{ID: sessionID}
		pack := packOf(t, change.InitialCheckpoint, stableID)
		err := validateChangeActors(legacy, change.InitialCheckpoint, pack)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("accept the system client's initial actor", func(t *testing.T) {
		systemID := types.IDFromActorID(time.InitialActorID)
		system := &database.ClientInfo{ID: systemID}
		pack := packOf(t, change.InitialCheckpoint, systemID)
		assert.NoError(t, validateChangeActors(system, change.InitialCheckpoint, pack))
	})

	t.Run("skip changes already acknowledged", func(t *testing.T) {
		cp := change.InitialCheckpoint.SyncClientSeq(1)
		pack := packOf(t, cp, victimID, sessionID)
		assert.NoError(t, validateChangeActors(client, cp, pack))
	})
}
