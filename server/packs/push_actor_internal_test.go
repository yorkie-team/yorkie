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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	sessionActorHex = "000000000000000000000001"
	stableActorHex  = "000000000000000000000002"
	foreignActorHex = "000000000000000000000003"
)

func actorOf(t *testing.T, hexID string) time.ActorID {
	actorID, err := time.ActorIDFromHex(hexID)
	require.NoError(t, err)
	return actorID
}

// packOf builds a one-change pack whose change ID names idActor, carrying one
// operation whose executedAt ticket names opActor.
func packOf(clientSeq uint32, idActor, opActor time.ActorID) *change.Pack {
	id := change.NewID(clientSeq, 0, int64(clientSeq), idActor, time.NewVersionVector())
	executedAt := time.NewTicket(int64(clientSeq), 0, opActor)
	ops := []operations.Operation{operations.NewRemove(time.InitialTicket, time.InitialTicket, executedAt)}
	changes := []*change.Change{change.New(id, "", ops, nil)}
	return change.NewPack(key.Key("doc"), change.InitialCheckpoint, changes, nil, nil)
}

// TestValidateChangeActors pins the push-path actor gate: a change stamped with
// an actor the pushing client row does not hold is refused with
// ErrActorMismatch, the same error the Watch path returns for the same compare.
// Without it the change is stored, applied by every peer and then dropped by
// its owner's pull as a self-echo (#2120).
func TestValidateChangeActors(t *testing.T) {
	// The default logger is built lazily, and the warn path below dereferences
	// whatever logger the context carries.
	ctx := logging.With(context.Background(), logging.DefaultLogger())
	docKey := types.DocRefKey{ProjectID: types.ID(sessionActorHex), DocID: types.ID(stableActorHex)}
	clientInfo := &database.ClientInfo{
		ID:            types.ID(sessionActorHex),
		StableActorID: types.ID(stableActorHex),
	}

	session := actorOf(t, sessionActorHex)
	stable := actorOf(t, stableActorHex)
	foreign := actorOf(t, foreignActorHex)

	t.Run("own session actor is accepted", func(t *testing.T) {
		err := validateChangeActors(ctx, clientInfo, change.InitialCheckpoint, docKey, packOf(1, session, session))
		assert.NoError(t, err)
	})

	t.Run("own stable actor is accepted", func(t *testing.T) {
		err := validateChangeActors(ctx, clientInfo, change.InitialCheckpoint, docKey, packOf(1, stable, stable))
		assert.NoError(t, err)
	})

	// A pre-attach edit in a client that skipped SetActor keeps the initial
	// actor, and no client holds it on pull, so a change stamped with it
	// suppresses nobody. Refusing it would be a new wire precondition.
	t.Run("initial actor is accepted", func(t *testing.T) {
		err := validateChangeActors(
			ctx, clientInfo, change.InitialCheckpoint, docKey,
			packOf(1, time.InitialActorID, time.InitialActorID),
		)
		assert.NoError(t, err)
	})

	t.Run("another client's actor in the change id is refused", func(t *testing.T) {
		err := validateChangeActors(ctx, clientInfo, change.InitialCheckpoint, docKey, packOf(1, foreign, foreign))
		assert.ErrorIs(t, err, clients.ErrActorMismatch)
	})

	// A client row written before StableActorID existed holds only a session
	// id; its own actor must still pass and a foreign one must still be refused.
	t.Run("a row without a stable actor still compares on the session id", func(t *testing.T) {
		legacy := &database.ClientInfo{ID: types.ID(sessionActorHex)}
		assert.NoError(t, validateChangeActors(ctx, legacy, change.InitialCheckpoint, docKey, packOf(1, session, session)))
		assert.ErrorIs(t,
			validateChangeActors(ctx, legacy, change.InitialCheckpoint, docKey, packOf(1, stable, stable)),
			clients.ErrActorMismatch,
		)
	})

	// The server's own writers hold the initial actor as their session id, so
	// IsOwnActor accepts them and the exemption above is not what carries them.
	t.Run("the server client's own actor is accepted", func(t *testing.T) {
		server := &database.ClientInfo{ID: types.IDFromActorID(time.InitialActorID)}
		require.True(t, server.IsServerClient())
		err := validateChangeActors(
			ctx, server, change.InitialCheckpoint, docKey,
			packOf(1, time.InitialActorID, time.InitialActorID),
		)
		assert.NoError(t, err)
	})

	// Only changes the server would store are checked; pushPack drops the rest,
	// so an already-acknowledged change cannot fail a resend.
	t.Run("an already-pushed change is not checked", func(t *testing.T) {
		cp := change.Checkpoint{ClientSeq: 1}
		err := validateChangeActors(ctx, clientInfo, cp, docKey, packOf(1, foreign, foreign))
		assert.NoError(t, err)
	})

	// An operation's executedAt keeps the actor it was minted under, which the
	// pull dedup and presence keying never read, so it is logged, not refused.
	t.Run("a foreign actor in an operation ticket is logged, not refused", func(t *testing.T) {
		err := validateChangeActors(ctx, clientInfo, change.InitialCheckpoint, docKey, packOf(1, session, foreign))
		assert.NoError(t, err)
	})

	t.Run("the first foreign change in a pack refuses the whole pack", func(t *testing.T) {
		pack := packOf(1, session, session)
		pack.Changes = append(pack.Changes, packOf(2, foreign, foreign).Changes...)
		assert.ErrorIs(t,
			validateChangeActors(ctx, clientInfo, change.InitialCheckpoint, docKey, pack),
			clients.ErrActorMismatch,
		)
	})
}
