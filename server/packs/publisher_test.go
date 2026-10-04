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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

func TestPublisherActor(t *testing.T) {
	// publisherActor logs through the context logger; a bare context resolves
	// to the package default, which is nil until it is built.
	ctx := logging.With(context.Background(), logging.DefaultLogger())

	sessionID := types.ID("000000000000000000000001")
	stableID := types.ID("000000000000000000000002")
	victimID := types.ID("000000000000000000000003")

	actorOf := func(t *testing.T, id types.ID) time.ActorID {
		actorID, err := id.ToActorID()
		assert.NoError(t, err)
		return actorID
	}

	t.Run("publish under the session id when no change was accepted", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}

		publisher, err := publisherActor(ctx, clientInfo, nil)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	t.Run("publish under the session id stamped by an old SDK", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		pushed := []*database.ChangeInfo{{ActorID: sessionID}}

		publisher, err := publisherActor(ctx, clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	t.Run("publish under the stable actor stamped by a new SDK", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		pushed := []*database.ChangeInfo{{ActorID: stableID}}

		publisher, err := publisherActor(ctx, clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, stableID), publisher)
	})

	// A client must not be able to pick another client's actor as the event
	// author: the pubsub self-echo filter drops events whose Actor equals the
	// subscriber, so an honored forgery would suppress the victim's DocChanged.
	t.Run("never publish under a foreign actor", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID, StableActorID: stableID}
		pushed := []*database.ChangeInfo{{ActorID: victimID}}

		publisher, err := publisherActor(ctx, clientInfo, pushed)
		assert.NoError(t, err)
		assert.NotEqual(t, actorOf(t, victimID), publisher)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	// Rows written before StableActorID existed leave it empty, and an empty
	// value must never match a pushed change's actor.
	t.Run("never match an empty stable actor", func(t *testing.T) {
		clientInfo := &database.ClientInfo{ID: sessionID}
		pushed := []*database.ChangeInfo{{ActorID: ""}}

		publisher, err := publisherActor(ctx, clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, actorOf(t, sessionID), publisher)
	})

	t.Run("publish a system client's push under the initial actor", func(t *testing.T) {
		systemID := types.IDFromActorID(time.InitialActorID)
		clientInfo := &database.ClientInfo{ID: systemID}
		pushed := []*database.ChangeInfo{{ActorID: systemID}}

		publisher, err := publisherActor(ctx, clientInfo, pushed)
		assert.NoError(t, err)
		assert.Equal(t, time.InitialActorID, publisher)
	})
}
